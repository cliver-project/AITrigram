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
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/adapters"
	"github.com/cliver-project/AITrigram/internal/controller/component"
	"github.com/cliver-project/AITrigram/internal/controller/storage"
)

const (
	LLMEngineFinalizer = storage.LLMEngineFinalizer
)

// LLMEngineReconciler reconciles a LLMEngine object
type LLMEngineReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	OperatorNamespace string
}

// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=llmengines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=llmengines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=llmengines/finalizers,verbs=update
// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=modelrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=aitrigram.cliver-project.github.io,resources=modelrepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

func (r *LLMEngineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the LLMEngine instance
	llmEngine := &aitrigramv1.LLMEngine{}
	if err := r.Get(ctx, req.NamespacedName, llmEngine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if llmEngine.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, r.handleDeletion(ctx, llmEngine)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(llmEngine, LLMEngineFinalizer) {
		controllerutil.AddFinalizer(llmEngine, LLMEngineFinalizer)
		if err := r.Update(ctx, llmEngine); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Fetch referenced ModelRepositories
	modelRepos, err := r.fetchModelRepositories(ctx, llmEngine)
	if err != nil {
		return r.updateStatus(ctx, llmEngine, "Error", "ModelRepositoryFetchFailed", err.Error())
	}

	if len(modelRepos) == 0 {
		return r.updateStatus(ctx, llmEngine, "Pending", "NoModelsFound", "No ModelRepository resources found")
	}

	// Check if all models are downloaded
	for _, modelRepo := range modelRepos {
		if modelRepo.Status.Phase != aitrigramv1.DownloadPhaseReady {
			msg := fmt.Sprintf("Waiting for ModelRepository %s (phase: %s)", modelRepo.Name, modelRepo.Status.Phase)
			// TODO maybe trigger the downloading if the autodownload is false
			return r.updateStatus(ctx, llmEngine, "Pending", "WaitingForModels", msg)
		}

		// For RWO storage, log node binding info if available
		if storage.RequiresNodeAffinity(modelRepo) {
			boundNode := storage.GetBoundNodeName(modelRepo)
			if boundNode != "" {
				logger.Info("ModelRepository is bound to node",
					"modelRepo", modelRepo.Name,
					"boundNode", boundNode)
			} else {
				// PV may not have node affinity (e.g., minikube) — proceed without it
				logger.V(1).Info("RWO storage has no boundNodeName, proceeding without node affinity",
					"modelRepo", modelRepo.Name)
			}
		}
	}

	// Validate GPU availability if GPU is enabled
	if llmEngine.Spec.GPU != nil && llmEngine.Spec.GPU.Enabled {
		// If any ModelRepository requires node affinity, validate GPU on that specific node
		var requiredNodeName string
		for _, modelRepo := range modelRepos {
			if storage.RequiresNodeAffinity(modelRepo) {
				boundNode := storage.GetBoundNodeName(modelRepo)
				if boundNode != "" {
					requiredNodeName = boundNode
					break
				}
			}
		}

		if err := r.validateGPUAvailability(ctx, llmEngine, requiredNodeName); err != nil {
			return r.updateStatus(ctx, llmEngine, "Error", "GPUValidationFailed", err.Error())
		}
	}

	// Prepare read-only storage for each ModelRepository
	// Creates a PV+PVC in the LLMEngine's namespace mirroring the ModelRepository's backend
	storageResults := map[string]*storage.LLMEngineStorageResult{}
	for _, modelRepo := range modelRepos {
		result, err := storage.EnsureLLMEngineStorage(ctx, r.Client, llmEngine, modelRepo)
		if err != nil {
			return r.updateStatus(ctx, llmEngine, "Pending", "StoragePending",
				fmt.Sprintf("Preparing storage for model %s: %v", modelRepo.Name, err))
		}
		storageResults[modelRepo.Name] = result
	}

	// Create one Deployment and Service per ModelRef
	totalReady := 0
	totalDeployments := len(modelRepos)

	for _, modelRepo := range modelRepos {

		// Create component for this model
		// ConfigMap (if needed) will be created by the component's manifest adapters
		comp := adapters.NewModelComponent(llmEngine, modelRepo)

		// Create component context
		compCtx := component.LLMEngineContext{
			Context:      ctx,
			Client:       r.Client,
			Scheme:       r.Scheme,
			LLMEngine:    llmEngine,
			ModelRepo:    modelRepo,
			Namespace:    llmEngine.Namespace,
			ModelPVCName: storageResults[modelRepo.Name].PVCName,
		}

		// Reconcile component (creates/updates Deployment and Service)
		if err := comp.Reconcile(compCtx); err != nil {
			return r.updateStatus(ctx, llmEngine, "Error", "ReconcileFailed",
				fmt.Sprintf("Failed to reconcile model %s: %v", modelRepo.Name, err))
		}

		// Check deployment readiness
		deploymentName := fmt.Sprintf("%s-%s", llmEngine.Name, modelRepo.Name)
		deployment := &appsv1.Deployment{}
		if err := r.Get(ctx, types.NamespacedName{
			Name:      deploymentName,
			Namespace: llmEngine.Namespace,
		}, deployment); err == nil {
			if deployment.Status.ReadyReplicas > 0 {
				totalReady++
			}
		}
	}

	// Update status based on deployment readiness
	modelNames := make([]string, len(modelRepos))
	for i, mr := range modelRepos {
		modelNames[i] = mr.Name
	}
	llmEngine.Status.ModelRepositories = modelNames

	// Update referenced engines on each ModelRepository
	r.updateReferencedEngines(ctx, modelNames)

	if totalReady == totalDeployments {
		return r.updateStatus(ctx, llmEngine, "Running", "DeploymentsReady",
			fmt.Sprintf("All %d model deployments ready", totalDeployments))
	}

	return r.updateStatus(ctx, llmEngine, "Starting", "DeploymentsCreated",
		fmt.Sprintf("%d/%d deployments ready", totalReady, totalDeployments))
}

func (r *LLMEngineReconciler) fetchModelRepositories(ctx context.Context, llmEngine *aitrigramv1.LLMEngine) ([]*aitrigramv1.ModelRepository, error) {
	modelRepos := make([]*aitrigramv1.ModelRepository, 0, len(llmEngine.Spec.ModelRefs))

	for _, modelRef := range llmEngine.Spec.ModelRefs {
		modelRepo := &aitrigramv1.ModelRepository{}
		// ModelRepository is cluster-scoped, so no namespace
		if err := r.Get(ctx, types.NamespacedName{Name: modelRef.Name}, modelRepo); err != nil {
			if errors.IsNotFound(err) {
				return nil, fmt.Errorf("ModelRepository %s not found", modelRef.Name)
			}
			return nil, err
		}
		modelRepos = append(modelRepos, modelRepo)
	}

	return modelRepos, nil
}

// validateGPUAvailability validates that GPU nodes are available in the cluster
// If requiredNodeName is specified, validates GPU availability on that specific node only
// Returns an error if GPU is required but no suitable nodes are found
func (r *LLMEngineReconciler) validateGPUAvailability(ctx context.Context, llmEngine *aitrigramv1.LLMEngine, requiredNodeName string) error {
	logger := log.FromContext(ctx)

	// Get GPU configuration
	gpuConfig := llmEngine.Spec.GPU
	if gpuConfig == nil || !gpuConfig.Enabled {
		return nil
	}

	// Set default GPU type if not specified
	gpuType := gpuConfig.Type
	if gpuType == "" {
		gpuType = "nvidia.com/gpu"
	}

	requestedGPUs := gpuConfig.Count
	if requestedGPUs == 0 {
		requestedGPUs = 1
	}

	// If a specific node is required (due to storage constraints), validate only that node
	if requiredNodeName != "" {
		node := &corev1.Node{}
		if err := r.Get(ctx, client.ObjectKey{Name: requiredNodeName}, node); err != nil {
			return fmt.Errorf("failed to get required node %s: %w", requiredNodeName, err)
		}

		// Check if node has GPU resources
		gpuCapacity := node.Status.Allocatable[corev1.ResourceName(gpuType)]
		if gpuCapacity.IsZero() {
			return fmt.Errorf("required node %s (due to storage constraints) does not have GPU resources of type %s. "+
				"Storage requires node affinity, but this node lacks GPUs. "+
				"Either use ReadWriteMany storage or ensure the storage node has GPUs",
				requiredNodeName, gpuType)
		}

		// Check if node has enough GPUs
		availableGPUs := gpuCapacity.Value()
		if availableGPUs < int64(requestedGPUs) {
			return fmt.Errorf("required node %s has only %d GPUs of type %s, but %d requested. "+
				"Storage requires node affinity to this node",
				requiredNodeName, availableGPUs, gpuType, requestedGPUs)
		}

		// Check if node matches custom node selector
		if len(gpuConfig.NodeSelector) > 0 {
			for key, value := range gpuConfig.NodeSelector {
				if node.Labels[key] != value {
					return fmt.Errorf("required node %s (due to storage) does not match GPU node selector %s=%s. "+
						"Storage node binding conflicts with GPU requirements",
						requiredNodeName, key, value)
				}
			}
		}

		logger.Info("GPU validation successful on required node",
			"node", requiredNodeName,
			"gpuType", gpuType,
			"availableGPUs", availableGPUs,
			"requestedGPUs", requestedGPUs,
			"reason", "storage node affinity")

		return nil
	}

	// No specific node required - validate cluster-wide GPU availability
	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	// Find nodes with GPU resources
	suitableNodes := make([]string, 0, len(nodeList.Items))
	gpuNodes := make([]string, 0, len(nodeList.Items))

	for _, node := range nodeList.Items {
		// Check if node has GPU resources
		gpuCapacity := node.Status.Allocatable[corev1.ResourceName(gpuType)]
		if gpuCapacity.IsZero() {
			continue
		}

		gpuNodes = append(gpuNodes, node.Name)

		// Check if node matches all custom node selector
		nodeMatches := true
		if len(gpuConfig.NodeSelector) > 0 {
			for key, value := range gpuConfig.NodeSelector {
				if node.Labels[key] != value {
					nodeMatches = false
					break
				}
			}
		}

		if !nodeMatches {
			logger.Info("Node has GPUs but doesn't match node selector",
				"node", node.Name,
				"gpuCapacity", gpuCapacity.String(),
				"nodeSelector", gpuConfig.NodeSelector)
			continue
		}

		// Check if node has enough available GPUs
		availableGPUs := gpuCapacity.Value()
		if availableGPUs >= int64(requestedGPUs) {
			suitableNodes = append(suitableNodes, node.Name)
			logger.Info("Found suitable GPU node",
				"node", node.Name,
				"gpuType", gpuType,
				"availableGPUs", availableGPUs,
				"requestedGPUs", requestedGPUs)
		} else {
			logger.Info("Node has GPUs but not enough capacity",
				"node", node.Name,
				"availableGPUs", availableGPUs,
				"requestedGPUs", requestedGPUs)
		}
	}

	// Log results for diagnostics
	if len(gpuNodes) == 0 {
		logger.Info("No GPU nodes found in cluster",
			"gpuType", gpuType,
			"totalNodes", len(nodeList.Items))
		return fmt.Errorf("no nodes with GPU resources (%s) found in cluster. Total nodes checked: %d",
			gpuType, len(nodeList.Items))
	}

	if len(suitableNodes) == 0 {
		logger.Info("GPU nodes found but none suitable for deployment",
			"gpuType", gpuType,
			"gpuNodes", gpuNodes,
			"requestedGPUs", requestedGPUs,
			"nodeSelector", gpuConfig.NodeSelector)
		return fmt.Errorf("found %d GPU nodes (%v) but none have sufficient resources (need %d GPUs of type %s) or match node selector %v",
			len(gpuNodes), gpuNodes, requestedGPUs, gpuType, gpuConfig.NodeSelector)
	}

	logger.Info("GPU validation successful",
		"suitableNodes", suitableNodes,
		"totalGPUNodes", len(gpuNodes),
		"gpuType", gpuType,
		"requestedGPUs", requestedGPUs)

	return nil
}

func (r *LLMEngineReconciler) updateStatus(ctx context.Context, llmEngine *aitrigramv1.LLMEngine, phase, reason, message string) (ctrl.Result, error) {
	// Use retry logic to handle conflicts when multiple reconcile loops update status
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get fresh copy of the resource
		key := client.ObjectKeyFromObject(llmEngine)
		fresh := &aitrigramv1.LLMEngine{}
		if err := r.Get(ctx, key, fresh); err != nil {
			return err
		}

		// Skip update if nothing changed to avoid unnecessary generation bumps
		if fresh.Status.Phase == phase && fresh.Status.Reason == reason && fresh.Status.Message == message {
			return nil
		}

		fresh.Status.Phase = phase
		fresh.Status.Reason = reason
		fresh.Status.Message = message
		fresh.Status.LastUpdated = &metav1.Time{Time: time.Now()}

		return r.Status().Update(ctx, fresh)
	})

	if err != nil {
		return ctrl.Result{}, err
	}

	// Requeue if pending
	if phase == "Pending" {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *LLMEngineReconciler) handleDeletion(ctx context.Context, llmEngine *aitrigramv1.LLMEngine) error {
	logger := log.FromContext(ctx)

	// Cleanup read-only PV+PVC for each model reference (never touches underlying data)
	modelNames := make([]string, 0, len(llmEngine.Spec.ModelRefs))
	for _, ref := range llmEngine.Spec.ModelRefs {
		modelNames = append(modelNames, ref.Name)
		if err := storage.CleanupLLMEngineStorage(ctx, r.Client, llmEngine, ref.Name); err != nil {
			logger.Error(err, "Failed to cleanup storage for model", "model", ref.Name)
		}
	}

	// Update referenced engines (this engine is being deleted, so it will be excluded)
	r.updateReferencedEngines(ctx, modelNames)

	// Remove finalizer with retry to handle conflicts
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get fresh copy
		key := client.ObjectKeyFromObject(llmEngine)
		fresh := &aitrigramv1.LLMEngine{}
		if err := r.Get(ctx, key, fresh); err != nil {
			return err
		}

		// Remove finalizer from fresh copy
		controllerutil.RemoveFinalizer(fresh, LLMEngineFinalizer)
		return r.Update(ctx, fresh)
	})
}

// updateReferencedEngines scans all LLMEngines and updates each ModelRepository's
// status.referencedEngines with a comma-separated list of referencing engine names.
func (r *LLMEngineReconciler) updateReferencedEngines(ctx context.Context, modelRepoNames []string) {
	logger := log.FromContext(ctx)

	// List all LLMEngines across all namespaces
	allEngines := &aitrigramv1.LLMEngineList{}
	if err := r.List(ctx, allEngines); err != nil {
		logger.Error(err, "Failed to list LLMEngines for referenced engines update")
		return
	}

	// Build map: modelRepo name -> set of engine names (namespace/name)
	refMap := map[string]map[string]struct{}{}
	for _, name := range modelRepoNames {
		refMap[name] = map[string]struct{}{}
	}

	for _, engine := range allEngines.Items {
		// Skip engines being deleted
		if engine.DeletionTimestamp != nil {
			continue
		}
		for _, ref := range engine.Spec.ModelRefs {
			if _, tracked := refMap[ref.Name]; tracked {
				engineKey := fmt.Sprintf("%s/%s", engine.Namespace, engine.Name)
				refMap[ref.Name][engineKey] = struct{}{}
			}
		}
	}

	// Update each ModelRepository's status
	for _, repoName := range modelRepoNames {
		engines := refMap[repoName]
		names := make([]string, 0, len(engines))
		for name := range engines {
			names = append(names, name)
		}
		sort.Strings(names)
		referencedStr := strings.Join(names, ",")

		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			modelRepo := &aitrigramv1.ModelRepository{}
			if err := r.Get(ctx, types.NamespacedName{Name: repoName}, modelRepo); err != nil {
				return client.IgnoreNotFound(err)
			}
			if modelRepo.Status.ReferencedEngines == referencedStr {
				return nil // no change
			}
			modelRepo.Status.ReferencedEngines = referencedStr
			return r.Status().Update(ctx, modelRepo)
		}); err != nil {
			logger.Error(err, "Failed to update referencedEngines", "modelRepository", repoName)
		}
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *LLMEngineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aitrigramv1.LLMEngine{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Watches(
			&aitrigramv1.ModelRepository{},
			handler.EnqueueRequestsFromMapFunc(r.modelRepositoryToLLMEngine),
		).
		Named("llmengine").
		Complete(r)
}

// modelRepositoryToLLMEngine maps ModelRepository events to LLMEngine reconciliations
func (r *LLMEngineReconciler) modelRepositoryToLLMEngine(ctx context.Context, obj client.Object) []reconcile.Request {
	modelRepo := obj.(*aitrigramv1.ModelRepository)

	// Find all LLMEngines that reference this ModelRepository
	llmEngineList := &aitrigramv1.LLMEngineList{}
	if err := r.List(ctx, llmEngineList); err != nil {
		return []reconcile.Request{}
	}

	var requests []reconcile.Request
	for _, llmEngine := range llmEngineList.Items {
		for _, modelRef := range llmEngine.Spec.ModelRefs {
			if modelRef.Name == modelRepo.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      llmEngine.Name,
						Namespace: llmEngine.Namespace,
					},
				})
				break
			}
		}
	}

	return requests
}
