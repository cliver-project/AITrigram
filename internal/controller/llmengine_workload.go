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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

// LLMEngineWorkload holds all data needed for LLM inference deployment
type LLMEngineWorkload struct {
	// Image is the container image for the LLM engine
	Image string

	// PodPort is the port the LLM engine listens on inside the container
	PodPort int32

	// ServicePort is the port exposed by the Kubernetes service
	ServicePort int32

	// Storage contains volumes and volumeMounts for model and cache storage
	Storage StorageConfig

	// Envs contains environment variables (CPU or GPU scenarios)
	Envs []corev1.EnvVar

	// Args contains container arguments (CPU or GPU scenarios)
	Args []string

	// RequestGPU indicates whether GPU is requested
	RequestGPU bool

	// NodeSelector for scheduling (includes GPU node selectors if GPU enabled)
	NodeSelector map[string]string

	// Tolerations for scheduling (includes GPU tolerations if GPU enabled)
	Tolerations []corev1.Toleration

	// Resources for GPU/CPU requirements
	Resources corev1.ResourceRequirements

	// SecurityContext for GPU access
	SecurityContext *corev1.SecurityContext
}

// StorageConfig holds volume and volumeMount configurations
type StorageConfig struct {
	// Volumes contains the list of volumes
	Volumes []corev1.Volume

	// VolumeMounts contains the list of volume mounts
	VolumeMounts []corev1.VolumeMount
}

// BuildLLMEngineWorkload constructs an LLMEngineWorkload for a given LLMEngine and ModelRepository
func BuildLLMEngineWorkload(llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository) (*LLMEngineWorkload, error) {
	workload := &LLMEngineWorkload{}

	// Determine image
	workload.Image = llmEngine.Spec.Image
	if workload.Image == "" {
		switch llmEngine.Spec.EngineType {
		case aitrigramv1.LLMEngineTypeOllama:
			workload.Image = DefaultOllamaImage
		case aitrigramv1.LLMEngineTypeVLLM:
			workload.Image = DefaultVLLMImage
		default:
			workload.Image = DefaultVLLMImage
		}
	}

	// Determine pod port
	workload.PodPort = llmEngine.Spec.Port
	if workload.PodPort == 0 {
		switch llmEngine.Spec.EngineType {
		case aitrigramv1.LLMEngineTypeOllama:
			workload.PodPort = 11434
		case aitrigramv1.LLMEngineTypeVLLM:
			workload.PodPort = 8000
		default:
			workload.PodPort = 8080
		}
	}

	// Determine service port
	workload.ServicePort = llmEngine.Spec.ServicePort
	if workload.ServicePort == 0 {
		workload.ServicePort = 8080
	}

	// Build storage paths (includes cache paths respecting custom cache configuration)
	storagePaths := GetStoragePaths(llmEngine, modelRepo)

	// Build storage configuration with model storage (read-only)
	volumeSource, err := getStorageVolumeSource(&modelRepo.Spec.Storage)
	if err != nil {
		return nil, fmt.Errorf("failed to get storage volume source: %w", err)
	}

	workload.Storage = StorageConfig{
		Volumes: []corev1.Volume{
			{
				Name:         "model-storage",
				VolumeSource: volumeSource,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "model-storage",
				MountPath: storagePaths.ModelPath,
				ReadOnly:  true,
			},
		},
	}

	// Add cache volumes (respects custom cache configuration from spec)
	cacheVolumeMounts, cacheVolumes, _ := buildCacheVolumes(llmEngine)
	workload.Storage.VolumeMounts = append(workload.Storage.VolumeMounts, cacheVolumeMounts...)
	workload.Storage.Volumes = append(workload.Storage.Volumes, cacheVolumes...)

	// Build model path
	modelPath := fmt.Sprintf("%s/%s", storagePaths.ModelPath, modelRepo.Name)

	// Check if GPU is enabled
	requestGPU := detectGPURequest(llmEngine)
	workload.RequestGPU = requestGPU

	// Build environment variables and args based on engine type
	switch llmEngine.Spec.EngineType {
	case aitrigramv1.LLMEngineTypeOllama:
		workload.Envs = buildOllamaEnv(llmEngine, modelRepo, requestGPU)
		workload.Args = buildOllamaArgs()
	case aitrigramv1.LLMEngineTypeVLLM:
		workload.Envs = buildVLLMEnv(llmEngine, modelRepo, requestGPU)
		workload.Args = buildVLLMArgs(modelPath, workload.PodPort, requestGPU)
	default:
		workload.Envs = buildVLLMEnv(llmEngine, modelRepo, requestGPU)
		workload.Args = buildVLLMArgs(modelPath, workload.PodPort, requestGPU)
	}

	// Merge user-provided args if specified
	if len(llmEngine.Spec.Args) > 0 {
		workload.Args = llmEngine.Spec.Args
	}

	// Build resource requirements and security context
	if requestGPU {
		// GPU configuration
		gpuConfig := llmEngine.Spec.GPU
		gpuType := gpuConfig.Type
		if gpuType == "" {
			gpuType = "nvidia.com/gpu"
		}

		gpuCount := gpuConfig.Count
		if gpuCount == 0 {
			gpuCount = 1
		}

		// Build GPU resources
		workload.Resources = corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceName(gpuType): *resource.NewQuantity(int64(gpuCount), resource.DecimalSI),
				corev1.ResourceMemory:        *resource.NewQuantity(16*1024*1024*1024, resource.BinarySI), // 16Gi
				corev1.ResourceCPU:           *resource.NewQuantity(4, resource.DecimalSI),                // 4 cores
			},
			Requests: corev1.ResourceList{
				corev1.ResourceName(gpuType): *resource.NewQuantity(int64(gpuCount), resource.DecimalSI),
				corev1.ResourceMemory:        *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI), // 8Gi
				corev1.ResourceCPU:           *resource.NewQuantity(2, resource.DecimalSI),               // 2 cores
			},
		}

		// Build security context for GPU
		privileged := false
		workload.SecurityContext = &corev1.SecurityContext{
			Privileged: &privileged,
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{"SYS_ADMIN"},
			},
		}

		// Build node selector - start with general nodeSelector from spec
		workload.NodeSelector = make(map[string]string)
		if len(llmEngine.Spec.NodeSelector) > 0 {
			for k, v := range llmEngine.Spec.NodeSelector {
				workload.NodeSelector[k] = v
			}
		}

		// Add GPU-specific node selectors (these override general selectors if there's a conflict)
		if len(gpuConfig.NodeSelector) > 0 {
			for k, v := range gpuConfig.NodeSelector {
				workload.NodeSelector[k] = v
			}
		}

		// Add default GPU node selector (lowest priority, only if not already set)
		defaultNodeSelector := getDefaultGPUNodeSelector(gpuConfig.Type)
		if defaultNodeSelector != nil {
			for k, v := range defaultNodeSelector {
				if _, exists := workload.NodeSelector[k]; !exists {
					workload.NodeSelector[k] = v
				}
			}
		}

		// Build tolerations
		if len(gpuConfig.Tolerations) > 0 {
			workload.Tolerations = gpuConfig.Tolerations
		} else {
			workload.Tolerations = getDefaultGPUTolerations(gpuConfig.Type)
		}
	} else {
		// CPU-only configuration
		// Set reasonable CPU and memory requests/limits for LLM inference
		workload.Resources = corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI), // 8Gi
				corev1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),               // 4 cores
			},
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI), // 4Gi
				corev1.ResourceCPU:    *resource.NewQuantity(2, resource.DecimalSI),               // 2 cores
			},
		}

		// Build security context for CPU (more restrictive, no special capabilities needed)
		runAsNonRoot := true
		allowPrivilegeEscalation := false
		workload.SecurityContext = &corev1.SecurityContext{
			RunAsNonRoot:             &runAsNonRoot,
			AllowPrivilegeEscalation: &allowPrivilegeEscalation,
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		}

		// Apply general nodeSelector for CPU-only deployments
		if len(llmEngine.Spec.NodeSelector) > 0 {
			workload.NodeSelector = llmEngine.Spec.NodeSelector
		}
	}

	return workload, nil
}

// buildOllamaEnv returns environment variables for Ollama based on GPU request
func buildOllamaEnv(llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository, requestGPU bool) []corev1.EnvVar {
	storagePaths := GetStoragePaths(llmEngine, modelRepo)
	userEnv := llmEngine.Spec.Env

	// Common environment variables
	commonEnv := []corev1.EnvVar{
		{Name: "OLLAMA_HOST", Value: "0.0.0.0"},
		{Name: "OLLAMA_ORIGINS", Value: "*"},
		{Name: "OLLAMA_MAX_LOADED_MODELS", Value: "1"},
	}

	// Add cache paths
	for envName, path := range storagePaths.CachePaths {
		commonEnv = append(commonEnv, corev1.EnvVar{
			Name:  envName,
			Value: path,
		})
	}

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
	return mergeEnvVars(append(commonEnv, specificEnv...), userEnv)
}

// buildVLLMEnv returns environment variables for vLLM based on GPU request
func buildVLLMEnv(llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository, requestGPU bool) []corev1.EnvVar {
	storagePaths := GetStoragePaths(llmEngine, modelRepo)
	userEnv := llmEngine.Spec.Env

	// Common environment variables
	commonEnv := []corev1.EnvVar{
		{Name: "VLLM_LOGGING_LEVEL", Value: "warning"},
		{Name: "VLLM_USE_RAY", Value: "0"},
		{Name: "VLLM_WORKER_MULTIPROC_METHOD", Value: "spawn"},
	}

	// Add HuggingFace token if provided (for downloading gated models)
	// vLLM uses HUGGING_FACE_HUB_TOKEN
	if modelRepo != nil && modelRepo.Spec.Source.HFTokenSecretRef != nil && modelRepo.Spec.Source.Origin == aitrigramv1.ModelOriginHuggingFace {
		commonEnv = append(commonEnv, corev1.EnvVar{
			Name: "HUGGING_FACE_HUB_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: modelRepo.Spec.Source.HFTokenSecretRef,
			},
		})
	}

	// Add cache paths
	for envName, path := range storagePaths.CachePaths {
		commonEnv = append(commonEnv, corev1.EnvVar{
			Name:  envName,
			Value: path,
		})
	}

	// Add CPU or GPU specific environment variables
	var specificEnv []corev1.EnvVar
	if requestGPU {
		// GPU-specific environment variables
		specificEnv = []corev1.EnvVar{
			{Name: "VLLM_ATTENTION_BACKEND", Value: "flash-attn"},
			{Name: "VLLM_GPU_MEMORY_UTILIZATION", Value: "0.95"},
			{Name: "TORCH_CUDNN_V8_API_ENABLED", Value: "1"},
			{Name: "NCCL_P2P_DISABLE", Value: "1"},
			{Name: "NCCL_LAUNCH_MODE", Value: "GROUP"},
			{Name: "PYTORCH_CUDA_ALLOC_CONF", Value: "expandable_segments:True,max_split_size_mb:512"},
			{Name: "CUDA_VISIBLE_DEVICES", Value: "0"},
		}
	} else {
		// CPU-specific environment variables
		specificEnv = []corev1.EnvVar{
			{Name: "OMP_NUM_THREADS", Value: "8"},
			{Name: "MKL_NUM_THREADS", Value: "8"},
		}
	}

	// Merge with user env
	return mergeEnvVars(append(commonEnv, specificEnv...), userEnv)
}
