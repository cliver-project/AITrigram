/*
Copyright 2025 Red Hat, Inc.

Authors: Lin Gao <lgao@redhat.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

const (
	ModelRepositoryFinalizer = "modelrepository.aitrigram.cliver-project.github.io/finalizer"
)

// ModelRepositoryReconciler reconciles a ModelRepository object
type ModelRepositoryReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	OperatorNamespace string
}

// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=modelrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=modelrepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=modelrepositories/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

func (r *ModelRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ModelRepository instance
	modelRepo := &aitrigramv1.ModelRepository{}
	if err := r.Get(ctx, req.NamespacedName, modelRepo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Default ModelName to metadata.name if not specified
	if modelRepo.Spec.ModelName == "" {
		modelRepo.Spec.ModelName = modelRepo.Name
		if err := r.Update(ctx, modelRepo); err != nil {
			logger.Error(err, "Failed to set default ModelName")
			return ctrl.Result{}, err
		}
		// Requeue to process with the updated spec
		return ctrl.Result{Requeue: true}, nil
	}

	// Handle deletion
	if modelRepo.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, r.handleDeletion(ctx, modelRepo)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(modelRepo, ModelRepositoryFinalizer) {
		controllerutil.AddFinalizer(modelRepo, ModelRepositoryFinalizer)
		if err := r.Update(ctx, modelRepo); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Check if autoDownload is enabled and model is not already downloaded
	// Retry failed downloads automatically (allows recovery from transient errors like namespace not found)
	if modelRepo.Spec.AutoDownload && (modelRepo.Status.Phase == "" || modelRepo.Status.Phase == aitrigramv1.ModelRepositoryPhasePending || modelRepo.Status.Phase == aitrigramv1.ModelRepositoryPhaseFailed) {
		// If failed, add a delay before retrying to avoid tight loops
		if modelRepo.Status.Phase == aitrigramv1.ModelRepositoryPhaseFailed {
			// Check if enough time has passed since last failure (30 seconds)
			if modelRepo.Status.LastUpdated != nil && time.Since(modelRepo.Status.LastUpdated.Time) < 30*time.Second {
				remainingTime := 30*time.Second - time.Since(modelRepo.Status.LastUpdated.Time)
				logger.Info("Waiting before retrying failed download", "remainingTime", remainingTime)
				return ctrl.Result{RequeueAfter: remainingTime}, nil
			}
			logger.Info("Retrying failed download", "modelRepo", modelRepo.Name)
		}

		// Update status to downloading
		modelRepo.Status.Phase = aitrigramv1.ModelRepositoryPhaseDownloading
		modelRepo.Status.Reason = "StartingDownload"
		modelRepo.Status.Message = "Starting model download process"
		modelRepo.Status.LastUpdated = &metav1.Time{Time: time.Now()}
		if err := r.Status().Update(ctx, modelRepo); err != nil {
			logger.Error(err, "Failed to update ModelRepository status")
			return ctrl.Result{}, err
		}

		// Create download job
		job, err := r.createDownloadJob(ctx, modelRepo)
		if err != nil {
			logger.Error(err, "Failed to create download job")
			modelRepo.Status.Phase = aitrigramv1.ModelRepositoryPhaseFailed
			modelRepo.Status.Reason = "JobCreationFailed"
			modelRepo.Status.Message = fmt.Sprintf("Failed to create download job: %v", err)
			modelRepo.Status.LastUpdated = &metav1.Time{Time: time.Now()}
			_ = r.Status().Update(ctx, modelRepo)
			return ctrl.Result{}, err
		}

		// Set owner reference
		if err := ctrl.SetControllerReference(modelRepo, job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		// Create the job
		if err := r.Create(ctx, job); err != nil && !errors.IsAlreadyExists(err) {
			logger.Error(err, "Failed to create download job")
			modelRepo.Status.Phase = aitrigramv1.ModelRepositoryPhaseFailed
			modelRepo.Status.Reason = "JobCreationFailed"
			modelRepo.Status.Message = fmt.Sprintf("Failed to create download job: %v", err)
			modelRepo.Status.LastUpdated = &metav1.Time{Time: time.Now()}
			_ = r.Status().Update(ctx, modelRepo)
			return ctrl.Result{}, err
		}
	} else if modelRepo.Spec.AutoDownload && modelRepo.Status.Phase == aitrigramv1.ModelRepositoryPhaseDownloading {
		// Check job status
		job := &batchv1.Job{}
		jobName := fmt.Sprintf("download-%s", modelRepo.Name)
		namespace := r.OperatorNamespace
		if namespace == "" {
			namespace = "default"
		}
		if err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: namespace}, job); err != nil {
			if !errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			// Job not found, reset status
			modelRepo.Status.Phase = aitrigramv1.ModelRepositoryPhasePending
			modelRepo.Status.Reason = "JobNotFound"
			modelRepo.Status.Message = "Download job not found, resetting to pending"
			modelRepo.Status.LastUpdated = &metav1.Time{Time: time.Now()}
			_ = r.Status().Update(ctx, modelRepo)
			return ctrl.Result{RequeueAfter: time.Second * 60}, nil
		}

		// Check job completion
		if job.Status.Succeeded > 0 {
			modelRepo.Status.Phase = aitrigramv1.ModelRepositoryPhaseDownloaded
			modelRepo.Status.Reason = "Success"
			modelRepo.Status.Message = "Model successfully downloaded"
			modelRepo.Status.LastUpdated = &metav1.Time{Time: time.Now()}
			if err := r.Status().Update(ctx, modelRepo); err != nil {
				return ctrl.Result{}, err
			}
		} else if job.Status.Failed > 0 {
			modelRepo.Status.Phase = aitrigramv1.ModelRepositoryPhaseFailed
			modelRepo.Status.Reason = "DownloadFailed"
			modelRepo.Status.Message = "Model download failed"
			modelRepo.Status.LastUpdated = &metav1.Time{Time: time.Now()}
			if err := r.Status().Update(ctx, modelRepo); err != nil {
				return ctrl.Result{}, err
			}
			// Requeue to retry
			return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
		}
	}

	return ctrl.Result{}, nil
}

func (r *ModelRepositoryReconciler) createDownloadJob(ctx context.Context, modelRepo *aitrigramv1.ModelRepository) (*batchv1.Job, error) {
	jobName := fmt.Sprintf("download-%s", modelRepo.Name)

	// Build workload configuration using the new workload struct
	workload, err := BuildModelRepositoryWorkload(modelRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to build ModelRepository workload: %w", err)
	}

	// Use operator namespace if available, otherwise fall back to default
	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = "default"
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "model-downloader",
							Image:           workload.DownloadImage,
							Args:            workload.DownloadArgs,
							Env:             workload.Envs,
							VolumeMounts:    workload.Storage.VolumeMounts,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &[]bool{false}[0],
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								RunAsNonRoot: &[]bool{true}[0],
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
							},
						},
					},
					Volumes:       workload.Storage.Volumes,
					RestartPolicy: corev1.RestartPolicyOnFailure,
				},
			},
		},
	}

	return job, nil
}

func (r *ModelRepositoryReconciler) handleDeletion(ctx context.Context, modelRepo *aitrigramv1.ModelRepository) error {
	logger := log.FromContext(ctx)

	// Check if any LLMEngine still references this ModelRepository
	llmEngines := &aitrigramv1.LLMEngineList{}
	if err := r.List(ctx, llmEngines); err != nil {
		logger.Error(err, "Failed to list LLMEngines")
		return err
	}

	dependentEngines := []string{}
	for _, engine := range llmEngines.Items {
		for _, ref := range engine.Spec.ModelRefs {
			if ref == modelRepo.Name {
				dependentEngines = append(dependentEngines, engine.Name)
				break
			}
		}
	}

	if len(dependentEngines) > 0 {
		// Block deletion - update status and requeue
		msg := fmt.Sprintf("Cannot delete: still referenced by LLMEngines: %v. Please delete the LLMEngines first or remove the modelRef.", dependentEngines)
		logger.Info("Blocking ModelRepository deletion", "reason", msg)

		// Update status to inform the user
		modelRepo.Status.Phase = aitrigramv1.ModelRepositoryPhaseFailed
		modelRepo.Status.Message = msg
		if err := r.Status().Update(ctx, modelRepo); err != nil {
			logger.Error(err, "Failed to update status")
		}

		// Return error to requeue and keep checking
		return fmt.Errorf("deletion blocked: %s", msg)
	}

	// No dependencies - proceed with cleanup
	logger.Info("No LLMEngine dependencies found, proceeding with deletion")

	// Create a cleanup job to delete the model files from the storage
	if err := r.createCleanupJob(ctx, modelRepo); err != nil {
		logger.Error(err, "Failed to create cleanup job, will still remove finalizer")
		// We still want to remove the finalizer even if cleanup fails
		// to avoid blocking deletion, but log the error
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(modelRepo, ModelRepositoryFinalizer)
	return r.Update(ctx, modelRepo)
}

func (r *ModelRepositoryReconciler) createCleanupJob(ctx context.Context, modelRepo *aitrigramv1.ModelRepository) error {
	jobName := fmt.Sprintf("cleanup-%s", modelRepo.Name)

	// Build workload configuration using the new workload struct
	workload, err := BuildModelRepositoryWorkload(modelRepo)
	if err != nil {
		return fmt.Errorf("failed to build ModelRepository workload: %w", err)
	}

	// Use operator namespace if available, otherwise fall back to default
	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = "default"
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			// Set TTL to automatically delete the job after completion
			// TTLSecondsAfterFinished: func(i int32) *int32 { return &i }(1800), // 30 minutes
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "cleanup",
							Image:           workload.CleanupImage,
							Args:            workload.CleanupArgs,
							VolumeMounts:    workload.Storage.VolumeMounts,
							ImagePullPolicy: corev1.PullIfNotPresent,
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &[]bool{false}[0],
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								RunAsNonRoot: &[]bool{true}[0],
								SeccompProfile: &corev1.SeccompProfile{
									Type: corev1.SeccompProfileTypeRuntimeDefault,
								},
							},
						},
					},
					Volumes:       workload.Storage.Volumes,
					RestartPolicy: corev1.RestartPolicyOnFailure,
				},
			},
		},
	}

	// Create the cleanup job (don't set owner reference as the owner is being deleted)
	return r.Create(ctx, job)
}

func (r *ModelRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aitrigramv1.ModelRepository{}).
		Owns(&batchv1.Job{}).
		Named("modelrepository").
		Complete(r)
}
