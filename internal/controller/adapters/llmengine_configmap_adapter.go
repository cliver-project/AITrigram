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

package adapters

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cliver-project/AITrigram/internal/controller/component"
)

// AdaptVLLMConfigMap adapts the vLLM ConfigMap with model-specific configuration
func AdaptVLLMConfigMap(ctx component.LLMEngineContext, obj client.Object) error {
	configMap, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return fmt.Errorf("expected ConfigMap, got %T", obj)
	}

	llmEngine := ctx.LLMEngine
	modelRepo := ctx.ModelRepo

	// Build vLLM configuration
	requestGPU := DetectGPURequest(llmEngine)
	vllmConfig := BuildVLLMConfig(requestGPU, llmEngine.Spec.Args)

	// Marshal to YAML
	yamlData, err := MarshalVLLMConfig(vllmConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal vLLM config to YAML: %w", err)
	}

	// Set ConfigMap name and labels
	configMap.Name = GetVLLMConfigMapName(llmEngine.Name, modelRepo.Name)
	configMap.Namespace = llmEngine.Namespace

	if configMap.Labels == nil {
		configMap.Labels = make(map[string]string)
	}
	configMap.Labels["app"] = llmEngine.Name
	configMap.Labels["model"] = modelRepo.Name
	configMap.Labels["engine-type"] = string(llmEngine.Spec.EngineType)

	// Set ConfigMap data
	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}
	configMap.Data[VLLMConfigMapKeyName] = string(yamlData)

	return nil
}

// IsVLLMEngine returns true if the engine type is vLLM
func IsVLLMEngine(ctx component.LLMEngineContext) (bool, error) {
	return ctx.LLMEngine.Spec.EngineType == "vllm", nil
}
