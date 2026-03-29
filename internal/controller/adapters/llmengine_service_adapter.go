package adapters

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/cliver-project/AITrigram/internal/controller/component"
)

// AdaptService customizes the service template for a specific LLMEngine + ModelRepository.
// It applies dynamic configuration to the asset template based on the LLMEngine spec.
func AdaptService(ctx component.LLMEngineContext, service *corev1.Service) error {
	llmEngine := ctx.LLMEngine
	modelRepo := ctx.ModelRepo

	// Set name and labels
	name := fmt.Sprintf("%s-%s", llmEngine.Name, modelRepo.Name)
	service.SetName(name)
	service.Labels = map[string]string{
		"app":   llmEngine.Name,
		"model": modelRepo.Name,
	}

	// Set selector (targets pods with matching app and model labels)
	service.Spec.Selector = map[string]string{
		"app":   llmEngine.Name,
		"model": modelRepo.Name,
	}

	// Calculate ports
	servicePort := GetServicePort(llmEngine)
	podPort := GetPodPort(llmEngine)

	// Set ports
	service.Spec.Ports[0].Port = servicePort
	service.Spec.Ports[0].TargetPort = intstr.FromInt(int(podPort))

	return nil
}
