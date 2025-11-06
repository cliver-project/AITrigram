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
	"k8s.io/apimachinery/pkg/api/resource"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

// Production-ready storage paths for different model origins and cache types
const (
	// Model storage paths (shared across pods, mounted from PVC/NFS)
	DefaultModelStoragePath = "/data/models"

	// HuggingFace paths
	// HF_HOME is shared storage where models are downloaded (same as model storage for HF models)
	// This should point to the ModelRepository storage path for HuggingFace models
	// Note: HF_HOME will be set dynamically to ModelRepository storage path

	// HF_HUB_CACHE is per-pod cache for HuggingFace hub metadata and temp files
	// This improves performance and avoids lock contention between pods
	HFHubCachePath = "/var/cache/huggingface/hub"

	// vLLM paths
	// Per-pod local caches for vLLM runtime data (not shared)
	VLLMCacheDir        = "/var/cache/vllm"             // Local runtime cache
	VLLMWeightsCacheDir = "/var/lib/vllm/weights-cache" // Local weights cache

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

// GetStoragePaths returns production-ready storage paths for a given LLMEngine and model repository
// The ModelPath is taken from the ModelRepository's storage path to ensure consistency
// between where the model was downloaded and where the engine will look for it
// Cache paths are determined by buildCacheVolumes, respecting custom cache configuration
func GetStoragePaths(llmEngine *aitrigramv1.LLMEngine, modelRepo *aitrigramv1.ModelRepository) StoragePaths {
	// Use the ModelRepository's storage path as the model path
	// This ensures the LLMEngine mounts at the same location where models were downloaded
	modelPath := DefaultModelStoragePath
	if modelRepo != nil && modelRepo.Spec.Storage.Path != "" {
		modelPath = modelRepo.Spec.Storage.Path
	}

	// Get cache paths from buildCacheVolumes which respects custom cache configuration
	_, _, cachePaths := buildCacheVolumes(llmEngine)

	// Add engine-specific model paths
	switch llmEngine.Spec.EngineType {
	case aitrigramv1.LLMEngineTypeVLLM:
		// For HuggingFace models used with vLLM:
		// HF_HOME points to the shared model storage where HF models are downloaded
		// This is the same as the ModelRepository storage path
		if modelRepo != nil && modelRepo.Spec.Source.Origin == aitrigramv1.ModelOriginHuggingFace {
			cachePaths["HF_HOME"] = modelPath
			// HF_HUB_CACHE is already set by buildCacheVolumes to a per-pod path
			// Don't override it here
		}
	case aitrigramv1.LLMEngineTypeOllama:
		// For Ollama, OLLAMA_MODELS points to model storage
		cachePaths["OLLAMA_MODELS"] = modelPath
	}

	return StoragePaths{
		ModelPath:  modelPath,
		CachePaths: cachePaths,
	}
}

// detectGPURequest checks if GPU resources are requested in the LLMEngine spec
// Returns true if GPU resources are requested, false otherwise
func detectGPURequest(llmEngine *aitrigramv1.LLMEngine) bool {
	return llmEngine.Spec.GPU != nil && llmEngine.Spec.GPU.Enabled
}

// buildCacheVolumes creates cache volume mounts and volumes for an engine
// For vLLM/HuggingFace: Creates per-pod local caches for runtime data
// For Ollama: Creates per-pod caches
// If llmEngine.Spec.Cache is specified, it can be used for custom cache configuration
// Returns volume mounts, volumes, and updated cache paths for environment variables
func buildCacheVolumes(llmEngine *aitrigramv1.LLMEngine) ([]corev1.VolumeMount, []corev1.Volume, map[string]string) {
	var volumeMounts []corev1.VolumeMount
	var volumes []corev1.Volume
	cachePaths := make(map[string]string)

	engineType := llmEngine.Spec.EngineType
	customCache := llmEngine.Spec.Cache

	// Create cache volumes based on engine type
	switch engineType {
	case aitrigramv1.LLMEngineTypeVLLM:
		// vLLM local cache directory (per-pod, for runtime data)
		if customCache != nil {
			// Use custom cache for vLLM cache directory
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "vllm-cache",
				MountPath: customCache.Path,
			})
			volumes = append(volumes, corev1.Volume{
				Name:         "vllm-cache",
				VolumeSource: customCache.VolumeSource,
			})
			cachePaths["VLLM_CACHE_DIR"] = customCache.Path
		} else {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "vllm-cache",
				MountPath: VLLMCacheDir,
			})
			volumes = append(volumes, corev1.Volume{
				Name: "vllm-cache",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						SizeLimit: nil,
					},
				},
			})
			cachePaths["VLLM_CACHE_DIR"] = VLLMCacheDir
		}

		// vLLM weights cache (always per-pod EmptyDir)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "vllm-weights-cache",
			MountPath: VLLMWeightsCacheDir,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "vllm-weights-cache",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: nil,
				},
			},
		})
		cachePaths["VLLM_WEIGHTS_CACHE_DIR"] = VLLMWeightsCacheDir

		// HuggingFace hub cache (per-pod EmptyDir for metadata and temp files)
		// This avoids lock contention between pods
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "hf-hub-cache",
			MountPath: HFHubCachePath,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "hf-hub-cache",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: nil,
				},
			},
		})
		cachePaths["HF_HUB_CACHE"] = HFHubCachePath

		// Shared memory for tensor parallel inference
		// vLLM needs access to shared memory for multi-GPU tensor parallel operations
		sizeLimit := resource.MustParse("2Gi")
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "shm",
			MountPath: "/dev/shm",
		})
		volumes = append(volumes, corev1.Volume{
			Name: "shm",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium:    corev1.StorageMediumMemory,
					SizeLimit: &sizeLimit,
				},
			},
		})

	case aitrigramv1.LLMEngineTypeOllama:
		// Ollama general cache (always EmptyDir - used for temporary files)
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
		cachePaths["OLLAMA_CACHE"] = OllamaCachePath
		cachePaths["OLLAMA_TMPDIR"] = OllamaTmpDirPath

		// Ollama KV cache - use custom cache if specified, otherwise EmptyDir
		if customCache != nil {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "kv-cache",
				MountPath: customCache.Path,
			})
			volumes = append(volumes, corev1.Volume{
				Name:         "kv-cache",
				VolumeSource: customCache.VolumeSource,
			})
			cachePaths["OLLAMA_KV_CACHE_DIR"] = customCache.Path
		} else {
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
			cachePaths["OLLAMA_KV_CACHE_DIR"] = OllamaKVCachePath
		}
	}

	return volumeMounts, volumes, cachePaths
}

// buildVLLMArgs constructs vLLM arguments based on GPU availability
func buildVLLMArgs(modelPath string, port int32, requestGPU bool) []string {
	if requestGPU {
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

// buildOllamaArgs constructs Ollama arguments (minimal, most config via env vars)
func buildOllamaArgs() []string {
	// Ollama uses environment variables for most configuration
	// The serve command starts the Ollama server
	return []string{"serve"}
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
hf_token = os.environ.get("HF_TOKEN", "")

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
		// Note: ollama pull requires the ollama server to be running
		return `#!/bin/sh
set -e

# Start ollama server in the background
echo "Starting ollama server..."
ollama serve > /tmp/ollama.log 2>&1 &
OLLAMA_PID=$!

# Wait for ollama server to be ready
echo "Waiting for ollama server to be ready..."
for i in $(seq 1 30); do
  if ollama list > /dev/null 2>&1; then
    echo "Ollama server is ready"
    break
  fi
  if [ $i -eq 30 ]; then
    echo "Timeout waiting for ollama server to start"
    cat /tmp/ollama.log
    exit 1
  fi
  sleep 1
done

# Pull the model
echo "Pulling model {{ ModelId }} using ollama..."
ollama pull {{ ModelId }}

# Stop the ollama server
echo "Stopping ollama server..."
kill $OLLAMA_PID || true
wait $OLLAMA_PID 2>/dev/null || true

echo "Model pull completed successfully"
echo "Model is stored in OLLAMA_MODELS directory"
`
	case aitrigramv1.ModelOriginGGUF:
		// Bash script using curl
		return `#!/bin/sh
set -e
echo "Downloading GGUF model {{ ModelId }} to {{ MountPath }}/{{ ModelName }}..."
mkdir -p {{ MountPath }}/{{ ModelName }}
curl -L {{ ModelId }} -o {{ MountPath }}/{{ ModelName }}/model.gguf
echo "Download completed successfully"
`
	case aitrigramv1.ModelOriginLocal:
		// Simple bash script for local models
		return `#!/bin/sh
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
		return `#!/bin/sh
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
		return `#!/bin/sh
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
		return `#!/bin/sh
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
		return `#!/bin/sh
echo "Local model source - skipping deletion of {{ MountPath }}/{{ ModelName }}"
echo "If you want to delete local models, provide custom deleteScripts"
ls -la {{ MountPath }} || true
`
	default:
		// Default to removing directory
		return `#!/bin/sh
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
