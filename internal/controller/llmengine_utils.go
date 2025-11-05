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
	"fmt"

	corev1 "k8s.io/api/core/v1"

	aitrigramv1 "github.com/gaol/AITrigram/api/v1"
)

// Production-ready storage paths for different model origins and cache types
const (
	// Model storage paths (shared, read-only for LLMEngine)
	DefaultModelStoragePath = "/data/models"

	// HuggingFace cache paths (per-pod, not shared)
	HFHomePath            = "/data/huggingface"
	TransformersCachePath = "/data/huggingface"

	// vLLM cache paths (per-pod, not shared)
	VLLMKVCachePath = "/data/vllm_kv_cache"

	// Ollama cache paths (per-pod, not shared)
	OllamaModelsPath  = "/data/models" // Same as model storage for Ollama
	OllamaCachePath   = "/data/ollama_cache"
	OllamaKVCachePath = "/data/ollama_kv_cache"
	OllamaTmpDirPath  = "/data/ollama_cache" // Use cache dir as tmpdir

	// Images for serving, downloading, and maybe deleting models
	DefaultVLLMImage   = "vllm/vllm-openai:latest"
	DefaultOllamaImage = "ollama/ollama:latest"
)

// StoragePaths defines the storage configuration for an engine
type StoragePaths struct {
	// ModelPath is the shared model storage path (read-only for engine)
	ModelPath string
	// CachePaths contains various cache directories (per-pod, not shared)
	CachePaths map[string]string
}

// GetStoragePaths returns production-ready storage paths for a given engine type
func GetStoragePaths(engineType aitrigramv1.LLMEngineType) StoragePaths {
	switch engineType {
	case aitrigramv1.LLMEngineTypeVLLM:
		return StoragePaths{
			ModelPath: DefaultModelStoragePath,
			CachePaths: map[string]string{
				"HF_HOME":            HFHomePath,
				"TRANSFORMERS_CACHE": TransformersCachePath,
				"VLLM_KV_CACHE_DIR":  VLLMKVCachePath,
			},
		}
	case aitrigramv1.LLMEngineTypeOllama:
		return StoragePaths{
			ModelPath: DefaultModelStoragePath,
			CachePaths: map[string]string{
				"OLLAMA_MODELS":       OllamaModelsPath,
				"OLLAMA_CACHE":        OllamaCachePath,
				"OLLAMA_KV_CACHE_DIR": OllamaKVCachePath,
				"OLLAMA_TMPDIR":       OllamaTmpDirPath,
			},
		}
	default:
		return StoragePaths{
			ModelPath:  DefaultModelStoragePath,
			CachePaths: map[string]string{},
		}
	}
}

// buildCacheVolumes creates cache volume mounts and volumes for an engine
// Cache volumes are per-pod and not shared between pods
func buildCacheVolumes(engineType aitrigramv1.LLMEngineType) ([]corev1.VolumeMount, []corev1.Volume) {
	var volumeMounts []corev1.VolumeMount
	var volumes []corev1.Volume

	// Create cache volumes based on engine type
	switch engineType {
	case aitrigramv1.LLMEngineTypeVLLM:
		// HuggingFace cache (shared location for HF_HOME and TRANSFORMERS_CACHE)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "hf-cache",
			MountPath: HFHomePath,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "hf-cache",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: nil, // No limit by default
				},
			},
		})

		// vLLM KV cache
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "vllm-kv-cache",
			MountPath: VLLMKVCachePath,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "vllm-kv-cache",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: nil,
				},
			},
		})

	case aitrigramv1.LLMEngineTypeOllama:
		// Ollama cache
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "ollama-cache",
			MountPath: OllamaCachePath,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "ollama-cache",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: nil,
				},
			},
		})

		// Ollama KV cache
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "ollama-kv-cache",
			MountPath: OllamaKVCachePath,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "ollama-kv-cache",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: nil,
				},
			},
		})
	}

	return volumeMounts, volumes
}

// buildVLLMArgs constructs vLLM arguments based on GPU availability
func buildVLLMArgs(modelPath string, port int32, hasGPU bool) []string {
	if hasGPU {
		// GPU-optimized vLLM arguments
		return []string{
			"--host", "0.0.0.0",
			"--port", fmt.Sprintf("%d", port),
			"--model", modelPath,
			"--dtype", "float8_e5m2",
			"--max-num-batched-tokens", "32768",
			"--max-model-len", "8192",
			"--enforce-eager",
		}
	}

	// CPU-only vLLM arguments
	return []string{
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", port),
		"--model", modelPath,
		"--dtype", "half",
		"--max-num-batched-tokens", "2048",
		"--max-model-len", "2048",
		"--enforce-eager",
		"--device", "cpu",
	}
}

// buildVLLMEnv constructs vLLM environment variables based on GPU availability
func buildVLLMEnv(hasGPU bool, userEnv []corev1.EnvVar) []corev1.EnvVar {
	storagePaths := GetStoragePaths(aitrigramv1.LLMEngineTypeVLLM)

	// Initial environment variables (common to both GPU and CPU)
	initialEnv := []corev1.EnvVar{
		{Name: "VLLM_LOGGING_LEVEL", Value: "warning"},
		{Name: "VLLM_USE_RAY", Value: "0"},
		{Name: "VLLM_WORKER_MULTIPROC_METHOD", Value: "spawn"},
	}

	// Add cache paths
	for envName, path := range storagePaths.CachePaths {
		initialEnv = append(initialEnv, corev1.EnvVar{
			Name:  envName,
			Value: path,
		})
	}

	var modeSpecificEnv []corev1.EnvVar
	if hasGPU {
		// GPU-specific environment variables
		modeSpecificEnv = []corev1.EnvVar{
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
		modeSpecificEnv = []corev1.EnvVar{
			{Name: "OMP_NUM_THREADS", Value: "8"},
			{Name: "MKL_NUM_THREADS", Value: "8"},
		}
	}

	// Combine initial + mode-specific + user env vars
	defaultEnv := append(initialEnv, modeSpecificEnv...)
	return mergeEnvVars(defaultEnv, userEnv)
}

// buildOllamaArgs constructs Ollama arguments (minimal, most config via env vars)
func buildOllamaArgs() []string {
	// Ollama uses environment variables for most configuration
	// The serve command starts the Ollama server
	return []string{"serve"}
}

// buildOllamaEnv constructs Ollama environment variables based on GPU availability
func buildOllamaEnv(hasGPU bool, userEnv []corev1.EnvVar) []corev1.EnvVar {
	storagePaths := GetStoragePaths(aitrigramv1.LLMEngineTypeOllama)

	// Initial environment variables (common to both GPU and CPU)
	initialEnv := []corev1.EnvVar{
		{Name: "OLLAMA_HOST", Value: "0.0.0.0"},
		{Name: "OLLAMA_ORIGINS", Value: "*"},
		{Name: "OLLAMA_MAX_LOADED_MODELS", Value: "1"},
	}

	// Add cache paths
	for envName, path := range storagePaths.CachePaths {
		initialEnv = append(initialEnv, corev1.EnvVar{
			Name:  envName,
			Value: path,
		})
	}

	var modeSpecificEnv []corev1.EnvVar
	if hasGPU {
		// GPU-specific environment variables for Ollama
		modeSpecificEnv = []corev1.EnvVar{
			{Name: "OLLAMA_NUM_PARALLEL", Value: "4"},
			{Name: "OLLAMA_FLASH_ATTENTION", Value: "1"},
			{Name: "CUDA_VISIBLE_DEVICES", Value: "0"},
			{Name: "OLLAMA_GPU_OVERHEAD", Value: "0.05"},
		}
	} else {
		// CPU-specific environment variables for Ollama
		modeSpecificEnv = []corev1.EnvVar{
			{Name: "OLLAMA_NUM_PARALLEL", Value: "2"},
			{Name: "OLLAMA_NUM_GPU", Value: "0"},
			{Name: "OMP_NUM_THREADS", Value: "8"},
		}
	}

	// Combine initial + mode-specific + user env vars
	defaultEnv := append(initialEnv, modeSpecificEnv...)
	return mergeEnvVars(defaultEnv, userEnv)
}

// mergeEnvVars merges default and user environment variables, with user vars taking precedence
func mergeEnvVars(defaultEnv, userEnv []corev1.EnvVar) []corev1.EnvVar {
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

// buildHuggingFaceDownloadScript returns the default HuggingFace download script
func buildHuggingFaceDownloadScript() string {
	return `#!/usr/bin/env python3
import os
from huggingface_hub import snapshot_download

model_id = "{{ ModelId }}"
model_name = "{{ ModelName }}"
mount_path = "{{ MountPath }}"
target_path = os.path.join(mount_path, model_name)

print(f"Downloading model {model_id} to {target_path}...")

# Check if HF_TOKEN is available in environment
hf_token = os.environ.get("HF_TOKEN")

try:
    snapshot_download(
        repo_id=model_id,
        local_dir=target_path,
        local_dir_use_symlinks=False,
        token=hf_token,
    )
    print("Download completed successfully")
except Exception as e:
    print(f"Error downloading model: {e}")
    exit(1)
`
}

// buildDefaultDownloadScript returns the default download script based on the model origin
func buildDefaultDownloadScript(origin aitrigramv1.ModelOrigin) string {
	switch origin {
	case aitrigramv1.ModelOriginHuggingFace:
		return buildHuggingFaceDownloadScript()
	case aitrigramv1.ModelOriginOllama:
		// Bash script using ollama
		return `#!/bin/bash
set -e
echo "Pulling model {{ ModelId }} using ollama..."
ollama pull {{ ModelId }}
echo "Copying model to {{ MountPath }}/{{ ModelName }}..."
cp -r ~/.ollama/models/{{ ModelId }} {{ MountPath }}/{{ ModelName }}
echo "Model copied successfully"
`
	case aitrigramv1.ModelOriginGGUF:
		// Bash script using curl
		return `#!/bin/bash
set -e
echo "Downloading GGUF model {{ ModelId }} to {{ MountPath }}/{{ ModelName }}..."
mkdir -p {{ MountPath }}/{{ ModelName }}
curl -L {{ ModelId }} -o {{ MountPath }}/{{ ModelName }}/model.gguf
echo "Download completed successfully"
`
	case aitrigramv1.ModelOriginLocal:
		// Simple bash script for local models
		return `#!/bin/bash
echo "Local model source - no download needed"
echo "Model should already be available at {{ MountPath }}"
ls -la {{ MountPath }}
`
	default:
		// Default to HuggingFace Python script
		return buildHuggingFaceDownloadScript()
	}
}

// buildDefaultDeleteScript returns the default delete script based on the model origin
func buildDefaultDeleteScript(origin aitrigramv1.ModelOrigin) string {
	switch origin {
	case aitrigramv1.ModelOriginHuggingFace:
		// Bash script to remove HuggingFace model directory
		return `#!/bin/bash
set -e
echo "Deleting HuggingFace model {{ ModelName }} from {{ MountPath }}..."
if [ -d "{{ MountPath }}/{{ ModelName }}" ]; then
  rm -rf {{ MountPath }}/{{ ModelName }}
  echo "Model directory {{ MountPath }}/{{ ModelName }} deleted successfully"
else
  echo "Model directory {{ MountPath }}/{{ ModelName }} not found, nothing to delete"
fi
`
	case aitrigramv1.ModelOriginOllama:
		// Bash script using ollama rm command
		return `#!/bin/bash
set -e
echo "Deleting Ollama model {{ ModelId }}..."
if ollama list | grep -q "{{ ModelId }}"; then
  ollama rm {{ ModelId }}
  echo "Ollama model {{ ModelId }} deleted successfully"
else
  echo "Ollama model {{ ModelId }} not found, nothing to delete"
fi

# Also clean up local storage if exists
if [ -d "{{ MountPath }}/{{ ModelName }}" ]; then
  echo "Removing model files from {{ MountPath }}/{{ ModelName }}..."
  rm -rf {{ MountPath }}/{{ ModelName }}
  echo "Model files deleted"
fi
`
	case aitrigramv1.ModelOriginGGUF:
		// Bash script to remove GGUF model directory
		return `#!/bin/bash
set -e
echo "Deleting GGUF model {{ ModelName }} from {{ MountPath }}..."
if [ -d "{{ MountPath }}/{{ ModelName }}" ]; then
  rm -rf {{ MountPath }}/{{ ModelName }}
  echo "Model directory {{ MountPath }}/{{ ModelName }} deleted successfully"
else
  echo "Model directory {{ MountPath }}/{{ ModelName }} not found, nothing to delete"
fi
`
	case aitrigramv1.ModelOriginLocal:
		// For local models, just log - don't delete by default
		return `#!/bin/bash
echo "Local model source - skipping deletion of {{ MountPath }}/{{ ModelName }}"
echo "If you want to delete local models, provide custom deleteScripts"
ls -la {{ MountPath }} || true
`
	default:
		// Default to removing directory
		return `#!/bin/bash
set -e
echo "Deleting model {{ ModelName }} from {{ MountPath }}..."
if [ -d "{{ MountPath }}/{{ ModelName }}" ]; then
  rm -rf {{ MountPath }}/{{ ModelName }}
  echo "Model directory {{ MountPath }}/{{ ModelName }} deleted successfully"
else
  echo "Model directory {{ MountPath }}/{{ ModelName }} not found, nothing to delete"
fi
`
	}
}

// getDefaultGPUNodeSelector returns the default node selector based on GPU type
// Returns nil if no default selector should be applied
func getDefaultGPUNodeSelector(gpuType string) map[string]string {
	if gpuType == "" {
		gpuType = "nvidia.com/gpu"
	}

	switch gpuType {
	case "nvidia.com/gpu":
		// For NVIDIA GPUs, use the common gpu.present label
		return map[string]string{
			"nvidia.com/gpu.present": "true",
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

// getDefaultGPUTolerations returns the default tolerations based on GPU type
// Returns nil if no default tolerations should be applied
func getDefaultGPUTolerations(gpuType string) []corev1.Toleration {
	if gpuType == "" {
		gpuType = "nvidia.com/gpu"
	}

	switch gpuType {
	case "nvidia.com/gpu":
		// Common NVIDIA GPU toleration
		return []corev1.Toleration{
			{
				Key:      "nvidia.com/gpu",
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
