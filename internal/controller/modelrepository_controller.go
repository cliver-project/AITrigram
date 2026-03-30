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
	"hash/fnv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/assets"
	"github.com/cliver-project/AITrigram/internal/controller/component"
	"github.com/cliver-project/AITrigram/internal/controller/storage"
)

const (
	ModelRepositoryFinalizer = storage.ModelRepositoryFinalizer
	defaultNamespace         = "default"
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
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch

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
	provider, err := r.StorageFactory.CreateProvider(ctx, modelRepo)
	if err != nil {
		// During deletion, we still want to proceed even if provider creation fails
		if modelRepo.GetDeletionTimestamp() != nil {
			logger.Error(err, "Failed to create storage provider during deletion, will proceed with deletion")
			return ctrl.Result{}, r.handleDeletion(ctx, modelRepo, nil)
		}
		// During normal operation, fail and requeue
		logger.Error(err, "Failed to create storage provider")
		_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.DownloadPhaseFailed,
			fmt.Sprintf("Failed to create storage provider: %v", err))
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
		_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.DownloadPhaseFailed,
			fmt.Sprintf("Storage validation failed: %v", err))
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Step 1: Provision storage (create PVC if needed)
	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	if err := provider.CreateStorage(ctx, namespace); err != nil {
		logger.Error(err, "Failed to provision storage")
		_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.DownloadPhaseFailed,
			fmt.Sprintf("Failed to provision storage: %v", err))
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Step 2: Update storage status from the bound PV
	// For RWO storage, we MUST wait for the PVC to bind so we know the boundNodeName
	// before creating download jobs (they need node affinity to run on the correct node).
	// For RWX storage, we can proceed immediately — any node can access the storage.
	if err := r.updateStorageStatus(ctx, modelRepo, provider); err != nil {
		if !provider.IsShared() {
			// RWO: must wait for PVC to bind to discover node
			logger.Info("RWO storage: waiting for PVC to bind to determine node affinity", "error", err.Error())
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		// RWX: storage status update failed but we can proceed — no node affinity needed
		logger.V(1).Info("RWX storage: proceeding without full storage status", "error", err.Error())
	} else {
		// Refresh modelRepo and provider with updated status
		if err := r.Get(ctx, client.ObjectKeyFromObject(modelRepo), modelRepo); err != nil {
			return ctrl.Result{}, err
		}
		provider, err = r.StorageFactory.CreateProvider(ctx, modelRepo)
		if err != nil {
			return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		}
	}

	logger.V(1).Info("Storage is ready",
		"shared", provider.IsShared(),
		"boundNode", storage.GetBoundNodeName(modelRepo))

	// Handle revision-based downloads
	if !modelRepo.Spec.AutoDownload {
		logger.Info("AutoDownload is disabled, skipping", "modelRepo", modelRepo.Name)
		return ctrl.Result{}, nil
	}

	desiredRevisions := r.getDesiredRevisions(modelRepo)
	if len(desiredRevisions) == 0 {
		logger.Info("No revisions to download", "modelRepo", modelRepo.Name)
		return ctrl.Result{}, nil
	}

	// Track if any job is still running
	anyJobRunning := false

	// For each desired revision, ensure it's downloaded
	for _, revRef := range desiredRevisions {
		revStatus := r.findRevisionStatus(modelRepo, revRef.Name)

		// Check if revision is already ready
		if revStatus != nil && revStatus.Status == aitrigramv1.DownloadPhaseReady {
			logger.V(1).Info("Revision already ready", "revision", revRef.Name)
			continue
		}

		// Check if download job exists for this revision
		jobName := generateJobName("download", modelRepo.Name, revRef.Name)
		job := &batchv1.Job{}
		err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: namespace}, job)

		if errors.IsNotFound(err) {
			// Job doesn't exist, create it
			logger.Info("Creating download job for revision", "revision", revRef.Name, "job", jobName)

			// IMPORTANT: Before creating download job, check and delete any stale cleanup job for this revision
			// This prevents conflicts between cleanup and download operations on the same revision
			deleted, err := r.deleteStaleCleanupJob(ctx, modelRepo, revRef.Name)
			if err != nil {
				logger.Error(err, "Failed to check/delete stale cleanup job for revision", "revision", revRef.Name)
				return ctrl.Result{}, err
			}
			if deleted {
				// Cleanup job was deleted, requeue to wait for it to fully terminate
				logger.Info("Deleted stale cleanup job, waiting for termination before creating download job",
					"modelRepo", modelRepo.Name,
					"revision", revRef.Name)
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}

			// No cleanup job exists, safe to proceed with download job creation
			// Initialize revision status as Pending
			if revStatus == nil {
				revStatus = &aitrigramv1.RevisionStatus{
					Name:       revRef.Name,
					CommitHash: revRef.Ref,
					Status:     aitrigramv1.DownloadPhasePending,
				}
				if err := r.updateRevisionStatus(ctx, modelRepo, revStatus); err != nil {
					logger.Error(err, "Failed to initialize revision status")
					return ctrl.Result{}, err
				}
			}

			// Create download job
			job, err := r.createRevisionDownloadJob(ctx, modelRepo, provider, revRef)
			if err != nil {
				logger.Error(err, "Failed to create revision download job", "revision", revRef.Name)
				// Update revision status to Failed
				revStatus.Status = aitrigramv1.DownloadPhaseFailed
				_ = r.updateRevisionStatus(ctx, modelRepo, revStatus)
				return ctrl.Result{}, err
			}

			// Set owner reference
			if err := ctrl.SetControllerReference(modelRepo, job, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}

			// Create the job
			if err := r.Create(ctx, job); err != nil && !errors.IsAlreadyExists(err) {
				logger.Error(err, "Failed to create revision download job", "revision", revRef.Name)
				revStatus.Status = aitrigramv1.DownloadPhaseFailed
				_ = r.updateRevisionStatus(ctx, modelRepo, revStatus)
				return ctrl.Result{}, err
			}

			// Update revision status to Downloading
			revStatus.Status = aitrigramv1.DownloadPhaseDownloading
			if err := r.updateRevisionStatus(ctx, modelRepo, revStatus); err != nil {
				logger.Error(err, "Failed to update revision status to Downloading")
				return ctrl.Result{}, err
			}

			anyJobRunning = true

		} else if err != nil {
			logger.Error(err, "Failed to get download job", "job", jobName)
			return ctrl.Result{}, err
		} else {
			// Job exists, check its status
			if job.Status.Succeeded > 0 {
				// Job completed successfully
				logger.Info("Revision download job succeeded", "revision", revRef.Name)

				// Update revision status to Ready
				if revStatus == nil {
					revStatus = &aitrigramv1.RevisionStatus{
						Name:       revRef.Name,
						CommitHash: revRef.Ref,
					}
				}
				revStatus.Status = aitrigramv1.DownloadPhaseReady

				if err := r.updateRevisionStatus(ctx, modelRepo, revStatus); err != nil {
					logger.Error(err, "Failed to update revision status to Ready")
					return ctrl.Result{}, err
				}

			} else if job.Status.Failed > 0 {
				// Job failed - this is an error condition
				logger.Error(fmt.Errorf("revision download job failed"), "Job has failed", "revision", revRef.Name, "job", jobName)

				if revStatus == nil {
					revStatus = &aitrigramv1.RevisionStatus{
						Name:       revRef.Name,
						CommitHash: revRef.Ref,
					}
				}
				revStatus.Status = aitrigramv1.DownloadPhaseFailed

				if err := r.updateRevisionStatus(ctx, modelRepo, revStatus); err != nil {
					logger.Error(err, "Failed to update revision status to Failed")
					return ctrl.Result{}, err
				}

				// Update overall ModelRepository status to reflect the failure
				failureMsg := fmt.Sprintf("Download job failed for revision %s", revRef.Name)
				_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.DownloadPhaseFailed, failureMsg)

				// Requeue to retry after delay
				return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil

			} else {
				// Job is still running
				logger.V(1).Info("Revision download job still running", "revision", revRef.Name)

				if revStatus == nil || revStatus.Status != aitrigramv1.DownloadPhaseDownloading {
					if revStatus == nil {
						revStatus = &aitrigramv1.RevisionStatus{
							Name:       revRef.Name,
							CommitHash: revRef.Ref,
						}
					}
					revStatus.Status = aitrigramv1.DownloadPhaseDownloading
					_ = r.updateRevisionStatus(ctx, modelRepo, revStatus)
				}

				anyJobRunning = true
			}
		}

	}

	// Update overall phase
	allReady := true
	for _, revRef := range desiredRevisions {
		revStatus := r.findRevisionStatus(modelRepo, revRef.Name)
		if revStatus == nil || revStatus.Status != aitrigramv1.DownloadPhaseReady {
			allReady = false
			break
		}
	}

	if allReady {
		if modelRepo.Status.Phase != aitrigramv1.DownloadPhaseReady {
			_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.DownloadPhaseReady, "All revisions downloaded successfully")
		}
	} else if anyJobRunning {
		if modelRepo.Status.Phase != aitrigramv1.DownloadPhaseDownloading {
			_ = r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.DownloadPhaseDownloading, "Downloading revisions")
		}
	}

	// If jobs are running, requeue
	if anyJobRunning {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *ModelRepositoryReconciler) handleDeletion(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, provider *storage.StorageClassProvider) error {
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
			if ref.Name == modelRepo.Name {
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
		if err := r.updateModelRepoStatus(ctx, modelRepo, aitrigramv1.DownloadPhaseFailed, msg); err != nil {
			logger.Error(err, "Failed to update status")
		}

		// Return error to requeue and keep checking
		return fmt.Errorf("deletion blocked: %s", msg)
	}

	// No dependencies - proceed with cleanup
	logger.Info("No LLMEngine dependencies found, proceeding with deletion")

	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	// Delete all jobs owned by this ModelRepository (download and cleanup)
	jobList := &batchv1.JobList{}
	if err := r.List(ctx, jobList, client.InNamespace(namespace), client.MatchingLabels{
		"modelrepository.aitrigram.cliver-project.github.io/name": modelRepo.Name,
	}); err == nil {
		propagation := metav1.DeletePropagationBackground
		for i := range jobList.Items {
			logger.Info("Deleting job", "job", jobList.Items[i].Name)
			_ = r.Delete(ctx, &jobList.Items[i], &client.DeleteOptions{
				PropagationPolicy: &propagation,
			})
		}
	}

	// Cleanup storage (PVC)
	if provider != nil {
		if err := provider.Cleanup(ctx, namespace); err != nil {
			logger.Error(err, "Failed to cleanup storage, will still remove finalizer")
		}
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

func (r *ModelRepositoryReconciler) createCleanupJob(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, provider *storage.StorageClassProvider, revision string) error {
	jobName := generateJobName("cleanup", modelRepo.Name, revision)

	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	// Load cleanup job template from assets
	origin := string(modelRepo.Spec.Source.Origin)
	job, err := assets.LoadModelRepositoryJobManifest(origin, "cleanup_job")
	if err != nil {
		return fmt.Errorf("failed to load cleanup job manifest: %w", err)
	}
	job.SetNamespace(namespace)

	// Create context for adapter
	revisionRef := aitrigramv1.RevisionReference{
		Name: revision,
		Ref:  revision,
	}
	jobCtx := component.ModelRepositoryJobContext{
		Context:      ctx,
		Client:       r.Client,
		Scheme:       r.Scheme,
		ModelRepo:    modelRepo,
		Provider:     provider,
		RevisionRef:  &revisionRef,
		JobName:      jobName,
		Namespace:    namespace,
		CustomImage:  modelRepo.Spec.DownloadImage, // Reuse download image for cleanup
		CustomScript: modelRepo.Spec.DeleteScripts,
	}

	// Apply adapter to customize the job
	if err := component.AdaptCleanupJob(jobCtx, job); err != nil {
		return fmt.Errorf("failed to adapt cleanup job: %w", err)
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
func (r *ModelRepositoryReconciler) updateModelRepoStatus(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, phase aitrigramv1.DownloadPhase, message string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get fresh copy of the resource
		key := client.ObjectKeyFromObject(modelRepo)
		fresh := &aitrigramv1.ModelRepository{}
		if err := r.Get(ctx, key, fresh); err != nil {
			return err
		}

		// Update status on the fresh copy
		fresh.Status.Phase = phase
		fresh.Status.Message = message
		fresh.Status.LastUpdated = &metav1.Time{Time: time.Now()}

		return r.Status().Update(ctx, fresh)
	})
}

// deleteStaleCleanupJob checks for and deletes any existing cleanup job for the ModelRepository revision
// This prevents conflicts when starting a new download job for the same revision
// Returns (wasDeleted, error) where wasDeleted=true indicates a cleanup job was found and deleted
func (r *ModelRepositoryReconciler) deleteStaleCleanupJob(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, revision string) (bool, error) {
	logger := log.FromContext(ctx)

	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	// Generate cleanup job name for this specific revision
	cleanupJobName := generateJobName("cleanup", modelRepo.Name, revision)

	// Check if cleanup job exists
	cleanupJob := &batchv1.Job{}
	err := r.Get(ctx, client.ObjectKey{Name: cleanupJobName, Namespace: namespace}, cleanupJob)
	if err != nil {
		if errors.IsNotFound(err) {
			// No cleanup job exists - this is the normal case
			return false, nil
		}
		return false, fmt.Errorf("failed to check for cleanup job: %w", err)
	}

	// Cleanup job exists - delete it to prevent storage conflicts
	logger.Info("Found stale cleanup job, deleting before starting download",
		"cleanupJob", cleanupJobName,
		"modelRepo", modelRepo.Name)

	if err := r.Delete(ctx, cleanupJob, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
		if errors.IsNotFound(err) {
			// Already deleted, that's fine - but we did find it initially
			return true, nil
		}
		return false, fmt.Errorf("failed to delete stale cleanup job %s: %w", cleanupJobName, err)
	}

	logger.Info("Successfully initiated deletion of stale cleanup job, will wait for termination",
		"cleanupJob", cleanupJobName,
		"modelRepo", modelRepo.Name)

	return true, nil
}

// canCleanupStorage checks if it's safe to cleanup storage for a ModelRepository being deleted
// Returns true if no other ModelRepositories are using the same ACTUAL storage backend
// This compares the real backend identifiers (filesystemID, volumeID, etc.) not just PVC names
func (r *ModelRepositoryReconciler) canCleanupStorage(ctx context.Context, modelRepo *aitrigramv1.ModelRepository) (bool, error) {
	logger := log.FromContext(ctx)

	// Check storage status to determine backend type
	if modelRepo.Status.Storage == nil || modelRepo.Status.Storage.BackendRef == nil {
		logger.Info("No storage status available, skipping cleanup")
		return false, nil
	}

	backendRef := modelRepo.Status.Storage.BackendRef

	// For hostpath, never cleanup (node-local, we don't manage the host directory)
	if backendRef.Type == "hostpath" {
		logger.Info("HostPath storage, skipping cleanup (managed by cluster admin)")
		return false, nil
	}

	// Extract the backend identifier to check for sharing
	// For shared filesystems: filesystemID (e.g., "nfs://server/path")
	// For block volumes: volumeID (e.g., "vol-xxxxx")
	// For pending/unknown: skip cleanup
	backendID := r.extractBackendIdentifier(backendRef)
	if backendID == "" {
		logger.Info("Cannot determine backend identifier, skipping cleanup",
			"backendType", backendRef.Type)
		return false, nil
	}

	// List all ModelRepositories to check if any share the same backend
	modelRepoList := &aitrigramv1.ModelRepositoryList{}
	if err := r.List(ctx, modelRepoList); err != nil {
		return false, fmt.Errorf("failed to list ModelRepositories: %w", err)
	}

	// Check if any other ModelRepository is using the same backend
	for _, otherRepo := range modelRepoList.Items {
		// Skip the ModelRepository being deleted
		if otherRepo.Name == modelRepo.Name {
			continue
		}

		// Skip if no storage status
		if otherRepo.Status.Storage == nil || otherRepo.Status.Storage.BackendRef == nil {
			continue
		}

		// Extract backend identifier from other repository
		otherBackendID := r.extractBackendIdentifier(otherRepo.Status.Storage.BackendRef)
		if otherBackendID == "" {
			continue
		}

		// Compare backend identifiers
		if backendID == otherBackendID {
			logger.Info("Found ModelRepository still using the same storage backend",
				"otherModelRepo", otherRepo.Name,
				"backendID", backendID,
				"backendType", backendRef.Type)
			return false, nil
		}
	}

	// No other ModelRepositories are using this backend - safe to cleanup
	logger.Info("No other ModelRepositories using this backend, cleanup allowed",
		"backendID", backendID,
		"backendType", backendRef.Type)
	return true, nil
}

// extractBackendIdentifier extracts a unique identifier for the storage backend
// This is used to detect if multiple ModelRepositories share the same underlying storage
func (r *ModelRepositoryReconciler) extractBackendIdentifier(backendRef *aitrigramv1.BackendReference) string {
	if backendRef == nil {
		return ""
	}

	details := backendRef.Details
	if details == nil {
		return ""
	}

	// For shared filesystems (NFS, CephFS, GlusterFS), use filesystemID
	// This identifies the actual filesystem mount point
	if filesystemID, ok := details["filesystemID"]; ok && filesystemID != "" {
		return filesystemID
	}

	// For block volumes (EBS, GCE PD, Azure Disk), use volumeID
	if volumeID, ok := details["volumeID"]; ok && volumeID != "" {
		return volumeID
	}

	// For GCE PD specifically
	if pdName, ok := details["pdName"]; ok && pdName != "" {
		return "gce-pd://" + pdName
	}

	// For Azure Disk specifically
	if diskURI, ok := details["diskURI"]; ok && diskURI != "" {
		return diskURI
	}

	// For CSI volumes, use volumeID as fallback
	if driver, ok := details["driver"]; ok && driver != "" {
		if volID, ok := details["volumeID"]; ok && volID != "" {
			return driver + "://" + volID
		}
	}

	// For hostpath (should not reach here, but be safe)
	if path, ok := details["path"]; ok && path != "" {
		if nodeName, ok := details["nodeName"]; ok && nodeName != "" {
			return "hostpath://" + nodeName + path
		}
		return "hostpath://" + path
	}

	// Cannot determine backend identifier
	return ""
}

// --- Revision Management Helper Functions ---

// getDesiredRevisions returns the list of revisions that should be downloaded
// Uses spec.source.revisions if specified, otherwise creates a single revision
// from spec.source.revision (or the origin default)
func (r *ModelRepositoryReconciler) getDesiredRevisions(modelRepo *aitrigramv1.ModelRepository) []aitrigramv1.RevisionReference {
	if len(modelRepo.Spec.Source.Revisions) > 0 {
		return modelRepo.Spec.Source.Revisions
	}

	// Use spec.source.revision or fall back to origin default
	rev := r.getCurrentRevision(modelRepo)
	return []aitrigramv1.RevisionReference{
		{
			Name: rev,
			Ref:  rev,
		},
	}
}

// getCurrentRevision returns the current revision from spec.source.revision
// or a default based on the model source origin
func (r *ModelRepositoryReconciler) getCurrentRevision(modelRepo *aitrigramv1.ModelRepository) string {
	if modelRepo.Spec.Source.Revision != "" {
		return modelRepo.Spec.Source.Revision
	}
	switch modelRepo.Spec.Source.Origin {
	case aitrigramv1.ModelOriginHuggingFace:
		return "main"
	case aitrigramv1.ModelOriginOllama:
		return "latest"
	default:
		return "main"
	}
}

// findRevisionStatus finds a revision in the status.availableRevisions list
func (r *ModelRepositoryReconciler) findRevisionStatus(modelRepo *aitrigramv1.ModelRepository, revisionName string) *aitrigramv1.RevisionStatus {
	for i := range modelRepo.Status.AvailableRevisions {
		if modelRepo.Status.AvailableRevisions[i].Name == revisionName {
			return &modelRepo.Status.AvailableRevisions[i]
		}
	}
	return nil
}

// updateRevisionStatus updates or adds a revision to the status.availableRevisions list
func (r *ModelRepositoryReconciler) updateRevisionStatus(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, revStatus *aitrigramv1.RevisionStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get fresh copy
		key := client.ObjectKeyFromObject(modelRepo)
		fresh := &aitrigramv1.ModelRepository{}
		if err := r.Get(ctx, key, fresh); err != nil {
			return err
		}

		// Find and update or append
		found := false
		for i := range fresh.Status.AvailableRevisions {
			if fresh.Status.AvailableRevisions[i].Name == revStatus.Name {
				fresh.Status.AvailableRevisions[i] = *revStatus
				found = true
				break
			}
		}
		if !found {
			fresh.Status.AvailableRevisions = append(fresh.Status.AvailableRevisions, *revStatus)
		}

		fresh.Status.LastUpdated = &metav1.Time{Time: time.Now()}
		return r.Status().Update(ctx, fresh)
	})
}

// updateStorageStatus updates the storage status in ModelRepository
func (r *ModelRepositoryReconciler) updateStorageStatus(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, provider *storage.StorageClassProvider) error {
	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	storageStatus, err := provider.GetStorageStatus(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to get storage status: %w", err)
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get fresh copy
		key := client.ObjectKeyFromObject(modelRepo)
		fresh := &aitrigramv1.ModelRepository{}
		if err := r.Get(ctx, key, fresh); err != nil {
			return err
		}

		fresh.Status.Storage = storageStatus
		fresh.Status.LastUpdated = &metav1.Time{Time: time.Now()}
		return r.Status().Update(ctx, fresh)
	})
}

// createRevisionDownloadJob creates a download job for a specific revision
func (r *ModelRepositoryReconciler) createRevisionDownloadJob(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, provider *storage.StorageClassProvider, revisionRef aitrigramv1.RevisionReference) (*batchv1.Job, error) {
	jobName := generateJobName("download", modelRepo.Name, revisionRef.Name)

	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	// Load download job template from assets
	origin := string(modelRepo.Spec.Source.Origin)
	job, err := assets.LoadModelRepositoryJobManifest(origin, "download_job")
	if err != nil {
		return nil, fmt.Errorf("failed to load download job manifest: %w", err)
	}
	job.SetNamespace(namespace)

	// Create context for adapter
	jobCtx := component.ModelRepositoryJobContext{
		Context:      ctx,
		Client:       r.Client,
		Scheme:       r.Scheme,
		ModelRepo:    modelRepo,
		Provider:     provider,
		RevisionRef:  &revisionRef,
		JobName:      jobName,
		Namespace:    namespace,
		CustomImage:  modelRepo.Spec.DownloadImage,
		CustomScript: modelRepo.Spec.DownloadScripts,
	}

	// Apply adapter to customize the job
	if err := component.AdaptDownloadJob(jobCtx, job); err != nil {
		return nil, fmt.Errorf("failed to adapt download job: %w", err)
	}

	return job, nil
}

// generateJobName creates a valid Kubernetes job name that respects the 63-character limit
// This function is DETERMINISTIC - it will always return the same name for the same inputs,
// which is critical for job lookup during reconciliation loops.
//
// jobType: "download" or "cleanup"
// modelRepoName: name of the ModelRepository
// revisionName: name of the revision (empty for cleanup jobs)
func generateJobName(jobType, modelRepoName, revisionName string) string {
	const maxLen = 63 // Kubernetes DNS-1123 subdomain name limit

	// For cleanup jobs (no revision)
	if revisionName == "" {
		prefix := fmt.Sprintf("%s-", jobType)
		available := maxLen - len(prefix)

		if len(modelRepoName) <= available {
			return fmt.Sprintf("%s%s", prefix, modelRepoName)
		}

		// Truncate and add hash for uniqueness
		truncated := modelRepoName[:available-7] // Reserve 7 chars for hash
		hash := hashString(modelRepoName)[:6]
		return fmt.Sprintf("%s%s-%s", prefix, truncated, hash)
	}

	// For download jobs (with revision)
	prefix := fmt.Sprintf("%s-", jobType)
	separator := "-"

	// Calculate available space: 63 - len("download-") - len("-") = 53 chars
	available := maxLen - len(prefix) - len(separator)

	// Check if we can fit both names without truncation
	combined := fmt.Sprintf("%s%s%s", modelRepoName, separator, revisionName)
	if len(combined) <= available {
		return fmt.Sprintf("%s%s", prefix, combined)
	}

	// Need to truncate - allocate space proportionally, but reserve space for hash
	// Reserve 7 chars for hash suffix: "-" + 6 char hash
	hashSuffixLen := 7
	availableForNames := available - hashSuffixLen

	// Allocate space: give slightly more to modelRepo (it's more important for identification)
	modelRepoMaxLen := (availableForNames * 3) / 5            // 60% to modelRepo
	revisionMaxLen := availableForNames - modelRepoMaxLen - 1 // 40% to revision (minus separator)

	// Ensure minimum lengths
	if modelRepoMaxLen < 8 {
		modelRepoMaxLen = 8
		revisionMaxLen = availableForNames - modelRepoMaxLen - 1
	}
	if revisionMaxLen < 6 {
		revisionMaxLen = 6
		modelRepoMaxLen = availableForNames - revisionMaxLen - 1
	}

	// Truncate names
	truncatedModel := modelRepoName
	if len(truncatedModel) > modelRepoMaxLen {
		truncatedModel = truncatedModel[:modelRepoMaxLen]
	}

	truncatedRevision := revisionName
	if len(truncatedRevision) > revisionMaxLen {
		truncatedRevision = truncatedRevision[:revisionMaxLen]
	}

	// Create hash from original full name for uniqueness
	fullName := fmt.Sprintf("%s-%s", modelRepoName, revisionName)
	hash := hashString(fullName)[:6]

	return fmt.Sprintf("%s%s-%s-%s", prefix, truncatedModel, truncatedRevision, hash)
}

// hashString creates a short hash of a string for uniqueness
func hashString(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum32())
}

func (r *ModelRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aitrigramv1.ModelRepository{}).
		Owns(&batchv1.Job{}).
		Named("modelrepository").
		Complete(r)
}
