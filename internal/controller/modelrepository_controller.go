/*
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
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
		return ctrl.Result{Requeue: true}, nil
	}

	// Create storage provider
	provider, err := r.StorageFactory.CreateProvider(ctx, modelRepo)
	if err != nil {
		if modelRepo.GetDeletionTimestamp() != nil {
			return ctrl.Result{}, r.handleDeletion(ctx, modelRepo, nil)
		}
		r.setCondition(ctx, modelRepo, aitrigramv1.ModelRepoConditionReady, metav1.ConditionFalse, "StorageProviderFailed",
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

	// Step 1: Provision and prepare storage
	provider, result, err := r.reconcileStorage(ctx, modelRepo, provider)
	if err != nil || result != nil {
		if result != nil {
			return *result, err
		}
		return ctrl.Result{}, err
	}

	// Step 2: Reconcile revision downloads
	if !modelRepo.Spec.AutoDownload {
		logger.Info("AutoDownload is disabled, skipping", "modelRepo", modelRepo.Name)
		return ctrl.Result{}, nil
	}

	return r.reconcileRevisions(ctx, modelRepo, provider)
}

// reconcileStorage provisions the PVC and updates storage status.
// Returns the (possibly refreshed) provider, a result if requeue is needed, and any error.
func (r *ModelRepositoryReconciler) reconcileStorage(
	ctx context.Context,
	modelRepo *aitrigramv1.ModelRepository,
	provider *storage.StorageClassProvider,
) (*storage.StorageClassProvider, *ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if err := provider.ValidateConfig(); err != nil {
		r.setCondition(ctx, modelRepo, aitrigramv1.ModelRepoConditionStorageReady, metav1.ConditionFalse, "ValidationFailed",
			fmt.Sprintf("Storage validation failed: %v", err))
		return nil, &ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	if err := provider.CreateStorage(ctx, namespace); err != nil {
		r.setCondition(ctx, modelRepo, aitrigramv1.ModelRepoConditionStorageReady, metav1.ConditionFalse, "ProvisionFailed",
			fmt.Sprintf("Failed to provision storage: %v", err))
		return nil, &ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Update storage status from the bound PV.
	// RWO must wait for PVC to bind (needs node affinity); RWX can proceed immediately.
	if err := r.updateStorageStatus(ctx, modelRepo, provider); err != nil {
		if !provider.IsShared() {
			logger.Info("RWO storage: waiting for PVC to bind", "error", err.Error())
			return nil, &ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		logger.V(1).Info("RWX storage: proceeding without full storage status", "error", err.Error())
	} else {
		// Refresh modelRepo and provider with updated status
		if err := r.Get(ctx, client.ObjectKeyFromObject(modelRepo), modelRepo); err != nil {
			return nil, nil, err
		}
		var err error
		provider, err = r.StorageFactory.CreateProvider(ctx, modelRepo)
		if err != nil {
			return nil, &ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		}
	}

	logger.V(1).Info("Storage is ready",
		"shared", provider.IsShared(),
		"boundNode", storage.GetBoundNodeName(modelRepo))

	return provider, nil, nil
}

// reconcileRevisions ensures all desired revisions are downloaded.
func (r *ModelRepositoryReconciler) reconcileRevisions(
	ctx context.Context,
	modelRepo *aitrigramv1.ModelRepository,
	provider *storage.StorageClassProvider,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	desiredRevisions := r.getDesiredRevisions(modelRepo)
	if len(desiredRevisions) == 0 {
		logger.Info("No revisions to download", "modelRepo", modelRepo.Name)
		return ctrl.Result{}, nil
	}

	anyJobRunning := false

	for _, revRef := range desiredRevisions {
		running, result, err := r.reconcileRevision(ctx, modelRepo, provider, revRef, namespace)
		if err != nil || result != nil {
			if result != nil {
				return *result, err
			}
			return ctrl.Result{}, err
		}
		if running {
			anyJobRunning = true
		}
	}

	// Update overall phase
	r.updateOverallPhase(ctx, modelRepo, desiredRevisions, anyJobRunning)

	if anyJobRunning {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// reconcileRevision handles a single revision's download job.
// Returns (jobRunning, result if requeue needed, error).
func (r *ModelRepositoryReconciler) reconcileRevision(
	ctx context.Context,
	modelRepo *aitrigramv1.ModelRepository,
	provider *storage.StorageClassProvider,
	revRef aitrigramv1.RevisionReference,
	namespace string,
) (bool, *ctrl.Result, error) {
	revStatus := r.findRevisionStatus(modelRepo, revRef.Name)

	// Already ready
	if revStatus != nil && revStatus.Status == aitrigramv1.DownloadPhaseReady {
		return false, nil, nil
	}

	jobName := generateJobName("download", modelRepo.Name, revRef.Name)
	job := &batchv1.Job{}
	err := r.Get(ctx, client.ObjectKey{Name: jobName, Namespace: namespace}, job)

	if errors.IsNotFound(err) {
		return r.createDownloadJob(ctx, modelRepo, provider, revRef, revStatus, jobName)
	}
	if err != nil {
		return false, nil, err
	}

	// Job exists — check status
	return r.handleJobStatus(ctx, modelRepo, revRef, revStatus, job, jobName)
}

// createDownloadJob creates a new download job for a revision.
func (r *ModelRepositoryReconciler) createDownloadJob(
	ctx context.Context,
	modelRepo *aitrigramv1.ModelRepository,
	provider *storage.StorageClassProvider,
	revRef aitrigramv1.RevisionReference,
	revStatus *aitrigramv1.RevisionStatus,
	jobName string,
) (bool, *ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Creating download job for revision", "revision", revRef.Name, "job", jobName)

	// Delete any stale cleanup job first
	deleted, err := r.deleteStaleCleanupJob(ctx, modelRepo, revRef.Name)
	if err != nil {
		return false, nil, err
	}
	if deleted {
		logger.Info("Deleted stale cleanup job, waiting for termination")
		return false, &ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Initialize revision status
	if revStatus == nil {
		revStatus = &aitrigramv1.RevisionStatus{
			Name:       revRef.Name,
			CommitHash: revRef.Ref,
			Status:     aitrigramv1.DownloadPhasePending,
		}
		if err := r.updateRevisionStatus(ctx, modelRepo, revStatus); err != nil {
			return false, nil, err
		}
	}

	// Create the job
	job, err := r.createRevisionDownloadJob(ctx, modelRepo, provider, revRef)
	if err != nil {
		revStatus.Status = aitrigramv1.DownloadPhaseFailed
		_ = r.updateRevisionStatus(ctx, modelRepo, revStatus)
		return false, nil, err
	}

	if err := ctrl.SetControllerReference(modelRepo, job, r.Scheme); err != nil {
		return false, nil, err
	}

	if err := r.Create(ctx, job); err != nil && !errors.IsAlreadyExists(err) {
		revStatus.Status = aitrigramv1.DownloadPhaseFailed
		_ = r.updateRevisionStatus(ctx, modelRepo, revStatus)
		return false, nil, err
	}

	revStatus.Status = aitrigramv1.DownloadPhaseDownloading
	_ = r.updateRevisionStatus(ctx, modelRepo, revStatus)
	return true, nil, nil
}

// handleJobStatus processes the status of an existing download job.
func (r *ModelRepositoryReconciler) handleJobStatus(
	ctx context.Context,
	modelRepo *aitrigramv1.ModelRepository,
	revRef aitrigramv1.RevisionReference,
	revStatus *aitrigramv1.RevisionStatus,
	job *batchv1.Job,
	jobName string,
) (bool, *ctrl.Result, error) {
	logger := log.FromContext(ctx)

	ensureRevStatus := func() *aitrigramv1.RevisionStatus {
		if revStatus != nil {
			return revStatus
		}
		return &aitrigramv1.RevisionStatus{Name: revRef.Name, CommitHash: revRef.Ref}
	}

	if job.Status.Succeeded > 0 {
		logger.Info("Revision download job succeeded", "revision", revRef.Name)
		rs := ensureRevStatus()
		rs.Status = aitrigramv1.DownloadPhaseReady
		if err := r.updateRevisionStatus(ctx, modelRepo, rs); err != nil {
			return false, nil, err
		}
		return false, nil, nil
	}

	if job.Status.Failed > 0 {
		logger.Error(fmt.Errorf("download job failed"), "Job has failed", "revision", revRef.Name, "job", jobName)
		rs := ensureRevStatus()
		rs.Status = aitrigramv1.DownloadPhaseFailed
		_ = r.updateRevisionStatus(ctx, modelRepo, rs)
		r.setCondition(ctx, modelRepo, aitrigramv1.ModelRepoConditionReady, metav1.ConditionFalse, "DownloadFailed",
			fmt.Sprintf("Download job failed for revision %s", revRef.Name))
		return false, &ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Job still running
	logger.V(1).Info("Revision download job still running", "revision", revRef.Name)
	if revStatus == nil || revStatus.Status != aitrigramv1.DownloadPhaseDownloading {
		rs := ensureRevStatus()
		rs.Status = aitrigramv1.DownloadPhaseDownloading
		_ = r.updateRevisionStatus(ctx, modelRepo, rs)
	}
	return true, nil, nil
}

// updateOverallPhase updates the ModelRepository conditions based on revision statuses.
func (r *ModelRepositoryReconciler) updateOverallPhase(
	ctx context.Context,
	modelRepo *aitrigramv1.ModelRepository,
	desiredRevisions []aitrigramv1.RevisionReference,
	anyJobRunning bool,
) {
	allReady := true
	for _, revRef := range desiredRevisions {
		rs := r.findRevisionStatus(modelRepo, revRef.Name)
		if rs == nil || rs.Status != aitrigramv1.DownloadPhaseReady {
			allReady = false
			break
		}
	}

	if allReady {
		r.setCondition(ctx, modelRepo, aitrigramv1.ModelRepoConditionReady, metav1.ConditionTrue, "AllRevisionsReady", "All revisions downloaded successfully")
	} else if anyJobRunning {
		r.setCondition(ctx, modelRepo, aitrigramv1.ModelRepoConditionReady, metav1.ConditionFalse, "Downloading", "Downloading revisions")
	}
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
		r.setCondition(ctx, modelRepo, aitrigramv1.ModelRepoConditionReady, metav1.ConditionFalse, "DeletionBlocked", msg)

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

// syncStatus fetches a fresh copy, applies the mutation, and writes only if status changed.
func (r *ModelRepositoryReconciler) syncStatus(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, mutate func(*aitrigramv1.ModelRepositoryStatus)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := &aitrigramv1.ModelRepository{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(modelRepo), fresh); err != nil {
			return err
		}
		existing := fresh.DeepCopy()
		mutate(&fresh.Status)
		if equality.Semantic.DeepEqual(existing.Status, fresh.Status) {
			return nil
		}
		return r.Status().Update(ctx, fresh)
	})
}

// setCondition sets a single condition and derives Phase.
func (r *ModelRepositoryReconciler) setCondition(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, condType string, status metav1.ConditionStatus, reason, message string) {
	if err := r.syncStatus(ctx, modelRepo, func(s *aitrigramv1.ModelRepositoryStatus) {
		meta.SetStatusCondition(&s.Conditions, metav1.Condition{
			Type:    condType,
			Status:  status,
			Reason:  reason,
			Message: message,
		})
		s.Phase = deriveModelRepoPhase(s.Conditions)
	}); err != nil {
		log.FromContext(context.TODO()).Error(err, "Failed to set condition", "type", condType)
	}
}

// deriveModelRepoPhase derives the Phase from the Ready condition.
func deriveModelRepoPhase(conditions []metav1.Condition) aitrigramv1.DownloadPhase {
	ready := meta.FindStatusCondition(conditions, aitrigramv1.ModelRepoConditionReady)
	if ready == nil {
		return aitrigramv1.DownloadPhasePending
	}
	if ready.Status == metav1.ConditionTrue {
		return aitrigramv1.DownloadPhaseReady
	}
	switch ready.Reason {
	case "Downloading":
		return aitrigramv1.DownloadPhaseDownloading
	default:
		return aitrigramv1.DownloadPhaseFailed
	}
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
	return r.syncStatus(ctx, modelRepo, func(s *aitrigramv1.ModelRepositoryStatus) {
		found := false
		for i := range s.AvailableRevisions {
			if s.AvailableRevisions[i].Name == revStatus.Name {
				s.AvailableRevisions[i] = *revStatus
				found = true
				break
			}
		}
		if !found {
			s.AvailableRevisions = append(s.AvailableRevisions, *revStatus)
		}
	})
}

// updateStorageStatus updates the storage status in ModelRepository and sets StorageReady condition
func (r *ModelRepositoryReconciler) updateStorageStatus(ctx context.Context, modelRepo *aitrigramv1.ModelRepository, provider *storage.StorageClassProvider) error {
	namespace := r.OperatorNamespace
	if namespace == "" {
		namespace = defaultNamespace
	}

	storageStatus, err := provider.GetStorageStatus(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to get storage status: %w", err)
	}

	return r.syncStatus(ctx, modelRepo, func(s *aitrigramv1.ModelRepositoryStatus) {
		s.Storage = storageStatus
		meta.SetStatusCondition(&s.Conditions, metav1.Condition{
			Type:    aitrigramv1.ModelRepoConditionStorageReady,
			Status:  metav1.ConditionTrue,
			Reason:  "StorageProvisioned",
			Message: fmt.Sprintf("Storage provisioned with class %s", storageStatus.StorageClass),
		})
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
