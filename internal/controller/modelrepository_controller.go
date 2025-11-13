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
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/storage"
)

const (
	ModelRepositoryFinalizer = "modelrepository.aitrigram.cliver-project.github.io/finalizer"
)

// ModelRepositoryReconciler reconciles a ModelRepository object
type ModelRepositoryReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	OperatorNamespace string
	StorageFactory    *storage.ProviderFactory
}

// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=modelrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=modelrepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=modelrepositories/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete

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

	// Create storage provider early so it can be reused throughout reconciliation
	provider, err := r.StorageFactory.CreateProvider(modelRepo)
	if err != nil {
		// During deletion, we still want to proceed even if provider creation fails
		if modelRepo.GetDeletionTimestamp() != nil {
			logger.Error(err, "Failed to create storage provider during deletion, will proceed with deletion")
			return ctrl.Result{}, r.handleDeletion(ctx, modelRepo, nil)
		}
		// During normal operation, fail and requeue
		logger.Error(err, "Failed to create storage provider")
		_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.ModelRepositoryPhaseFailed,
			"StorageProviderFailed", fmt.Sprintf("Failed to create storage provider: %v", err))
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Handle deletion
	if modelRepo.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, r.handleDeletion(ctx, modelRepo, provider)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(modelRepo, ModelRepositoryFinalizer) {
		controllerutil.AddFinalizer(modelRepo, ModelRepositoryFinalizer)
		if err := r.Update(ctx, modelRepo); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Validate storage configuration
	if err := provider.ValidateConfig(); err != nil {
		logger.Error(err, "Storage configuration validation failed")
		_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.ModelRepositoryPhaseFailed,
			"StorageValidationFailed", fmt.Sprintf("Storage validation failed: %v", err))
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Provision storage if needed (for ProvisionableProvider)
	if provisionable, ok := provider.(storage.ProvisionableProvider); ok {
		namespace := r.OperatorNamespace
		if namespace == "" {
			namespace = "default"
		}
		if err := provisionable.CreateStorage(ctx, namespace); err != nil {
			logger.Error(err, "Failed to provision storage")
			_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.ModelRepositoryPhaseFailed,
				"StorageProvisionFailed", fmt.Sprintf("Failed to provision storage: %v", err))
			return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		}

		// Check storage status, in case of PVC storage, it tries to get the status of the PVC, others are typically just ready after CreateStorage
		status, err := provisionable.GetStatus(ctx, namespace)
		if err != nil {
			logger.Error(err, "Failed to get storage status")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		if !status.Ready {
			logger.Info("Storage not ready yet, will retry", "message", status.Message)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
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
		if err := r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.ModelRepositoryPhaseDownloading, "StartingDownload", "Starting model download process"); err != nil {
			logger.Error(err, "Failed to update ModelRepository status")
			return ctrl.Result{}, err
		}

		// Delete any stale cleanup job from a previous deletion to avoid conflicts
		namespace := r.OperatorNamespace
		if namespace == "" {
			namespace = "default"
		}
		cleanupJobName := fmt.Sprintf("cleanup-%s", modelRepo.Name)
		cleanupJob := &batchv1.Job{}
		if err := r.Get(ctx, client.ObjectKey{Name: cleanupJobName, Namespace: namespace}, cleanupJob); err == nil {
			// Cleanup job exists, delete it
			logger.Info("Found stale cleanup job, deleting it before creating download job",
				"cleanupJob", cleanupJobName,
				"modelRepo", modelRepo.Name)
			if err := r.Delete(ctx, cleanupJob, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
				logger.Error(err, "Failed to delete stale cleanup job", "cleanupJob", cleanupJobName)
				// Continue anyway - the download job creation might still succeed
			}
		} else if !errors.IsNotFound(err) {
			logger.Error(err, "Failed to check for stale cleanup job", "cleanupJob", cleanupJobName)
			// Continue anyway
		}

		// Create download job
		job, err := r.createDownloadJob(ctx, modelRepo, provider)
		if err != nil {
			logger.Error(err, "Failed to create download job")
			_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.ModelRepositoryPhaseFailed, "JobCreationFailed", fmt.Sprintf("Failed to create download job: %v", err))
			return ctrl.Result{}, err
		}

		// Set owner reference
		if err := ctrl.SetControllerReference(modelRepo, job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		// Create the job
		if err := r.Create(ctx, job); err != nil && !errors.IsAlreadyExists(err) {
			logger.Error(err, "Failed to create download job")
			_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.ModelRepositoryPhaseFailed, "JobCreationFailed", fmt.Sprintf("Failed to create download job: %v", err))
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
			_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.ModelRepositoryPhasePending, "JobNotFound", "Download job not found, resetting to pending")
			return ctrl.Result{RequeueAfter: time.Second * 60}, nil
		}

		// For storage that requires node affinity, track which node the download job is running on
		// job has been created, check if we need to update BoundNodeName
		if nodeBound, ok := provider.(storage.NodeBoundProvider); ok && !nodeBound.IsShared() {
			if modelRepo.Status.BoundNodeName == "" {
				nodeName, err := r.getDownloadJobNodeName(ctx, modelRepo)
				if err != nil {
					logger.Error(err, "Failed to get download job node name")
					// Retry after 10 seconds
					return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
				} else if nodeName != "" {
					// Update status with bound node name
					if err := r.updateBoundNodeName(ctx, modelRepo, nodeName); err != nil {
						logger.Error(err, "Failed to update bound node name")
						return ctrl.Result{}, err
					}
					logger.Info("Successfully bound ModelRepository to node",
						"modelRepo", modelRepo.Name,
						"node", nodeName,
						"storageType", provider.GetType())
				} else {
					// Node not yet assigned, retry after 10 seconds
					logger.Info("Download job pod not yet scheduled to a node, will retry",
						"modelRepo", modelRepo.Name,
						"job", fmt.Sprintf("download-%s", modelRepo.Name),
						"retryAfter", "10s")
					return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
				}
			}
		}

		// Check job completion
		if job.Status.Succeeded > 0 {
			if err := r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.ModelRepositoryPhaseDownloaded, "Success", "Model successfully downloaded"); err != nil {
				return ctrl.Result{}, err
			}
		} else if job.Status.Failed > 0 {
			if err := r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.ModelRepositoryPhaseFailed, "DownloadFailed", "Model download failed"); err != nil {
				return ctrl.Result{}, err
			}
			// Requeue to retry
			return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
		}
	}

	return ctrl.Result{}, nil
}

func (r *ModelRepositoryReconciler) createDownloadJob(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, provider storage.Provider) (*batchv1.Job, error) {
	jobName := fmt.Sprintf("download-%s", modelRepo.Name)

	// Get volume and volume mount from provider
	volume, volumeMount, err := provider.PrepareDownloadVolume()
	if err != nil {
		return nil, fmt.Errorf("failed to prepare download volume: %w", err)
	}

	// Use operator namespace if available, otherwise fall back to default
	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = "default"
	}

	// Get mount path from storage (use MountPath or default)
	mountPath := modelRepo.Spec.Storage.MountPath
	if mountPath == "" {
		mountPath = "/data/models" // Default mount path
	}

	// Get node affinity if needed
	var nodeAffinity *corev1.NodeAffinity
	if nodeBound, ok := provider.(storage.NodeBoundProvider); ok {
		nodeAffinity = nodeBound.GetNodeAffinity()
	}

	// Create JobBuilder using the asset system
	origin := string(modelRepo.Spec.Source.Origin)
	builder, err := NewJobBuilder(
		ctx,
		r.Client,
		origin,
		mountPath,
		volume,
		volumeMount,
		modelRepo.Spec.NodeSelector,
		nodeAffinity,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create job builder: %w", err)
	}

	// Prepare parameters for the download script, these all will be injected as environment variables to the download job
	params := map[string]string{
		"MODEL_ID":   modelRepo.Spec.Source.ModelId,
		"MODEL_NAME": modelRepo.Spec.ModelName,
		"MOUNT_PATH": mountPath,
	}

	// Build the download job using the asset system
	// Pass existing CRD fields for customization
	job, err := builder.BuildDownloadJob(
		jobName,
		namespace,
		modelRepo.Name,
		modelRepo.Spec.DownloadImage,
		modelRepo.Spec.DownloadScripts,
		params,
		&modelRepo.Spec.Source,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build download job: %w", err)
	}

	return job, nil
}

func (r *ModelRepositoryReconciler) handleDeletion(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, provider storage.Provider) error {
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
		if err := r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.ModelRepositoryPhaseFailed, "DeletionBlocked", msg); err != nil {
			logger.Error(err, "Failed to update status")
		}

		// Return error to requeue and keep checking
		return fmt.Errorf("deletion blocked: %s", msg)
	}

	// No dependencies - proceed with cleanup
	logger.Info("No LLMEngine dependencies found, proceeding with deletion")

	// Only proceed with cleanup if provider was successfully created
	if provider != nil {
		// Only create cleanup job if model was actually downloaded
		// Skip cleanup if status is empty or pending (no files to clean up)
		shouldCleanup := modelRepo.Status.Phase != "" &&
			modelRepo.Status.Phase != aitrigramv1.ModelRepositoryPhasePending

		if shouldCleanup {
			logger.Info("Creating cleanup job to remove model files",
				"phase", modelRepo.Status.Phase)
			// Create a cleanup job to delete the model files from the storage
			if err := r.createCleanupJob(ctx, modelRepo, provider); err != nil {
				logger.Error(err, "Failed to create cleanup job, will still remove finalizer")
				// We still want to remove the finalizer even if cleanup fails
				// to avoid blocking deletion, but log the error
			}
		} else {
			logger.Info("Skipping cleanup job - model was never downloaded",
				"phase", modelRepo.Status.Phase)
		}

		// Cleanup storage if provider supports it
		if provisionable, ok := provider.(storage.ProvisionableProvider); ok {
			namespace := r.OperatorNamespace
			if namespace == "" {
				namespace = "default"
			}
			if err := provisionable.Cleanup(ctx, namespace); err != nil {
				logger.Error(err, "Failed to cleanup storage, will still remove finalizer")
				// Continue with deletion even if cleanup fails
			}
		}
	} else {
		logger.Info("Provider is nil, skipping cleanup job and storage cleanup")
	}

	// Remove finalizer with retry to handle conflicts
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get fresh copy
		key := client.ObjectKeyFromObject(modelRepo)
		fresh := &aitrigramv1.ModelRepository{}
		if err := r.Get(ctx, key, fresh); err != nil {
			return err
		}

		// Remove finalizer from fresh copy
		controllerutil.RemoveFinalizer(fresh, ModelRepositoryFinalizer)
		return r.Update(ctx, fresh)
	})
}

func (r *ModelRepositoryReconciler) createCleanupJob(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, provider storage.Provider) error {
	jobName := fmt.Sprintf("cleanup-%s", modelRepo.Name)

	// Get volume and volume mount from provider (same as download)
	volume, volumeMount, err := provider.PrepareDownloadVolume()
	if err != nil {
		return fmt.Errorf("failed to prepare cleanup volume: %w", err)
	}

	// Use operator namespace if available, otherwise fall back to default
	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = "default"
	}

	// Get mount path from storage (use MountPath or default)
	mountPath := modelRepo.Spec.Storage.MountPath
	if mountPath == "" {
		mountPath = "/data/models" // Default mount path
	}

	// Get node affinity if needed
	var nodeAffinity *corev1.NodeAffinity
	if nodeBound, ok := provider.(storage.NodeBoundProvider); ok {
		nodeAffinity = nodeBound.GetNodeAffinity()
	}

	// Create JobBuilder using the asset system
	origin := string(modelRepo.Spec.Source.Origin)
	builder, err := NewJobBuilder(
		ctx,
		r.Client,
		origin,
		mountPath,
		volume,
		volumeMount,
		modelRepo.Spec.NodeSelector,
		nodeAffinity,
	)
	if err != nil {
		return fmt.Errorf("failed to create job builder: %w", err)
	}

	// Prepare parameters for the cleanup script
	params := map[string]string{
		"MODEL_ID":   modelRepo.Spec.Source.ModelId,
		"MODEL_NAME": modelRepo.Spec.ModelName,
		"MOUNT_PATH": mountPath,
	}

	// Build the cleanup job using the asset system
	// Pass existing CRD fields for customization
	job, err := builder.BuildCleanupJob(
		jobName,
		namespace,
		modelRepo.Name,
		modelRepo.Spec.DownloadImage, // Reuse download image for cleanup
		modelRepo.Spec.DeleteScripts,
		params,
		&modelRepo.Spec.Source,
	)
	if err != nil {
		return fmt.Errorf("failed to build cleanup job: %w", err)
	}

	// Set TTL to auto-delete the job after completion (600 seconds = 10 minutes)
	// This prevents stale cleanup jobs from conflicting with future ModelRepositories
	ttl := int32(600)
	job.Spec.TTLSecondsAfterFinished = &ttl

	// Check if cleanup job already exists
	existingJob := &batchv1.Job{}
	err = r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: namespace}, existingJob)
	if err == nil {
		// Job already exists, nothing to do
		log.FromContext(ctx).Info("Cleanup job already exists, skipping creation", "job", jobName)
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing cleanup job: %w", err)
	}

	// Create the cleanup job (don't set owner reference as the owner is being deleted)
	if err := r.Create(ctx, job); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create cleanup job: %w", err)
	}

	return nil
}

// updateModelRepoStatus updates the ModelRepository status with retry logic to handle conflicts
func (r *ModelRepositoryReconciler) updateModelRepoStatus(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, phase aitrigramv1.ModelRepositoryPhase, reason, message string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get fresh copy of the resource
		key := client.ObjectKeyFromObject(modelRepo)
		fresh := &aitrigramv1.ModelRepository{}
		if err := r.Get(ctx, key, fresh); err != nil {
			return err
		}

		// Update status on the fresh copy
		fresh.Status.Phase = phase
		fresh.Status.Reason = reason
		fresh.Status.Message = message
		fresh.Status.LastUpdated = &metav1.Time{Time: time.Now()}

		return r.Status().Update(ctx, fresh)
	})
}

// updateBoundNodeName updates the BoundNodeName field in the ModelRepository status
func (r *ModelRepositoryReconciler) updateBoundNodeName(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, nodeName string) error {
	logger := log.FromContext(ctx)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get fresh copy of the resource
		key := client.ObjectKeyFromObject(modelRepo)
		fresh := &aitrigramv1.ModelRepository{}
		if err := r.Get(ctx, key, fresh); err != nil {
			return err
		}

		// Update bound node name on the fresh copy
		fresh.Status.BoundNodeName = nodeName
		logger.Info("Setting bound node name",
			"modelRepo", modelRepo.Name,
			"nodeName", nodeName)

		return r.Status().Update(ctx, fresh)
	})
}

// getDownloadJobNodeName retrieves the node name where the download job pod is running
// Returns empty string if the pod is not yet scheduled or not found
func (r *ModelRepositoryReconciler) getDownloadJobNodeName(ctx context.Context, modelRepo *aitrigramv1.ModelRepository) (string, error) {
	logger := log.FromContext(ctx)

	jobName := fmt.Sprintf("download-%s", modelRepo.Name)
	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = "default"
	}

	// Get the Job
	job := &batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: namespace}, job); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Download job not found", "job", jobName)
			return "", nil
		}
		return "", fmt.Errorf("failed to get download job: %w", err)
	}

	// List pods owned by this job
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{
		"job-name": jobName,
	}); err != nil {
		return "", fmt.Errorf("failed to list pods for job %s: %w", jobName, err)
	}

	// Find a pod that has been scheduled (has a node assignment)
	for _, pod := range podList.Items {
		if pod.Spec.NodeName != "" {
			logger.Info("Found download job pod node",
				"job", jobName,
				"pod", pod.Name,
				"node", pod.Spec.NodeName,
				"phase", pod.Status.Phase)
			return pod.Spec.NodeName, nil
		}
	}

	logger.Info("Download job pod not yet scheduled to a node", "job", jobName)
	return "", nil
}

func (r *ModelRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aitrigramv1.ModelRepository{}).
		Owns(&batchv1.Job{}).
		Named("modelrepository").
		Complete(r)
}
