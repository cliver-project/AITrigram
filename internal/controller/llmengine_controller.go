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

	aitrigramv1 "github.com/gaol/AITrigram/api/v1"
)

// LLMEngineReconciler reconciles a LLMEngine object
type LLMEngineReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	OperatorNamespace string
	OperatorPodName   string
}

// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=llmengines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=llmengines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=llmengines/finalizers,verbs=update
// +kubebuilder:rbac:groups=aitrigram.ihomeland.cn,resources=modelrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

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
	if !controllerutil.ContainsFinalizer(llmEngine, "llmengine.aitrigram.ihomeland.cn/finalizer") {
		controllerutil.AddFinalizer(llmEngine, "llmengine.aitrigram.ihomeland.cn/finalizer")
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
			return r.updateStatus(ctx, llmEngine, "Pending", "WaitingForModels", msg)
		}
	}

	// Create or update Deployment
	deployment, err := r.createOrUpdateDeployment(ctx, llmEngine, modelRepos)
	if err != nil {
		return r.updateStatus(ctx, llmEngine, "Error", "DeploymentFailed", err.Error())
	}

	// Create or update Service
	if err := r.createOrUpdateService(ctx, llmEngine); err != nil {
		return r.updateStatus(ctx, llmEngine, "Error", "ServiceFailed", err.Error())
	}

	// Update status based on deployment readiness
	if deployment.Status.ReadyReplicas > 0 {
		modelNames := make([]string, len(modelRepos))
		for i, mr := range modelRepos {
			modelNames[i] = mr.Name
		}
		llmEngine.Status.ModelRepositories = modelNames
		return r.updateStatus(ctx, llmEngine, "Running", "DeploymentReady", fmt.Sprintf("Serving %d models", len(modelRepos)))
	}

	return r.updateStatus(ctx, llmEngine, "Starting", "DeploymentCreated", "Waiting for deployment to become ready")
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

func (r *LLMEngineReconciler) createOrUpdateDeployment(ctx context.Context, llmEngine *aitrigramv1.LLMEngine, modelRepos []*aitrigramv1.ModelRepository) (*appsv1.Deployment, error) {
	deployment := r.buildDeployment(llmEngine, modelRepos)

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

func (r *LLMEngineReconciler) buildDeployment(llmEngine *aitrigramv1.LLMEngine, modelRepos []*aitrigramv1.ModelRepository) *appsv1.Deployment {
	replicas := int32(1)

	// Determine image based on engine type
	image := llmEngine.Spec.Image
	if image == "" {
		switch llmEngine.Spec.EngineType {
		case aitrigramv1.LLMEngineTypeOllama:
			image = "ollama/ollama:latest"
		case aitrigramv1.LLMEngineTypeVLLM:
			image = "vllm/vllm-openai:latest"
		default:
			image = "vllm/vllm-openai:latest"
		}
	}

	// Determine port
	port := llmEngine.Spec.Port
	if port == 0 {
		switch llmEngine.Spec.EngineType {
		case aitrigramv1.LLMEngineTypeOllama:
			port = 11434
		case aitrigramv1.LLMEngineTypeVLLM:
			port = 8000
		default:
			port = 8000
		}
	}

	// Build volume mounts and volumes from ModelRepositories
	volumeMounts := []corev1.VolumeMount{}
	volumes := []corev1.Volume{}

	for i, modelRepo := range modelRepos {
		volumeName := fmt.Sprintf("model-%d", i)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      volumeName,
			MountPath: modelRepo.Spec.Storage.Path,
		})
		volumes = append(volumes, corev1.Volume{
			Name:         volumeName,
			VolumeSource: modelRepo.Spec.Storage.VolumeSource,
		})
	}

	// Add cache volume if specified
	if llmEngine.Spec.Cache != nil {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "cache",
			MountPath: llmEngine.Spec.Cache.Path,
		})
		volumes = append(volumes, corev1.Volume{
			Name:         "cache",
			VolumeSource: llmEngine.Spec.Cache.VolumeSource,
		})
	}

	// Build container args
	args := llmEngine.Spec.Args
	if len(args) == 0 {
		switch llmEngine.Spec.EngineType {
		case aitrigramv1.LLMEngineTypeOllama:
			args = []string{"/bin/ollama", "serve"}
		case aitrigramv1.LLMEngineTypeVLLM:
			args = []string{"--host", "0.0.0.0", "--port", fmt.Sprintf("%d", port)}
		}
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      llmEngine.Name,
			Namespace: llmEngine.Namespace,
			Labels: map[string]string{
				"app":         llmEngine.Name,
				"engine-type": string(llmEngine.Spec.EngineType),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": llmEngine.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":         llmEngine.Name,
						"engine-type": string(llmEngine.Spec.EngineType),
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "llm-engine",
							Image: image,
							Args:  args,
							Env:   llmEngine.Spec.Env,
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: port,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: volumeMounts,
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	return deployment
}

func (r *LLMEngineReconciler) createOrUpdateService(ctx context.Context, llmEngine *aitrigramv1.LLMEngine) error {
	servicePort := llmEngine.Spec.ServicePort
	if servicePort == 0 {
		servicePort = 8080
	}

	targetPort := llmEngine.Spec.Port
	if targetPort == 0 {
		switch llmEngine.Spec.EngineType {
		case aitrigramv1.LLMEngineTypeOllama:
			targetPort = 11434
		case aitrigramv1.LLMEngineTypeVLLM:
			targetPort = 8000
		default:
			targetPort = 8000
		}
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      llmEngine.Name,
			Namespace: llmEngine.Namespace,
			Labels: map[string]string{
				"app": llmEngine.Name,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": llmEngine.Name,
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
	controllerutil.RemoveFinalizer(llmEngine, "llmengine.aitrigram.ihomeland.cn/finalizer")
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
