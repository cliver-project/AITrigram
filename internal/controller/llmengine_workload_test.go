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
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

func TestBuildLLMEngineWorkload_VLLM_Default(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "meta-llama/Llama-3-8B",
			},
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-engine",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeVLLM,
			ModelRefs:  []string{"test-model"},
		},
	}

	workload, err := BuildLLMEngineWorkload(llmEngine, modelRepo)
	if err != nil {
		t.Fatalf("BuildLLMEngineWorkload() error = %v", err)
	}

	// Verify basic fields
	if workload.Image != DefaultVLLMImage {
		t.Errorf("Image = %v, want %v", workload.Image, DefaultVLLMImage)
	}

	if workload.PodPort != 8000 {
		t.Errorf("PodPort = %v, want 8000", workload.PodPort)
	}

	if workload.ServicePort != 8080 {
		t.Errorf("ServicePort = %v, want 8080", workload.ServicePort)
	}

	if workload.RequestGPU != false {
		t.Errorf("RequestGPU = %v, want false", workload.RequestGPU)
	}

	// Verify storage volumes
	if len(workload.Storage.Volumes) == 0 {
		t.Fatal("Expected storage volumes, got none")
	}

	// Should have: model-storage, vllm-cache, vllm-weights-cache, hf-hub-cache, shm
	// (order matches how they're appended in buildCacheVolumes)
	expectedVolumeNames := []string{"model-storage", "vllm-cache", "vllm-weights-cache", "hf-hub-cache", "shm"}
	actualVolumeNames := make([]string, len(workload.Storage.Volumes))
	for i, vol := range workload.Storage.Volumes {
		actualVolumeNames[i] = vol.Name
	}

	if diff := cmp.Diff(expectedVolumeNames, actualVolumeNames); diff != "" {
		t.Errorf("Volume names mismatch (-want +got):\n%s", diff)
	}

	// Verify shared memory volume
	var shmVolume *corev1.Volume
	for i := range workload.Storage.Volumes {
		if workload.Storage.Volumes[i].Name == "shm" {
			shmVolume = &workload.Storage.Volumes[i]
			break
		}
	}
	if shmVolume == nil {
		t.Fatal("shm volume not found")
	}
	if shmVolume.EmptyDir == nil || shmVolume.EmptyDir.Medium != corev1.StorageMediumMemory {
		t.Error("shm volume should use Memory medium")
	}
	expectedSize := resource.MustParse("2Gi")
	if shmVolume.EmptyDir.SizeLimit == nil || !shmVolume.EmptyDir.SizeLimit.Equal(expectedSize) {
		t.Errorf("shm volume size = %v, want 2Gi", shmVolume.EmptyDir.SizeLimit)
	}

	// Verify environment variables
	envMap := make(map[string]string)
	for _, env := range workload.Envs {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}

	expectedEnvs := map[string]string{
		"HF_HOME":                      "/data/models",
		"HF_HUB_CACHE":                 "/var/cache/huggingface/hub",
		"VLLM_CACHE_DIR":               "/var/cache/vllm",
		"VLLM_WEIGHTS_CACHE_DIR":       "/var/lib/vllm/weights-cache",
		"VLLM_LOGGING_LEVEL":           "warning",
		"VLLM_USE_RAY":                 "0",
		"VLLM_WORKER_MULTIPROC_METHOD": "spawn",
		"OMP_NUM_THREADS":              "8",
		"MKL_NUM_THREADS":              "8",
	}

	for key, expectedVal := range expectedEnvs {
		if actualVal, ok := envMap[key]; !ok {
			t.Errorf("Environment variable %s not found", key)
		} else if actualVal != expectedVal {
			t.Errorf("Environment variable %s = %v, want %v", key, actualVal, expectedVal)
		}
	}

	// Verify CPU resources (no GPU)
	if workload.Resources.Requests.Cpu().Cmp(resource.MustParse("2")) != 0 {
		t.Errorf("CPU request = %v, want 2", workload.Resources.Requests.Cpu())
	}
	if workload.Resources.Limits.Cpu().Cmp(resource.MustParse("4")) != 0 {
		t.Errorf("CPU limit = %v, want 4", workload.Resources.Limits.Cpu())
	}
	if workload.Resources.Requests.Memory().Cmp(resource.MustParse("4Gi")) != 0 {
		t.Errorf("Memory request = %v, want 4Gi", workload.Resources.Requests.Memory())
	}

	// Verify security context (CPU mode)
	if workload.SecurityContext == nil {
		t.Fatal("SecurityContext should not be nil")
	}
	if workload.SecurityContext.RunAsNonRoot == nil || !*workload.SecurityContext.RunAsNonRoot {
		t.Error("RunAsNonRoot should be true for CPU mode")
	}
	if workload.SecurityContext.AllowPrivilegeEscalation == nil || *workload.SecurityContext.AllowPrivilegeEscalation {
		t.Error("AllowPrivilegeEscalation should be false for CPU mode")
	}
}

func TestBuildLLMEngineWorkload_VLLM_GPU(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model-gpu",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "meta-llama/Llama-3-70B",
			},
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "model-pvc",
					},
				},
			},
		},
	}

	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-engine-gpu",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeVLLM,
			ModelRefs:  []string{"test-model-gpu"},
			GPU: &aitrigramv1.GPUConfig{
				Enabled: true,
				Count:   2,
				Type:    "nvidia.com/gpu",
			},
		},
	}

	workload, err := BuildLLMEngineWorkload(llmEngine, modelRepo)
	if err != nil {
		t.Fatalf("BuildLLMEngineWorkload() error = %v", err)
	}

	// Verify GPU request
	if !workload.RequestGPU {
		t.Error("RequestGPU should be true")
	}

	// Verify GPU resources
	gpuQuantity := workload.Resources.Requests[corev1.ResourceName("nvidia.com/gpu")]
	expectedGPU := resource.MustParse("2")
	if gpuQuantity.Cmp(expectedGPU) != 0 {
		t.Errorf("GPU request = %v, want 2", gpuQuantity)
	}

	// Verify GPU environment variables
	envMap := make(map[string]string)
	for _, env := range workload.Envs {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}

	expectedGPUEnvs := map[string]string{
		"VLLM_ATTENTION_BACKEND":      "flash-attn",
		"VLLM_GPU_MEMORY_UTILIZATION": "0.95",
		"CUDA_VISIBLE_DEVICES":        "0",
	}

	for key, expectedVal := range expectedGPUEnvs {
		if actualVal, ok := envMap[key]; !ok {
			t.Errorf("GPU environment variable %s not found", key)
		} else if actualVal != expectedVal {
			t.Errorf("GPU environment variable %s = %v, want %v", key, actualVal, expectedVal)
		}
	}

	// Verify GPU security context
	if workload.SecurityContext == nil {
		t.Fatal("SecurityContext should not be nil")
	}
	if workload.SecurityContext.Privileged == nil || *workload.SecurityContext.Privileged {
		t.Error("Privileged should be false")
	}
	if workload.SecurityContext.Capabilities == nil {
		t.Fatal("Capabilities should be set")
	}
	foundSysAdmin := false
	for _, cap := range workload.SecurityContext.Capabilities.Add {
		if cap == "SYS_ADMIN" {
			foundSysAdmin = true
			break
		}
	}
	if !foundSysAdmin {
		t.Error("SYS_ADMIN capability should be added for GPU")
	}
}

func TestBuildLLMEngineWorkload_VLLM_WithHFToken(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gated-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "meta-llama/Llama-3-8B-Instruct",
				HFTokenSecretRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "hf-secret",
					},
					Key: "token",
				},
			},
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-engine-token",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeVLLM,
			ModelRefs:  []string{"gated-model"},
		},
	}

	workload, err := BuildLLMEngineWorkload(llmEngine, modelRepo)
	if err != nil {
		t.Fatalf("BuildLLMEngineWorkload() error = %v", err)
	}

	// Verify HUGGING_FACE_HUB_TOKEN environment variable
	var foundToken bool
	for _, env := range workload.Envs {
		if env.Name == "HUGGING_FACE_HUB_TOKEN" {
			foundToken = true
			if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
				t.Error("HUGGING_FACE_HUB_TOKEN should have SecretKeyRef")
			} else {
				if env.ValueFrom.SecretKeyRef.Name != "hf-secret" {
					t.Errorf("Secret name = %v, want hf-secret", env.ValueFrom.SecretKeyRef.Name)
				}
				if env.ValueFrom.SecretKeyRef.Key != "token" {
					t.Errorf("Secret key = %v, want token", env.ValueFrom.SecretKeyRef.Key)
				}
			}
			break
		}
	}

	if !foundToken {
		t.Error("HUGGING_FACE_HUB_TOKEN environment variable not found")
	}
}

func TestBuildLLMEngineWorkload_VLLM_WithCustomCache(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model-custom-cache",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "meta-llama/Llama-3-8B",
			},
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-engine-custom-cache",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeVLLM,
			ModelRefs:  []string{"test-model-custom-cache"},
			Cache: &aitrigramv1.ModelStorage{
				Path: "/persistent-cache",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "cache-pvc",
					},
				},
			},
		},
	}

	workload, err := BuildLLMEngineWorkload(llmEngine, modelRepo)
	if err != nil {
		t.Fatalf("BuildLLMEngineWorkload() error = %v", err)
	}

	// Verify custom cache volume
	var foundCacheVolume bool
	for _, vol := range workload.Storage.Volumes {
		if vol.Name == "vllm-cache" {
			foundCacheVolume = true
			if vol.PersistentVolumeClaim == nil {
				t.Error("Custom cache should use PersistentVolumeClaim")
			} else if vol.PersistentVolumeClaim.ClaimName != "cache-pvc" {
				t.Errorf("PVC name = %v, want cache-pvc", vol.PersistentVolumeClaim.ClaimName)
			}
			break
		}
	}

	if !foundCacheVolume {
		t.Error("vllm-cache volume not found")
	}

	// Verify custom cache path in environment
	envMap := make(map[string]string)
	for _, env := range workload.Envs {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}

	if cachePath, ok := envMap["VLLM_CACHE_DIR"]; !ok {
		t.Error("VLLM_CACHE_DIR environment variable not found")
	} else if cachePath != "/persistent-cache" {
		t.Errorf("VLLM_CACHE_DIR = %v, want /persistent-cache", cachePath)
	}
}

func TestBuildLLMEngineWorkload_Ollama_Default(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "llama3-ollama",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginOllama,
				ModelId: "llama3:8b",
			},
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ollama-engine",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeOllama,
			ModelRefs:  []string{"llama3-ollama"},
		},
	}

	workload, err := BuildLLMEngineWorkload(llmEngine, modelRepo)
	if err != nil {
		t.Fatalf("BuildLLMEngineWorkload() error = %v", err)
	}

	// Verify basic fields
	if workload.Image != DefaultOllamaImage {
		t.Errorf("Image = %v, want %v", workload.Image, DefaultOllamaImage)
	}

	if workload.PodPort != 11434 {
		t.Errorf("PodPort = %v, want 11434", workload.PodPort)
	}

	// Verify Ollama environment variables
	envMap := make(map[string]string)
	for _, env := range workload.Envs {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}

	expectedOllamaEnvs := map[string]string{
		"OLLAMA_HOST":              "0.0.0.0",
		"OLLAMA_ORIGINS":           "*",
		"OLLAMA_MAX_LOADED_MODELS": "1",
		"OLLAMA_MODELS":            "/data/models",
		"OLLAMA_NUM_PARALLEL":      "2",
		"OLLAMA_NUM_GPU":           "0",
		"OMP_NUM_THREADS":          "8",
	}

	for key, expectedVal := range expectedOllamaEnvs {
		if actualVal, ok := envMap[key]; !ok {
			t.Errorf("Ollama environment variable %s not found", key)
		} else if actualVal != expectedVal {
			t.Errorf("Ollama environment variable %s = %v, want %v", key, actualVal, expectedVal)
		}
	}

	// Verify args
	expectedArgs := []string{"serve"}
	if diff := cmp.Diff(expectedArgs, workload.Args); diff != "" {
		t.Errorf("Args mismatch (-want +got):\n%s", diff)
	}
}
