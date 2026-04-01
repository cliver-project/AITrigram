package adapters

import (
	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/component"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewModelComponent creates a component for a specific LLMEngine + ModelRepository pair.
// This is called once per ModelRef in the LLMEngine spec.
//
// The component name is unique per model: "{engine-name}-{model-name}".
// Each component manages one Deployment and one Service.
func NewModelComponent(llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository) component.LLMEngineComponent {
	// Component name is unique per model combination
	componentName := ComponentName(llmEngine.Name, modelRepo.Name)

	builder := component.NewLLMEngineComponent(componentName).
		WithDeploymentAdapter(AdaptDeployment).
		WithServiceAdapter(AdaptService)

	// Add engine-type specific customizations
	switch llmEngine.Spec.EngineType {
	case aitrigramv1.LLMEngineTypeOllama:
		// Ollama doesn't require additional manifests
	case aitrigramv1.LLMEngineTypeVLLM:
		// vLLM requires a ConfigMap for configuration
		builder.WithManifestAdapter("vllm-config",
			component.NewManifestAdapterWithPredicate(
				func() client.Object { return &corev1.ConfigMap{} },
				AdaptVLLMConfigMap,
				IsVLLMEngine,
			))
	}

	return builder.Build()
}
