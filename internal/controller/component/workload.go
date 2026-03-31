package component

import (
	"fmt"
	"reflect"

	"github.com/cliver-project/AITrigram/internal/controller/assets"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// llmEngineWorkload implements LLMEngineComponent.
// It manages Deployment and Service resources for an LLMEngine+ModelRepository pair.
type llmEngineWorkload struct {
	name             string
	adaptDeployment  func(LLMEngineContext, *appsv1.Deployment) error
	adaptService     func(LLMEngineContext, *corev1.Service) error
	manifestAdapters map[string]ManifestAdapter
	predicate        func(LLMEngineContext) (bool, error)
}

// Name returns the component name.
func (w *llmEngineWorkload) Name() string {
	return w.name
}

// Reconcile ensures the component's resources match the desired state.
func (w *llmEngineWorkload) Reconcile(ctx LLMEngineContext) error {
	logger := log.FromContext(ctx)

	// Check predicate - if false, delete component resources
	if w.predicate != nil {
		enabled, err := w.predicate(ctx)
		if err != nil {
			return fmt.Errorf("predicate failed for component %s: %w", w.name, err)
		}
		if !enabled {
			logger.Info("Component disabled by predicate, deleting resources", "component", w.name)
			return w.deleteResources(ctx)
		}
	}

	// Reconcile resources: manifests → deployment → service
	return w.updateResources(ctx)
}

// updateResources creates or updates all component resources.
func (w *llmEngineWorkload) updateResources(ctx LLMEngineContext) error {
	// 1. Reconcile additional manifests first (ConfigMaps, Secrets that Deployment might reference)
	if err := w.reconcileManifests(ctx); err != nil {
		return err
	}

	// 2. Reconcile Deployment
	if err := w.reconcileDeployment(ctx); err != nil {
		return err
	}

	// 3. Reconcile Service
	if err := w.reconcileService(ctx); err != nil {
		return err
	}

	return nil
}

// reconcileDeployment creates or updates the Deployment.
func (w *llmEngineWorkload) reconcileDeployment(ctx LLMEngineContext) error {
	logger := log.FromContext(ctx)

	// Determine engine type for asset loading
	engineType := string(ctx.LLMEngine.Spec.EngineType)

	// Load deployment template from assets
	deployment, err := assets.LoadLLMEngineDeploymentManifest(engineType)
	if err != nil {
		return fmt.Errorf("failed to load deployment manifest for %s: %w", w.name, err)
	}

	// Set namespace
	deployment.SetNamespace(ctx.Namespace)

	// Apply adapter function if provided
	if w.adaptDeployment != nil {
		if err := w.adaptDeployment(ctx, deployment); err != nil {
			return fmt.Errorf("failed to adapt deployment for %s: %w", w.name, err)
		}
	}

	// Set owner reference to LLMEngine for garbage collection
	if err := ctrl.SetControllerReference(ctx.LLMEngine, deployment, ctx.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference for deployment %s: %w", w.name, err)
	}

	// Create or update deployment
	if err := w.applyObject(ctx, deployment); err != nil {
		return fmt.Errorf("failed to apply deployment for %s: %w", w.name, err)
	}

	logger.V(1).Info("Reconciled deployment", "component", w.name, "deployment", deployment.Name)
	return nil
}

// reconcileService creates or updates the Service.
func (w *llmEngineWorkload) reconcileService(ctx LLMEngineContext) error {
	logger := log.FromContext(ctx)

	// Determine engine type for asset loading
	engineType := string(ctx.LLMEngine.Spec.EngineType)

	// Load service template from assets
	service, err := assets.LoadLLMEngineServiceManifest(engineType)
	if err != nil {
		return fmt.Errorf("failed to load service manifest for %s: %w", w.name, err)
	}

	// Set namespace
	service.SetNamespace(ctx.Namespace)

	// Apply adapter function if provided
	if w.adaptService != nil {
		if err := w.adaptService(ctx, service); err != nil {
			return fmt.Errorf("failed to adapt service for %s: %w", w.name, err)
		}
	}

	// Set owner reference to LLMEngine
	if err := ctrl.SetControllerReference(ctx.LLMEngine, service, ctx.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference for service %s: %w", w.name, err)
	}

	// Create or update service
	if err := w.applyObject(ctx, service); err != nil {
		return fmt.Errorf("failed to apply service for %s: %w", w.name, err)
	}

	logger.V(1).Info("Reconciled service", "component", w.name, "service", service.Name)
	return nil
}

// reconcileManifests reconciles additional manifests (ConfigMaps, Secrets, etc.).
func (w *llmEngineWorkload) reconcileManifests(ctx LLMEngineContext) error {
	logger := log.FromContext(ctx)

	for manifestName, adapter := range w.manifestAdapters {
		// Check predicate if provided
		if adapter.predicate != nil {
			enabled, err := adapter.predicate(ctx)
			if err != nil {
				return fmt.Errorf("predicate failed for manifest %s: %w", manifestName, err)
			}
			if !enabled {
				continue // Skip this manifest
			}
		}

		// Create manifest object using factory function
		if adapter.create == nil {
			return fmt.Errorf("manifest adapter %s missing create function", manifestName)
		}
		manifest := adapter.create()

		// Set namespace
		manifest.SetNamespace(ctx.Namespace)

		// Apply adapter function to configure the object
		if adapter.adapt != nil {
			if err := adapter.adapt(ctx, manifest); err != nil {
				return fmt.Errorf("failed to adapt manifest %s: %w", manifestName, err)
			}
		}

		// Set owner reference to LLMEngine for garbage collection
		if err := ctrl.SetControllerReference(ctx.LLMEngine, manifest, ctx.Scheme); err != nil {
			return fmt.Errorf("failed to set owner reference for manifest %s: %w", manifestName, err)
		}

		// Create or update the manifest
		if err := w.applyObject(ctx, manifest); err != nil {
			return fmt.Errorf("failed to apply manifest %s: %w", manifestName, err)
		}

		logger.V(1).Info("Reconciled manifest", "component", w.name, "manifest", manifestName, "name", manifest.GetName())
	}

	return nil
}

// applyObject creates or updates a generic Kubernetes object.
// This follows the pattern from Hypershift's resource application, handling any client.Object type.
func (w *llmEngineWorkload) applyObject(ctx LLMEngineContext, obj client.Object) error {
	// Create a new instance of the same type for fetching existing resource
	existing := obj.DeepCopyObject().(client.Object)

	err := ctx.Client.Get(ctx, types.NamespacedName{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}, existing)

	if err != nil {
		if errors.IsNotFound(err) {
			// Create new object
			return ctx.Client.Create(ctx, obj)
		}
		return err
	}

	// Update existing object — only if something actually changed
	needsUpdate := false

	// Check labels and annotations
	if !reflect.DeepEqual(existing.GetLabels(), obj.GetLabels()) {
		existing.SetLabels(obj.GetLabels())
		needsUpdate = true
	}
	if !reflect.DeepEqual(existing.GetAnnotations(), obj.GetAnnotations()) {
		existing.SetAnnotations(obj.GetAnnotations())
		needsUpdate = true
	}

	// Type-specific updates for known types
	switch typedObj := obj.(type) {
	case *corev1.ConfigMap:
		existingCM := existing.(*corev1.ConfigMap)
		if !reflect.DeepEqual(existingCM.Data, typedObj.Data) || !reflect.DeepEqual(existingCM.BinaryData, typedObj.BinaryData) {
			existingCM.Data = typedObj.Data
			existingCM.BinaryData = typedObj.BinaryData
			needsUpdate = true
		}

	case *corev1.Secret:
		existingSecret := existing.(*corev1.Secret)
		if !reflect.DeepEqual(existingSecret.Data, typedObj.Data) || existingSecret.Type != typedObj.Type {
			existingSecret.Data = typedObj.Data
			existingSecret.Type = typedObj.Type
			needsUpdate = true
		}

	case *appsv1.Deployment:
		existingDeployment := existing.(*appsv1.Deployment)
		// Only compare mutable fields — never touch Selector (immutable after creation)
		if !reflect.DeepEqual(existingDeployment.Spec.Replicas, typedObj.Spec.Replicas) ||
			!reflect.DeepEqual(existingDeployment.Spec.Template, typedObj.Spec.Template) ||
			!reflect.DeepEqual(existingDeployment.Spec.Strategy, typedObj.Spec.Strategy) {
			existingDeployment.Spec.Replicas = typedObj.Spec.Replicas
			existingDeployment.Spec.Template = typedObj.Spec.Template
			existingDeployment.Spec.Strategy = typedObj.Spec.Strategy
			needsUpdate = true
		}

	case *corev1.Service:
		existingService := existing.(*corev1.Service)
		// Compare excluding ClusterIP (immutable)
		desiredSpec := typedObj.Spec.DeepCopy()
		desiredSpec.ClusterIP = existingService.Spec.ClusterIP
		if !reflect.DeepEqual(existingService.Spec, *desiredSpec) {
			clusterIP := existingService.Spec.ClusterIP
			existingService.Spec = typedObj.Spec
			existingService.Spec.ClusterIP = clusterIP
			needsUpdate = true
		}

	default:
		return ctx.Client.Update(ctx, obj)
	}

	if !needsUpdate {
		return nil
	}
	return ctx.Client.Update(ctx, existing)
}

// deleteResources deletes all component resources.
func (w *llmEngineWorkload) deleteResources(ctx LLMEngineContext) error {
	// Delete deployment
	deployment := &appsv1.Deployment{}
	deployment.Name = w.name
	deployment.Namespace = ctx.Namespace
	if err := ctx.Client.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete deployment %s: %w", w.name, err)
	}

	// Delete service
	service := &corev1.Service{}
	service.Name = w.name
	service.Namespace = ctx.Namespace
	if err := ctx.Client.Delete(ctx, service); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete service %s: %w", w.name, err)
	}

	return nil
}
