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
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/storage"
)

// ComponentName returns a name for Deployment/Service resources derived from
// the engine and model names. The result is capped at 52 chars so that
// Kubernetes can append ReplicaSet and pod hashes without exceeding 63 chars.
func ComponentName(engineName, modelName string) string {
	return storage.TruncatedName(52, "", engineName, modelName)
}

const (
	// DefaultGPUResourceType is the default GPU resource type for Kubernetes
	DefaultGPUResourceType = "nvidia.com/gpu"

	// Model storage paths (shared across pods, mounted from PVC/NFS)
	DefaultModelStoragePath = "/data/models"

	// Images for serving, downloading, and maybe deleting models
	DefaultVLLMImage   = "vllm/vllm-openai:latest"
	DefaultOllamaImage = "ollama/ollama:latest"

	// vLLM config file constants
	VLLMConfigMapKeyName = "vllm-config.yaml"
	VLLMConfigMountPath  = "/etc/vllm"
	VLLMConfigFilePath   = "/etc/vllm/vllm-config.yaml"
)

// GetStoragePaths returns production-ready storage paths for a given LLMEngine and model repository.
// Returns (modelPath, envPaths) where:
//   - modelPath: the shared model storage path (read-only for engine)
//   - envPaths: environment variable names to path values for configuring the engine
//
// The modelPath is taken from the ModelRepository's storage path to ensure consistency
// between where the model was downloaded and where the engine will look for it.
func GetStoragePaths(llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository) (string, map[string]string) {
	// Use the ModelRepository's storage mount path as the model path
	// This ensures the LLMEngine mounts at the same location where models were downloaded
	modelPath := DefaultModelStoragePath
	if modelRepo != nil && modelRepo.Spec.Storage.MountPath != "" {
		modelPath = modelRepo.Spec.Storage.MountPath
	}

	// Initialize environment paths
	envPaths := make(map[string]string)

	// Add engine-specific model paths
	switch llmEngine.Spec.EngineType {
	case aitrigramv1.LLMEngineTypeVLLM:
		// For HuggingFace models used with vLLM:
		// HF_HOME points to the shared model storage where HF models are downloaded
		// This is the same as the ModelRepository storage path
		if modelRepo != nil && modelRepo.Spec.Source.Origin == aitrigramv1.ModelOriginHuggingFace {
			envPaths["HF_HOME"] = modelPath
			envPaths["TRANSFORMERS_CACHE"] = modelPath
			envPaths["HUGGINGFACE_HUB_CACHE"] = modelPath + "/hub"
		}
	case aitrigramv1.LLMEngineTypeOllama:
		// For Ollama, OLLAMA_MODELS points to model storage
		envPaths["OLLAMA_MODELS"] = modelPath
	}

	return modelPath, envPaths
}

// DetectGPURequest checks if GPU resources are requested in the LLMEngine spec
// Returns true if GPU resources are requested, false otherwise
func DetectGPURequest(llmEngine *aitrigramv1.LLMEngine) bool {
	return llmEngine.Spec.GPU != nil && llmEngine.Spec.GPU.Enabled
}

// BuildVLLMConfig constructs vLLM configuration map based on GPU availability and user args.
// Returns a map that will be marshaled to YAML for vLLM config file.
//
// Security: trust_remote_code is always set to false for read-only inference to prevent
// arbitrary code execution from model repositories.
//
// Note: --model, --revision, --host, --port are passed as CLI arguments (not in config file)
// to ensure CLI args always take precedence over config file values.
func BuildVLLMConfig(requestGPU bool, userArgs []string) map[string]interface{} {
	config := make(map[string]interface{})

	// Default parameters based on GPU availability
	if requestGPU {
		// GPU-optimized vLLM configuration
		config["dtype"] = "float8_e5m2"
		config["max_num_batched_tokens"] = 32768
		config["max_model_len"] = 8192
		config["enforce_eager"] = true
		// GPU memory utilization - use 95% for single GPU inference
		config["gpu_memory_utilization"] = 0.95
		// Attention backend configuration (moved from deprecated env var)
		config["attention_config"] = map[string]interface{}{
			"backend": "FLASH_ATTN", // Use FlashAttention for best GPU performance
		}
	} else {
		// CPU-only vLLM configuration
		config["dtype"] = "half"
		config["max_num_batched_tokens"] = 2048
		config["max_model_len"] = 2048
		config["enforce_eager"] = true
		config["device"] = "cpu"
	}

	// Merge user args into config (user args override defaults except trust_remote_code)
	if len(userArgs) > 0 {
		userConfig := ConvertArgsToVLLMConfig(userArgs)
		for key, value := range userConfig {
			config[key] = value
		}
	}

	return config
}

// ConvertArgsToVLLMConfig converts command-line arguments to vLLM config parameters.
// Supports:
//   - Flat args: --key=value or --key value → {"key": "value"}
//   - Nested args with dot notation: --nested.key=value → {"nested": {"key": "value"}}
//   - JSON values: --nested='{"key": "value"}' → {"nested": {"key": "value"}}
//   - Bool flags: --flag → {"flag": true}
//   - Merging: Multiple definitions are merged, later values override earlier ones
//
// Examples:
//   - --dtype=float16 --max-model-len=4096
//     → {"dtype": "float16", "max_model_len": 4096}
//   - --compilation-config.mode=3
//     → {"compilation_config": {"mode": 3}}
//   - --compilation-config='{"mode": 1}' --compilation-config.mode=3
//     → {"compilation_config": {"mode": 3}}
func ConvertArgsToVLLMConfig(args []string) map[string]interface{} {
	config := make(map[string]interface{})

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			continue
		}

		var key, value string
		var isBool bool

		// Check if arg uses --key=value format
		if strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			key = strings.TrimPrefix(parts[0], "--")
			value = parts[1]
			isBool = false
		} else {
			// --key value or --flag format
			key = strings.TrimPrefix(arg, "--")

			// Check if this is a boolean flag (no value following)
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				isBool = true
			} else {
				value = args[i+1]
				i++ // Skip the value in next iteration
				isBool = false
			}
		}

		// Convert hyphens to underscores in all key parts (vLLM convention)
		key = strings.ReplaceAll(key, "-", "_")

		// Handle bool flags
		if isBool {
			config[key] = true
			continue
		}

		// Parse value: try simple types first, then JSON for complex structures
		var parsedValue interface{}

		// Check if this looks like JSON syntax (object or array)
		trimmedValue := strings.TrimSpace(value)
		isJSONSyntax := (strings.HasPrefix(trimmedValue, "{") && strings.HasSuffix(trimmedValue, "}")) ||
			(strings.HasPrefix(trimmedValue, "[") && strings.HasSuffix(trimmedValue, "]"))

		if isJSONSyntax {
			// Try to parse as JSON
			if err := json.Unmarshal([]byte(value), &parsedValue); err != nil {
				// Invalid JSON syntax, treat as string
				parsedValue = value
			}
		} else {
			// Try to parse as int, float, bool, or keep as string
			if intVal := tryParseInt(value); intVal != nil {
				parsedValue = *intVal
			} else if floatVal := tryParseFloat(value); floatVal != nil {
				parsedValue = *floatVal
			} else if value == "true" || value == "false" {
				parsedValue = value == "true"
			} else {
				parsedValue = value
			}
		}

		// Handle nested keys (dot notation)
		if strings.Contains(key, ".") {
			keys := strings.Split(key, ".")
			nestedMap := createNestedMap(keys, parsedValue)
			recursiveMerge(config, nestedMap)
		} else {
			// Root-level key
			if existingMap, ok := config[key].(map[string]interface{}); ok {
				// Existing value is a map, try to merge
				if parsedMap, ok := parsedValue.(map[string]interface{}); ok {
					recursiveMerge(existingMap, parsedMap)
				} else {
					// Can't merge non-map into map, overwrite
					config[key] = parsedValue
				}
			} else {
				// No existing value or existing value is not a map, just set it
				config[key] = parsedValue
			}
		}
	}

	return config
}

// tryParseInt attempts to parse a string as an integer
func tryParseInt(s string) *int {
	result, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &result
}

// tryParseFloat attempts to parse a string as a float
func tryParseFloat(s string) *float64 {
	result, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &result
}

// createNestedMap creates a nested map from a slice of keys and a value.
// Example: keys=["a", "b", "c"], value=1 → {"a": {"b": {"c": 1}}}
// This follows the same pattern as vLLM's create_nested_dict function.
func createNestedMap(keys []string, value interface{}) map[string]interface{} {
	if len(keys) == 0 {
		return nil
	}

	// Build from the innermost level outward
	var result interface{} = value
	for i := len(keys) - 1; i >= 0; i-- {
		result = map[string]interface{}{
			keys[i]: result,
		}
	}

	return result.(map[string]interface{})
}

// recursiveMerge recursively merges src into dst.
// When both src and dst have the same key:
//   - If both values are maps, merge recursively
//   - Otherwise, src value overwrites dst value
//
// This follows the same pattern as vLLM's recursive_dict_update function.
func recursiveMerge(dst, src map[string]interface{}) {
	for k, v := range src {
		if dstVal, exists := dst[k]; exists {
			// Key exists in both maps
			if dstMap, dstIsMap := dstVal.(map[string]interface{}); dstIsMap {
				if srcMap, srcIsMap := v.(map[string]interface{}); srcIsMap {
					// Both are maps, merge recursively
					recursiveMerge(dstMap, srcMap)
					continue
				}
			}
		}
		// Either key doesn't exist in dst, or one/both values are not maps
		// Overwrite with src value
		dst[k] = v
	}
}

// MarshalVLLMConfig marshals vLLM configuration map to YAML bytes
func MarshalVLLMConfig(config map[string]interface{}) ([]byte, error) {
	yamlData, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal vLLM config to YAML: %w", err)
	}
	return yamlData, nil
}

// GetVLLMConfigMapName returns the ConfigMap name for a given LLMEngine and model
// Each model gets its own ConfigMap to support multi-model deployments
func GetVLLMConfigMapName(engineName, modelName string) string {
	return fmt.Sprintf("vllm-%s-%s-config", engineName, modelName)
}

// BuildOllamaEnv returns environment variables for Ollama based on GPU request
func BuildOllamaEnv(llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository, requestGPU bool) []corev1.EnvVar {
	_, envPaths := GetStoragePaths(llmEngine, modelRepo)
	userEnv := llmEngine.Spec.Env

	// Common environment variables
	commonEnv := []corev1.EnvVar{
		{Name: "OLLAMA_HOST", Value: "0.0.0.0"},
		{Name: "OLLAMA_ORIGINS", Value: "*"},
		{Name: "OLLAMA_MAX_LOADED_MODELS", Value: "1"},
	}

	// Add environment paths (sorted for deterministic pod template hash)
	commonEnv = append(commonEnv, sortedEnvFromMap(envPaths)...)

	// Add CPU or GPU specific environment variables
	var specificEnv []corev1.EnvVar
	if requestGPU {
		// GPU-specific environment variables
		specificEnv = []corev1.EnvVar{
			{Name: "OLLAMA_NUM_PARALLEL", Value: "4"},
			{Name: "OLLAMA_FLASH_ATTENTION", Value: "1"},
			{Name: "CUDA_VISIBLE_DEVICES", Value: "0"},
			{Name: "OLLAMA_GPU_OVERHEAD", Value: "0.05"},
		}
	} else {
		// CPU-specific environment variables
		specificEnv = []corev1.EnvVar{
			{Name: "OLLAMA_NUM_PARALLEL", Value: "2"},
			{Name: "OLLAMA_NUM_GPU", Value: "0"},
			{Name: "OMP_NUM_THREADS", Value: "8"},
		}
	}

	// Merge with user env
	return MergeEnvVars(append(commonEnv, specificEnv...), userEnv)
}

// BuildVLLMEnv returns environment variables for vLLM based on GPU request
// User can override these of cause
// Assumes resources: CPU=8 cores, Memory=16GB, GPU=1 (if requested)
func BuildVLLMEnv(llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository, requestGPU bool) []corev1.EnvVar {
	_, envPaths := GetStoragePaths(llmEngine, modelRepo)
	userEnv := llmEngine.Spec.Env

	// Common environment variables (both GPU and CPU)
	commonEnv := []corev1.EnvVar{
		// Logging configuration
		{Name: "VLLM_LOGGING_LEVEL", Value: "WARNING"},
		// Worker multiprocessing method - spawn is safer for GPU workloads
		{Name: "VLLM_WORKER_MULTIPROC_METHOD", Value: "spawn"},
	}

	// Add HuggingFace token if provided (for downloading gated models)
	// vLLM uses HUGGING_FACE_HUB_TOKEN
	if modelRepo != nil && modelRepo.Spec.Source.HFTokenSecret != "" && modelRepo.Spec.Source.Origin == aitrigramv1.ModelOriginHuggingFace {
		key := modelRepo.Spec.Source.HFTokenSecretKey
		if key == "" {
			key = "HFToken" // Default key
		}
		commonEnv = append(commonEnv, corev1.EnvVar{
			Name: "HUGGING_FACE_HUB_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: modelRepo.Spec.Source.HFTokenSecret,
					},
					Key: key,
				},
			},
		})
	}

	// Add environment paths (model storage locations)
	for envName, path := range envPaths {
		commonEnv = append(commonEnv, corev1.EnvVar{
			Name:  envName,
			Value: path,
		})
	}

	// Add hardware-specific environment variables
	var specificEnv []corev1.EnvVar
	if requestGPU {
		// GPU-specific environment variables (single GPU assumed)
		// Note: VLLM_ATTENTION_BACKEND and VLLM_GPU_MEMORY_UTILIZATION are deprecated
		// and have been moved to the YAML config file (see BuildVLLMConfig)
		specificEnv = []corev1.EnvVar{
			// NCCL configuration for distributed operations
			{Name: "NCCL_LAUNCH_MODE", Value: "GROUP"},
			// PyTorch CUDA memory allocator configuration
			{Name: "PYTORCH_CUDA_ALLOC_CONF", Value: "expandable_segments:True,max_split_size_mb:512"},
			// Use first GPU only
			{Name: "CUDA_VISIBLE_DEVICES", Value: "0"},
		}
	} else {
		// CPU-specific environment variables (8 cores assumed)
		specificEnv = []corev1.EnvVar{
			// OpenMP threads for parallel CPU operations
			{Name: "OMP_NUM_THREADS", Value: "8"},
			// Intel MKL threads for optimized math operations
			{Name: "MKL_NUM_THREADS", Value: "8"},
		}
	}

	// Merge with user env
	return MergeEnvVars(append(commonEnv, specificEnv...), userEnv)
}

// GetPodPort returns the pod port for the LLMEngine
func GetPodPort(llmEngine *aitrigramv1.LLMEngine) int32 {
	if llmEngine.Spec.Port != 0 {
		return llmEngine.Spec.Port
	}

	switch llmEngine.Spec.EngineType {
	case aitrigramv1.LLMEngineTypeOllama:
		return 11434
	case aitrigramv1.LLMEngineTypeVLLM:
		return 8000
	default:
		return 8080
	}
}

// GetServicePort returns the service port for the LLMEngine
func GetServicePort(llmEngine *aitrigramv1.LLMEngine) int32 {
	if llmEngine.Spec.ServicePort != 0 {
		return llmEngine.Spec.ServicePort
	}
	return 8080
}

// BuildGPUResources builds resource requirements for GPU workloads
// Only returns GPU resources - CPU/memory should come from template
func BuildGPUResources(gpuConfig *aitrigramv1.GPUConfig) corev1.ResourceRequirements {
	gpuType := gpuConfig.Type
	if gpuType == "" {
		gpuType = DefaultGPUResourceType
	}

	gpuCount := gpuConfig.Count
	if gpuCount == 0 {
		gpuCount = 1
	}

	gpuResource := corev1.ResourceName(gpuType)
	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			gpuResource: *resource.NewQuantity(int64(gpuCount), resource.DecimalSI),
		},
		Requests: corev1.ResourceList{
			gpuResource: *resource.NewQuantity(int64(gpuCount), resource.DecimalSI),
		},
	}
}

// MergeResourceRequirements merges base and additional resource requirements
// Additional resources take precedence for overlapping resource types
func MergeResourceRequirements(base, additional corev1.ResourceRequirements) corev1.ResourceRequirements {
	result := corev1.ResourceRequirements{
		Limits:   make(corev1.ResourceList),
		Requests: make(corev1.ResourceList),
	}

	// Copy base limits
	for k, v := range base.Limits {
		result.Limits[k] = v
	}

	// Copy base requests
	for k, v := range base.Requests {
		result.Requests[k] = v
	}

	// Merge additional limits (overrides base)
	for k, v := range additional.Limits {
		result.Limits[k] = v
	}

	// Merge additional requests (overrides base)
	for k, v := range additional.Requests {
		result.Requests[k] = v
	}

	return result
}

// AddGPUCapabilities adds GPU-specific capabilities to an existing security context
// Modifies the security context in-place by adding SYS_ADMIN capability
func AddGPUCapabilities(securityContext *corev1.SecurityContext) {
	if securityContext == nil {
		return
	}

	// Initialize capabilities if not present
	if securityContext.Capabilities == nil {
		securityContext.Capabilities = &corev1.Capabilities{}
	}

	// Add SYS_ADMIN capability for GPU access
	securityContext.Capabilities.Add = append(securityContext.Capabilities.Add, "SYS_ADMIN")
}

// BuildGPUNodeSelector builds node selector for GPU workloads
func BuildGPUNodeSelector(gpuConfig *aitrigramv1.GPUConfig, llmEngine *aitrigramv1.LLMEngine) map[string]string {
	nodeSelector := make(map[string]string)

	// Start with general nodeSelector from spec
	if len(llmEngine.Spec.NodeSelector) > 0 {
		for k, v := range llmEngine.Spec.NodeSelector {
			nodeSelector[k] = v
		}
	}

	// Add GPU-specific node selectors (override general selectors)
	if len(gpuConfig.NodeSelector) > 0 {
		for k, v := range gpuConfig.NodeSelector {
			nodeSelector[k] = v
		}
	}

	// Add default GPU node selector (lowest priority)
	defaultNodeSelector := GetDefaultGPUNodeSelector(gpuConfig.Type)
	for k, v := range defaultNodeSelector {
		if _, exists := nodeSelector[k]; !exists {
			nodeSelector[k] = v
		}
	}

	return nodeSelector
}

// BuildGPUTolerations builds tolerations for GPU workloads
func BuildGPUTolerations(gpuConfig *aitrigramv1.GPUConfig) []corev1.Toleration {
	if len(gpuConfig.Tolerations) > 0 {
		return gpuConfig.Tolerations
	}
	return GetDefaultGPUTolerations(gpuConfig.Type)
}

// sortedEnvFromMap converts a map of env var names to values into a sorted slice
// of EnvVar. Sorting ensures deterministic pod template hashes across reconciles.
func sortedEnvFromMap(envPaths map[string]string) []corev1.EnvVar {
	keys := make([]string, 0, len(envPaths))
	for k := range envPaths {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	envVars := make([]corev1.EnvVar, 0, len(keys))
	for _, k := range keys {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: envPaths[k]})
	}
	return envVars
}

// MergeEnvVars merges default and user environment variables, with user vars taking precedence
func MergeEnvVars(defaultEnv, userEnv []corev1.EnvVar) []corev1.EnvVar {
	// Create a map of user-provided env var names for quick lookup
	userEnvMap := make(map[string]bool)
	for _, env := range userEnv {
		userEnvMap[env.Name] = true
	}

	// Start with user env vars
	result := make([]corev1.EnvVar, len(userEnv))
	copy(result, userEnv)

	// Add default env vars that aren't overridden by user
	for _, env := range defaultEnv {
		if !userEnvMap[env.Name] {
			result = append(result, env)
		}
	}

	return result
}

// GetDefaultGPUNodeSelector returns the default node selector based on GPU type
// Returns nil if no default selector should be applied
func GetDefaultGPUNodeSelector(gpuType string) map[string]string {
	if gpuType == "" {
		gpuType = DefaultGPUResourceType
	}

	switch gpuType {
	case DefaultGPUResourceType:
		// For NVIDIA GPUs, use the common gpu.present label
		return map[string]string{
			DefaultGPUResourceType + ".present": "true",
		}
	case "amd.com/gpu":
		// For AMD GPUs, use AMD-specific label
		return map[string]string{
			"amd.com/gpu.present": "true",
		}
	default:
		// For other GPU types, don't set a default selector
		// Users should specify their own node selector
		return nil
	}
}

// GetDefaultGPUTolerations returns the default tolerations based on GPU type
// Returns nil if no default tolerations should be applied
func GetDefaultGPUTolerations(gpuType string) []corev1.Toleration {
	if gpuType == "" {
		gpuType = DefaultGPUResourceType
	}

	switch gpuType {
	case DefaultGPUResourceType:
		// Common NVIDIA GPU toleration
		return []corev1.Toleration{
			{
				Key:      DefaultGPUResourceType,
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			},
		}
	case "amd.com/gpu":
		// Common AMD GPU toleration
		return []corev1.Toleration{
			{
				Key:      "amd.com/gpu",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			},
		}
	default:
		// For other GPU types, don't set default tolerations
		// Users should specify their own tolerations
		return nil
	}
}
