package component

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ComponentBuilder builds LLMEngine components using a fluent API.
type ComponentBuilder struct {
	name             string
	adaptDeployment  func(LLMEngineContext, *appsv1.Deployment) error
	adaptService     func(LLMEngineContext, *corev1.Service) error
	manifestAdapters map[string]ManifestAdapter
	predicate        func(LLMEngineContext) (bool, error)
}

// NewLLMEngineComponent creates a new component builder.
// name should be unique (typically "{engine-name}-{model-name}").
func NewLLMEngineComponent(name string) *ComponentBuilder {
	return &ComponentBuilder{
		name:             name,
		manifestAdapters: make(map[string]ManifestAdapter),
	}
}

// WithDeploymentAdapter registers the deployment adapter function.
// The adapter function is called to customize the Deployment loaded from the template.
func (b *ComponentBuilder) WithDeploymentAdapter(fn func(LLMEngineContext, *appsv1.Deployment) error) *ComponentBuilder {
	b.adaptDeployment = fn
	return b
}

// WithServiceAdapter registers the service adapter function.
// The adapter function is called to customize the Service loaded from the template.
func (b *ComponentBuilder) WithServiceAdapter(fn func(LLMEngineContext, *corev1.Service) error) *ComponentBuilder {
	b.adaptService = fn
	return b
}

// WithManifestAdapter registers an adapter for additional manifests (ConfigMaps, Secrets, etc.).
// manifestName should match the filename in the assets directory (e.g., "configmap.yaml").
func (b *ComponentBuilder) WithManifestAdapter(manifestName string, adapter ManifestAdapter) *ComponentBuilder {
	b.manifestAdapters[manifestName] = adapter
	return b
}

// WithPredicate registers a predicate to conditionally enable the component.
// If the predicate returns false, the component's resources will be deleted.
// If the predicate returns an error, reconciliation fails.
func (b *ComponentBuilder) WithPredicate(fn func(LLMEngineContext) (bool, error)) *ComponentBuilder {
	b.predicate = fn
	return b
}

// Build creates the final LLMEngineComponent.
func (b *ComponentBuilder) Build() LLMEngineComponent {
	return &llmEngineWorkload{
		name:             b.name,
		adaptDeployment:  b.adaptDeployment,
		adaptService:     b.adaptService,
		manifestAdapters: b.manifestAdapters,
		predicate:        b.predicate,
	}
}

// ManifestAdapter adapts generic manifests (ConfigMaps, Secrets, etc.).
type ManifestAdapter struct {
	// create creates an empty object of the correct type
	create func() client.Object
	// adapt configures the object
	adapt     func(LLMEngineContext, client.Object) error
	predicate func(LLMEngineContext) (bool, error)
}

// NewManifestAdapter creates a manifest adapter with object factory and adapt function.
// createFn: factory function that creates an empty object of the correct type
// adaptFn: function that configures the object
func NewManifestAdapter(
	createFn func() client.Object,
	adaptFn func(LLMEngineContext, client.Object) error,
) ManifestAdapter {
	return ManifestAdapter{
		create: createFn,
		adapt:  adaptFn,
	}
}

// NewManifestAdapterWithPredicate creates a manifest adapter with object factory, adapt function, and predicate.
func NewManifestAdapterWithPredicate(
	createFn func() client.Object,
	adaptFn func(LLMEngineContext, client.Object) error,
	predicateFn func(LLMEngineContext) (bool, error),
) ManifestAdapter {
	return ManifestAdapter{
		create:    createFn,
		adapt:     adaptFn,
		predicate: predicateFn,
	}
}
