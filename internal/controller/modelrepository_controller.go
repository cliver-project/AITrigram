/*
Copyright 2025 Lin Gao.

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

	aitrigramv1 "github.com/gaol/AITrigram/api/v1"
)

const (
	ModelRepositoryFinalizer = "modelrepository.aitrigram.ihomeland.cn/finalizer"
)

// ModelRepositoryReconciler reconciles a ModelRepository object
type ModelRepositoryReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	OperatorNamespace string
}

// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=modelrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=modelrepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=modelrepositories/finalizers,verbs=update
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
	if modelRepo.Spec.AutoDownload && (modelRepo.Status.Phase == "" || modelRepo.Status.Phase == aitrigramv1.ModelRepositoryPhasePending) {
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
			return ctrl.Result{RequeueAfter: time.Second * 10}, nil
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

	// Determine image - use custom image if specified, otherwise use default based on source origin
	image := modelRepo.Spec.DownloadImage
	if image == "" {
		// Default images based on source origin
		switch modelRepo.Spec.Source.Origin {
		case aitrigramv1.ModelOriginHuggingFace:
			// we choose vllm image as it has transformers installed
			// and we don't need to pull another hugging face image
			image = DefaultVLLMImage
		case aitrigramv1.ModelOriginOllama:
			image = DefaultOllamaImage
		case aitrigramv1.ModelOriginGGUF:
			image = "python:3.11-slim"
		default:
			image = "busybox:latest"
		}
	}

	// Get download scripts - either custom or default
	downloadScriptTemplate := modelRepo.Spec.DownloadScripts
	if downloadScriptTemplate == "" {
		// Default scripts based on origin
		downloadScriptTemplate = buildDefaultDownloadScript(modelRepo.Spec.Source.Origin)
	}

	// Render the template using pongo2 with model-specific context
	renderer := NewTemplateRenderer()
	downloadScript, err := renderer.RenderModelScript(
		downloadScriptTemplate,
		modelRepo.Spec.Source.ModelId,
		modelRepo.Spec.ModelName,
		modelRepo.Spec.Storage.Path,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to render download script template: %w", err)
	}

	// Detect script type (bash or python) and set appropriate command
	scriptType := DetectScriptType(downloadScript)
	var args []string

	switch scriptType {
	case ScriptTypePython:
		args = []string{
			"python3",
			"-c",
			downloadScript,
		}
	case ScriptTypeBash:
		args = []string{
			"/bin/bash",
			"-c",
			downloadScript,
		}
	default:
		// Fallback to bash
		args = []string{
			"/bin/bash",
			"-c",
			downloadScript,
		}
	}

	// Create volume and volume mount using the unified VolumeSource
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "model-storage",
			MountPath: modelRepo.Spec.Storage.Path,
		},
	}

	volumes := []corev1.Volume{
		{
			Name:         "model-storage",
			VolumeSource: modelRepo.Spec.Storage.VolumeSource,
		},
	}

	// Handle secret for HF token if needed
	var envVars []corev1.EnvVar
	if modelRepo.Spec.Source.HFTokenSecretRef != nil && modelRepo.Spec.Source.Origin == aitrigramv1.ModelOriginHuggingFace {
		envVars = append(envVars, corev1.EnvVar{
			Name: "HF_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: modelRepo.Spec.Source.HFTokenSecretRef,
			},
		})
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
					Containers: []corev1.Container{
						{
							Name:            "model-downloader",
							Image:           image,
							Args:            args,
							Env:             envVars,
							VolumeMounts:    volumeMounts,
							ImagePullPolicy: corev1.PullIfNotPresent,
						},
					},
					Volumes:       volumes,
					RestartPolicy: corev1.RestartPolicyOnFailure,
				},
			},
		},
	}

	return job, nil
}

func (r *ModelRepositoryReconciler) handleDeletion(ctx context.Context, modelRepo *aitrigramv1.ModelRepository) error {
	logger := log.FromContext(ctx)

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

	// Determine image based on script type
	// Default to a simple shell image, but may need to change based on script
	deleteScriptTemplate := modelRepo.Spec.DeleteScripts
	if deleteScriptTemplate == "" {
		// Get default delete script based on origin
		deleteScriptTemplate = buildDefaultDeleteScript(modelRepo.Spec.Source.Origin)
	}

	// Render the template using pongo2 with model-specific context
	renderer := NewTemplateRenderer()
	deleteScript, err := renderer.RenderModelScript(
		deleteScriptTemplate,
		modelRepo.Spec.Source.ModelId,
		modelRepo.Spec.ModelName,
		modelRepo.Spec.Storage.Path,
	)
	if err != nil {
		return fmt.Errorf("failed to render delete script template: %w", err)
	}

	// Detect script type and determine appropriate image and command
	scriptType := DetectScriptType(deleteScript)
	var image string
	var args []string

	switch scriptType {
	case ScriptTypePython:
		image = "python:3.11-slim"
		args = []string{
			"python3",
			"-c",
			deleteScript,
		}
	case ScriptTypeBash:
		// Use busybox for bash scripts, or ollama image for ollama-specific operations
		if modelRepo.Spec.Source.Origin == aitrigramv1.ModelOriginOllama {
			image = "ollama/ollama:latest"
		} else {
			image = "busybox:latest"
		}
		args = []string{
			"/bin/bash",
			"-c",
			deleteScript,
		}
	default:
		// Fallback to busybox with bash
		image = "busybox:latest"
		args = []string{
			"/bin/bash",
			"-c",
			deleteScript,
		}
	}

	// Create volume and volume mount using the unified VolumeSource
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "model-storage",
			MountPath: modelRepo.Spec.Storage.Path,
		},
	}

	volumes := []corev1.Volume{
		{
			Name:         "model-storage",
			VolumeSource: modelRepo.Spec.Storage.VolumeSource,
		},
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
			TTLSecondsAfterFinished: func(i int32) *int32 { return &i }(1800), // 30 minutes
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "cleanup",
							Image:           image,
							Args:            args,
							VolumeMounts:    volumeMounts,
							ImagePullPolicy: corev1.PullIfNotPresent,
						},
					},
					Volumes:       volumes,
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
