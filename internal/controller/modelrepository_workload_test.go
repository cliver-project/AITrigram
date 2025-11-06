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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

func TestBuildModelRepositoryWorkload_HuggingFace_Default(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "llama3-8b",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "meta-llama/Llama-3-8B",
			},
			ModelName: "llama3-8b",
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	workload, err := BuildModelRepositoryWorkload(modelRepo)
	if err != nil {
		t.Fatalf("BuildModelRepositoryWorkload() error = %v", err)
	}

	// Verify download image (should default to vLLM image for HuggingFace)
	if workload.DownloadImage != DefaultVLLMImage {
		t.Errorf("DownloadImage = %v, want %v", workload.DownloadImage, DefaultVLLMImage)
	}

	// Verify cleanup image
	if workload.CleanupImage != DefaultVLLMImage {
		t.Errorf("CleanupImage = %v, want %v", workload.CleanupImage, DefaultVLLMImage)
	}

	// Verify download script contains expected elements
	if !strings.Contains(workload.DownloadScript, "huggingface_hub") {
		t.Error("Download script should import huggingface_hub")
	}
	if !strings.Contains(workload.DownloadScript, "snapshot_download") {
		t.Error("Download script should use snapshot_download")
	}
	if !strings.Contains(workload.DownloadScript, "meta-llama/Llama-3-8B") {
		t.Error("Download script should contain model ID")
	}
	if !strings.Contains(workload.DownloadScript, "llama3-8b") {
		t.Error("Download script should contain model name")
	}
	if !strings.Contains(workload.DownloadScript, "/data/models") {
		t.Error("Download script should contain mount path")
	}

	// Verify cleanup script
	if !strings.Contains(workload.CleanupScript, "llama3-8b") {
		t.Error("Cleanup script should contain model name")
	}
	if !strings.Contains(workload.CleanupScript, "/data/models") {
		t.Error("Cleanup script should contain mount path")
	}

	// Verify storage configuration
	if len(workload.Storage.Volumes) != 1 {
		t.Errorf("Expected 1 volume, got %d", len(workload.Storage.Volumes))
	}
	if workload.Storage.Volumes[0].Name != "model-storage" {
		t.Errorf("Volume name = %v, want model-storage", workload.Storage.Volumes[0].Name)
	}

	if len(workload.Storage.VolumeMounts) != 1 {
		t.Errorf("Expected 1 volume mount, got %d", len(workload.Storage.VolumeMounts))
	}
	if workload.Storage.VolumeMounts[0].MountPath != "/data/models" {
		t.Errorf("MountPath = %v, want /data/models", workload.Storage.VolumeMounts[0].MountPath)
	}

	// Verify download args (should be python3)
	expectedArgs := []string{"python3", "-c", workload.DownloadScript}
	if diff := cmp.Diff(expectedArgs, workload.DownloadArgs); diff != "" {
		t.Errorf("DownloadArgs mismatch (-want +got):\n%s", diff)
	}

	// Verify cleanup args (should be bash for HuggingFace)
	if len(workload.CleanupArgs) < 3 {
		t.Fatal("CleanupArgs should have at least 3 elements")
	}
	// HuggingFace cleanup script is bash (#!/bin/sh)
	if workload.CleanupArgs[0] != "/bin/sh" && workload.CleanupArgs[0] != "python3" {
		t.Errorf("CleanupArgs[0] should be /bin/sh or python3, got %v", workload.CleanupArgs[0])
	}
	if workload.CleanupArgs[1] != "-c" {
		t.Errorf("CleanupArgs[1] should be -c, got %v", workload.CleanupArgs[1])
	}

	// Verify environment variables (should be empty for default HF download)
	if len(workload.Envs) != 0 {
		t.Errorf("Expected 0 environment variables, got %d", len(workload.Envs))
	}
}

func TestBuildModelRepositoryWorkload_HuggingFace_WithToken(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "llama3-gated",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "meta-llama/Llama-3.1-70B-Instruct",
				HFTokenSecretRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "hf-token",
					},
					Key: "token",
				},
			},
			ModelName: "llama3-70b-instruct",
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

	workload, err := BuildModelRepositoryWorkload(modelRepo)
	if err != nil {
		t.Fatalf("BuildModelRepositoryWorkload() error = %v", err)
	}

	// Verify environment variables contain HF_TOKEN
	if len(workload.Envs) != 1 {
		t.Fatalf("Expected 1 environment variable, got %d", len(workload.Envs))
	}

	if workload.Envs[0].Name != "HF_TOKEN" {
		t.Errorf("Environment variable name = %v, want HF_TOKEN", workload.Envs[0].Name)
	}

	if workload.Envs[0].ValueFrom == nil || workload.Envs[0].ValueFrom.SecretKeyRef == nil {
		t.Fatal("HF_TOKEN should have SecretKeyRef")
	}

	if workload.Envs[0].ValueFrom.SecretKeyRef.Name != "hf-token" {
		t.Errorf("Secret name = %v, want hf-token", workload.Envs[0].ValueFrom.SecretKeyRef.Name)
	}

	if workload.Envs[0].ValueFrom.SecretKeyRef.Key != "token" {
		t.Errorf("Secret key = %v, want token", workload.Envs[0].ValueFrom.SecretKeyRef.Key)
	}

	// Verify download script mentions token
	if !strings.Contains(workload.DownloadScript, "HF_TOKEN") {
		t.Error("Download script should reference HF_TOKEN environment variable")
	}
}

func TestBuildModelRepositoryWorkload_Ollama_Default(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "llama3-ollama",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginOllama,
				ModelId: "llama3:8b",
			},
			ModelName: "llama3-8b",
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	workload, err := BuildModelRepositoryWorkload(modelRepo)
	if err != nil {
		t.Fatalf("BuildModelRepositoryWorkload() error = %v", err)
	}

	// Verify download image (should default to Ollama image)
	if workload.DownloadImage != DefaultOllamaImage {
		t.Errorf("DownloadImage = %v, want %v", workload.DownloadImage, DefaultOllamaImage)
	}

	// Verify cleanup image
	if workload.CleanupImage != DefaultOllamaImage {
		t.Errorf("CleanupImage = %v, want %v", workload.CleanupImage, DefaultOllamaImage)
	}

	// Verify download script contains expected Ollama elements
	if !strings.Contains(workload.DownloadScript, "ollama") {
		t.Error("Download script should contain ollama command")
	}
	if !strings.Contains(workload.DownloadScript, "pull") {
		t.Error("Download script should use pull command")
	}
	if !strings.Contains(workload.DownloadScript, "llama3:8b") {
		t.Error("Download script should contain model ID")
	}

	// Verify environment variables for Ollama
	envMap := make(map[string]string)
	for _, env := range workload.Envs {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}

	expectedOllamaEnvs := map[string]string{
		"OLLAMA_MODELS": "/data/models",
		"OLLAMA_CACHE":  "/data/models_cache",
	}

	for key, expectedVal := range expectedOllamaEnvs {
		if actualVal, ok := envMap[key]; !ok {
			t.Errorf("Ollama environment variable %s not found", key)
		} else if actualVal != expectedVal {
			t.Errorf("Ollama environment variable %s = %v, want %v", key, actualVal, expectedVal)
		}
	}

	// Verify download args (should be bash)
	if len(workload.DownloadArgs) < 3 {
		t.Fatal("DownloadArgs should have at least 3 elements")
	}
	if workload.DownloadArgs[0] != "/bin/sh" || workload.DownloadArgs[1] != "-c" {
		t.Error("DownloadArgs should start with /bin/sh -c")
	}

	// Verify cleanup script uses ollama rm
	if !strings.Contains(workload.CleanupScript, "ollama rm") {
		t.Error("Cleanup script should use ollama rm command")
	}
	if !strings.Contains(workload.CleanupScript, "llama3:8b") {
		t.Error("Cleanup script should contain model ID")
	}
}

func TestBuildModelRepositoryWorkload_GGUF_Default(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "llama3-gguf",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginGGUF,
				ModelId: "https://huggingface.co/TheBloke/Llama-3-8B-GGUF/resolve/main/llama-3-8b.Q4_K_M.gguf",
			},
			ModelName: "llama3-8b-gguf",
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	workload, err := BuildModelRepositoryWorkload(modelRepo)
	if err != nil {
		t.Fatalf("BuildModelRepositoryWorkload() error = %v", err)
	}

	// Verify download image (should default to python image for GGUF)
	if workload.DownloadImage != "python:3.11-slim" {
		t.Errorf("DownloadImage = %v, want python:3.11-slim", workload.DownloadImage)
	}

	// Verify download script contains curl command
	if !strings.Contains(workload.DownloadScript, "curl") {
		t.Error("Download script should use curl for GGUF download")
	}
	if !strings.Contains(workload.DownloadScript, workload.DownloadScript) {
		t.Error("Download script should contain model URL")
	}
	if !strings.Contains(workload.DownloadScript, "llama3-8b-gguf") {
		t.Error("Download script should contain model name")
	}
	if !strings.Contains(workload.DownloadScript, "model.gguf") {
		t.Error("Download script should save as model.gguf")
	}

	// Verify cleanup script
	if !strings.Contains(workload.CleanupScript, "llama3-8b-gguf") {
		t.Error("Cleanup script should contain model name")
	}
	if !strings.Contains(workload.CleanupScript, "/data/models") {
		t.Error("Cleanup script should contain mount path")
	}

	// Verify download args (should be bash)
	if len(workload.DownloadArgs) < 3 {
		t.Fatal("DownloadArgs should have at least 3 elements")
	}
	if workload.DownloadArgs[0] != "/bin/sh" || workload.DownloadArgs[1] != "-c" {
		t.Error("DownloadArgs should start with /bin/sh -c")
	}
}

func TestBuildModelRepositoryWorkload_CustomDownloadScript(t *testing.T) {
	customScript := `#!/bin/bash
echo "Custom download script for {{ ModelId }}"
echo "Saving to {{ MountPath }}/{{ ModelName }}"
# Custom download logic here
`

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "custom-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginLocal,
				ModelId: "custom-model-id",
			},
			ModelName:       "custom-model",
			DownloadScripts: customScript,
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	workload, err := BuildModelRepositoryWorkload(modelRepo)
	if err != nil {
		t.Fatalf("BuildModelRepositoryWorkload() error = %v", err)
	}

	// Verify that custom script was rendered with template values
	if !strings.Contains(workload.DownloadScript, "custom-model-id") {
		t.Error("Download script should contain rendered model ID")
	}
	if !strings.Contains(workload.DownloadScript, "custom-model") {
		t.Error("Download script should contain rendered model name")
	}
	if !strings.Contains(workload.DownloadScript, "/data/models") {
		t.Error("Download script should contain rendered mount path")
	}
	if !strings.Contains(workload.DownloadScript, "Custom download script") {
		t.Error("Download script should contain custom content")
	}
}

func TestBuildModelRepositoryWorkload_CustomDeleteScript(t *testing.T) {
	customDeleteScript := `#!/bin/bash
echo "Custom delete script for {{ ModelName }}"
rm -rf {{ MountPath }}/{{ ModelName }}
`

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "custom-delete-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "test/model",
			},
			ModelName:     "test-model",
			DeleteScripts: customDeleteScript,
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	workload, err := BuildModelRepositoryWorkload(modelRepo)
	if err != nil {
		t.Fatalf("BuildModelRepositoryWorkload() error = %v", err)
	}

	// Verify that custom delete script was rendered
	if !strings.Contains(workload.CleanupScript, "Custom delete script") {
		t.Error("Cleanup script should contain custom content")
	}
	if !strings.Contains(workload.CleanupScript, "test-model") {
		t.Error("Cleanup script should contain rendered model name")
	}
	if !strings.Contains(workload.CleanupScript, "/data/models") {
		t.Error("Cleanup script should contain rendered mount path")
	}
}

func TestBuildModelRepositoryWorkload_CustomDownloadImage(t *testing.T) {
	customImage := "my-custom-downloader:v1.0"

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "custom-image-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "test/model",
			},
			ModelName:     "test-model",
			DownloadImage: customImage,
			Storage: aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		},
	}

	workload, err := BuildModelRepositoryWorkload(modelRepo)
	if err != nil {
		t.Fatalf("BuildModelRepositoryWorkload() error = %v", err)
	}

	// Verify custom download image is used
	if workload.DownloadImage != customImage {
		t.Errorf("DownloadImage = %v, want %v", workload.DownloadImage, customImage)
	}
}

func TestBuildModelRepositoryWorkload_StorageConfiguration(t *testing.T) {
	// Test with PVC
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pvc-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "test/model",
			},
			ModelName: "test-model",
			Storage: aitrigramv1.ModelStorage{
				Path: "/mnt/shared-storage",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "shared-pvc",
						ReadOnly:  false,
					},
				},
			},
		},
	}

	workload, err := BuildModelRepositoryWorkload(modelRepo)
	if err != nil {
		t.Fatalf("BuildModelRepositoryWorkload() error = %v", err)
	}

	// Verify storage volume
	if len(workload.Storage.Volumes) != 1 {
		t.Fatalf("Expected 1 volume, got %d", len(workload.Storage.Volumes))
	}

	vol := workload.Storage.Volumes[0]
	if vol.Name != "model-storage" {
		t.Errorf("Volume name = %v, want model-storage", vol.Name)
	}

	if vol.PersistentVolumeClaim == nil {
		t.Fatal("Volume should use PersistentVolumeClaim")
	}

	if vol.PersistentVolumeClaim.ClaimName != "shared-pvc" {
		t.Errorf("PVC name = %v, want shared-pvc", vol.PersistentVolumeClaim.ClaimName)
	}

	// Verify storage volume mount
	if len(workload.Storage.VolumeMounts) != 1 {
		t.Fatalf("Expected 1 volume mount, got %d", len(workload.Storage.VolumeMounts))
	}

	mount := workload.Storage.VolumeMounts[0]
	if mount.Name != "model-storage" {
		t.Errorf("VolumeMount name = %v, want model-storage", mount.Name)
	}

	if mount.MountPath != "/mnt/shared-storage" {
		t.Errorf("MountPath = %v, want /mnt/shared-storage", mount.MountPath)
	}
}
