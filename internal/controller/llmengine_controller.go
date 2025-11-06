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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

const (
	LLMEngineFinalizer = "llmengine.aitrigram.ihomeland.cn/finalizer"
)

// LLMEngineReconciler reconciles a LLMEngine object
type LLMEngineReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	OperatorNamespace string
}

// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=llmengines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=llmengines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=llmengines/finalizers,verbs=update
// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=modelrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

func (r *LLMEngineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

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
		if modelRepo.Status.Phase != aitrigramv1.ModelRepositoryPhaseDownloaded {
			msg := fmt.Sprintf("Waiting for ModelRepository %s (phase: %s)", modelRepo.Name, modelRepo.Status.Phase)
			//TODO maybe trigger the downloading if the autodownload is false
			return r.updateStatus(ctx, llmEngine, "Pending", "WaitingForModels", msg)
		}
	}

	// Validate GPU availability if GPU is enabled
	if llmEngine.Spec.GPU != nil && llmEngine.Spec.GPU.Enabled {
		if err := r.validateGPUAvailability(ctx, llmEngine); err != nil {
			return r.updateStatus(ctx, llmEngine, "Error", "GPUValidationFailed", err.Error())
		}
	}

	// Create one Deployment and Service per ModelRef
	totalReady := 0
	totalDeployments := len(modelRepos)

	for _, modelRepo := range modelRepos {
		// Create or update Deployment for this model
		deployment, err := r.createOrUpdateDeploymentForModel(ctx, llmEngine, modelRepo)
		if err != nil {
			return r.updateStatus(ctx, llmEngine, "Error", "DeploymentFailed",
				fmt.Sprintf("Failed to create deployment for model %s: %v", modelRepo.Name, err))
		}

		// Create or update Service for this model
		if err := r.createOrUpdateServiceForModel(ctx, llmEngine, modelRepo); err != nil {
			return r.updateStatus(ctx, llmEngine, "Error", "ServiceFailed",
				fmt.Sprintf("Failed to create service for model %s: %v", modelRepo.Name, err))
		}

		// Count ready deployments
		if deployment.Status.ReadyReplicas > 0 {
			totalReady++
		}
	}

	// Update status based on deployment readiness
	modelNames := make([]string, len(modelRepos))
	for i, mr := range modelRepos {
		modelNames[i] = mr.Name
	}
	llmEngine.Status.ModelRepositories = modelNames

	if totalReady == totalDeployments {
		return r.updateStatus(ctx, llmEngine, "Running", "DeploymentsReady",
			fmt.Sprintf("All %d model deployments ready", totalDeployments))
	}

	return r.updateStatus(ctx, llmEngine, "Starting", "DeploymentsCreated",
		fmt.Sprintf("%d/%d deployments ready", totalReady, totalDeployments))
}

func (r *LLMEngineReconciler) fetchModelRepositories(ctx context.Context, llmEngine *aitrigramv1.LLMEngine) ([]*aitrigramv1.ModelRepository, error) {
	var modelRepos []*aitrigramv1.ModelRepository

	for _, modelRef := range llmEngine.Spec.ModelRefs {
		modelRepo := &aitrigramv1.ModelRepository{}
		// ModelRepository is cluster-scoped, so no namespace
		if err := r.Get(ctx, types.NamespacedName{Name: modelRef}, modelRepo); err != nil {
			if errors.IsNotFound(err) {
				return nil, fmt.Errorf("ModelRepository %s not found", modelRef)
			}
			return nil, err
		}
		modelRepos = append(modelRepos, modelRepo)
	}

	return modelRepos, nil
}

func (r *LLMEngineReconciler) createOrUpdateDeploymentForModel(ctx context.Context, llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository) (*appsv1.Deployment, error) {
	deployment := r.buildDeploymentForModel(llmEngine, modelRepo)

	// Set owner reference
	if err := ctrl.SetControllerReference(llmEngine, deployment, r.Scheme); err != nil {
		return nil, err
	}

	// Create or update
	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			if err := r.Create(ctx, deployment); err != nil {
				return nil, err
			}
			return deployment, nil
		}
		return nil, err
	}

	// Update if needed
	existing.Spec = deployment.Spec
	if err := r.Update(ctx, existing); err != nil {
		return nil, err
	}

	return existing, nil
}

// detectGPUAvailability checks if GPU resources are requested
// validateGPUAvailability validates that GPU nodes are available in the cluster
// Returns an error if GPU is required but no suitable nodes are found
func (r *LLMEngineReconciler) validateGPUAvailability(ctx context.Context, llmEngine *aitrigramv1.LLMEngine) error {
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

	// List all nodes
	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	// Find nodes with GPU resources
	var suitableNodes []string
	var gpuNodes []string
	requestedGPUs := gpuConfig.Count
	if requestedGPUs == 0 {
		requestedGPUs = 1
	}

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

func (r *LLMEngineReconciler) buildDeploymentForModel(llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository) *appsv1.Deployment {
	// Get replicas from spec, default to 1
	replicas := int32(1)
	if llmEngine.Spec.Replicas != nil {
		replicas = *llmEngine.Spec.Replicas
	}

	// Build workload configuration using the new workload struct
	workload, _ := BuildLLMEngineWorkload(llmEngine, modelRepo)

	// Generate unique name for this deployment (engine-name + model-name)
	deploymentName := fmt.Sprintf("%s-%s", llmEngine.Name, modelRepo.Name)

	// Build container with resources and security context
	container := corev1.Container{
		Name:  "llm-engine",
		Image: workload.Image,
		Args:  workload.Args,
		Env:   workload.Envs,
		Ports: []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: workload.PodPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts:    workload.Storage.VolumeMounts,
		Resources:       workload.Resources,
		SecurityContext: workload.SecurityContext,
	}

	// Build pod spec with node selector and tolerations for GPU
	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{container},
		Volumes:    workload.Storage.Volumes,
		HostIPC:    llmEngine.Spec.HostIPC,
	}

	// Add GPU node selector and tolerations if GPU is enabled
	if workload.RequestGPU {
		if len(workload.NodeSelector) > 0 {
			podSpec.NodeSelector = workload.NodeSelector
		}

		if len(workload.Tolerations) > 0 {
			podSpec.Tolerations = workload.Tolerations
		}
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: llmEngine.Namespace,
			Labels: map[string]string{
				"app":         llmEngine.Name,
				"model":       modelRepo.Name,
				"engine-type": string(llmEngine.Spec.EngineType),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":   llmEngine.Name,
					"model": modelRepo.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":         llmEngine.Name,
						"model":       modelRepo.Name,
						"engine-type": string(llmEngine.Spec.EngineType),
					},
				},
				Spec: podSpec,
			},
		},
	}

	return deployment
}

func (r *LLMEngineReconciler) createOrUpdateServiceForModel(ctx context.Context, llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository) error {
	// Build workload configuration to get port information
	workload, _ := BuildLLMEngineWorkload(llmEngine, modelRepo)

	servicePort := workload.ServicePort
	targetPort := workload.PodPort

	// Generate unique name for this service (engine-name + model-name)
	serviceName := fmt.Sprintf("%s-%s", llmEngine.Name, modelRepo.Name)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: llmEngine.Namespace,
			Labels: map[string]string{
				"app":   llmEngine.Name,
				"model": modelRepo.Name,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app":   llmEngine.Name,
				"model": modelRepo.Name,
			},
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       servicePort,
					TargetPort: intstr.FromInt(int(targetPort)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	// Set owner reference
	if err := ctrl.SetControllerReference(llmEngine, service, r.Scheme); err != nil {
		return err
	}

	// Create or update
	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			return r.Create(ctx, service)
		}
		return err
	}

	// Update if needed
	existing.Spec.Ports = service.Spec.Ports
	existing.Spec.Selector = service.Spec.Selector
	return r.Update(ctx, existing)
}

func (r *LLMEngineReconciler) updateStatus(ctx context.Context, llmEngine *aitrigramv1.LLMEngine, phase, reason, message string) (ctrl.Result, error) {
	llmEngine.Status.Phase = phase
	llmEngine.Status.Reason = reason
	llmEngine.Status.Message = message
	llmEngine.Status.LastUpdated = &metav1.Time{Time: time.Now()}

	if err := r.Status().Update(ctx, llmEngine); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue if pending
	if phase == "Pending" {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *LLMEngineReconciler) handleDeletion(ctx context.Context, llmEngine *aitrigramv1.LLMEngine) error {
	// Remove finalizer
	controllerutil.RemoveFinalizer(llmEngine, LLMEngineFinalizer)
	return r.Update(ctx, llmEngine)
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
			if modelRef == modelRepo.Name {
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
