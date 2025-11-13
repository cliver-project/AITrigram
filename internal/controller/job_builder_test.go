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
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestJobBuilder_HuggingFace_DefaultConfig(t *testing.T) {
	// Test: HuggingFace origin with all defaults (no customization)
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "test-pvc",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID":   "meta-llama/Llama-2-7b",
		"MODEL_NAME": "llama2-7b",
		"MOUNT_PATH": "/data/models",
	}

	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", "", params, nil)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	// Assertions
	if job.Name != "test-job" {
		t.Errorf("Expected job name 'test-job', got '%s'", job.Name)
	}
	if job.Namespace != "default" {
		t.Errorf("Expected namespace 'default', got '%s'", job.Namespace)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// Check default image from asset config
	if container.Image != "python:3.11-slim" {
		t.Errorf("Expected default image 'python:3.11-slim', got '%s'", container.Image)
	}

	// Check volume mount
	if len(container.VolumeMounts) != 1 {
		t.Fatalf("Expected 1 volume mount, got %d", len(container.VolumeMounts))
	}
	if container.VolumeMounts[0].MountPath != "/data/models" {
		t.Errorf("Expected mount path '/data/models', got '%s'", container.VolumeMounts[0].MountPath)
	}

	// Check environment variables
	envMap := make(map[string]string)
	for _, env := range container.Env {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}

	// Asset config should set HF_HOME and HF_HUB_CACHE to mount path
	if envMap["HF_HOME"] != "/data/models" {
		t.Errorf("Expected HF_HOME='/data/models', got '%s'", envMap["HF_HOME"])
	}
	if envMap["HF_HUB_CACHE"] != "/data/models" {
		t.Errorf("Expected HF_HUB_CACHE='/data/models', got '%s'", envMap["HF_HUB_CACHE"])
	}

	// Parameters should be in environment
	if envMap["MODEL_ID"] != "meta-llama/Llama-2-7b" {
		t.Errorf("Expected MODEL_ID='meta-llama/Llama-2-7b', got '%s'", envMap["MODEL_ID"])
	}
	if envMap["MODEL_NAME"] != "llama2-7b" {
		t.Errorf("Expected MODEL_NAME='llama2-7b', got '%s'", envMap["MODEL_NAME"])
	}

	// Check that script is loaded (command should have embedded script)
	if len(container.Command) == 0 {
		t.Fatal("Expected command to be set")
	}
	if container.Command[0] != "python3" {
		t.Errorf("Expected interpreter 'python3', got '%s'", container.Command[0])
	}
	if !strings.Contains(container.Command[2], "huggingface_hub") {
		t.Error("Expected embedded script to contain 'huggingface_hub'")
	}

	// Check security context
	if container.SecurityContext.AllowPrivilegeEscalation == nil || *container.SecurityContext.AllowPrivilegeEscalation {
		t.Error("Expected AllowPrivilegeEscalation to be false")
	}

	// Check resource requirements
	if container.Resources.Requests.Memory().Cmp(resource.MustParse("512Mi")) != 0 {
		t.Errorf("Expected memory request 512Mi, got %s", container.Resources.Requests.Memory())
	}
}

func TestJobBuilder_HuggingFace_CustomImage(t *testing.T) {
	// Test: HuggingFace with custom image override
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "test-pvc",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID": "test-model",
	}

	customImage := "my-registry.io/my-custom-image:v1"
	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", customImage, "", params, nil)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// Should use custom image
	if container.Image != customImage {
		t.Errorf("Expected custom image '%s', got '%s'", customImage, container.Image)
	}

	// Should still use embedded script
	if !strings.Contains(container.Command[2], "huggingface_hub") {
		t.Error("Expected to use embedded script even with custom image")
	}
}

func TestJobBuilder_HuggingFace_CustomScript(t *testing.T) {
	// Test: HuggingFace with custom script override
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "test-pvc",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID": "test-model",
	}

	customScript := `#!/bin/bash
echo "Custom download script"
echo "MODEL_ID: $MODEL_ID"
`

	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", customScript, params, nil)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// Should use default image
	if container.Image != "python:3.11-slim" {
		t.Errorf("Expected default image 'python:3.11-slim', got '%s'", container.Image)
	}

	// Should use custom script
	if !strings.Contains(container.Command[2], "Custom download script") {
		t.Error("Expected custom script to be used")
	}
	if !strings.Contains(container.Command[2], "echo \"MODEL_ID: $MODEL_ID\"") {
		t.Error("Expected custom script content")
	}

	// Environment variables should still be configured
	envMap := make(map[string]string)
	for _, env := range container.Env {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}
	if envMap["HF_HOME"] != "/data/models" {
		t.Errorf("Expected HF_HOME to be set even with custom script")
	}
}

func TestJobBuilder_HuggingFace_WithSecret(t *testing.T) {
	// Test: HuggingFace with HF_TOKEN secret
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "test-pvc",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID": "test-model",
	}

	secretRef := &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: "my-hf-secret",
		},
		Key: "token",
	}

	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", "", params, secretRef)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// Find HF_TOKEN environment variable
	var hfTokenEnv *corev1.EnvVar
	for i := range container.Env {
		if container.Env[i].Name == "HF_TOKEN" {
			hfTokenEnv = &container.Env[i]
			break
		}
	}

	if hfTokenEnv == nil {
		t.Fatal("Expected HF_TOKEN environment variable to be set")
	}

	// Should reference the secret
	if hfTokenEnv.ValueFrom == nil || hfTokenEnv.ValueFrom.SecretKeyRef == nil {
		t.Fatal("Expected HF_TOKEN to reference a secret")
	}

	if hfTokenEnv.ValueFrom.SecretKeyRef.Name != "my-hf-secret" {
		t.Errorf("Expected secret name 'my-hf-secret', got '%s'", hfTokenEnv.ValueFrom.SecretKeyRef.Name)
	}
	if hfTokenEnv.ValueFrom.SecretKeyRef.Key != "token" {
		t.Errorf("Expected secret key 'token', got '%s'", hfTokenEnv.ValueFrom.SecretKeyRef.Key)
	}
}

func TestJobBuilder_Ollama_DefaultConfig(t *testing.T) {
	// Test: Ollama origin with defaults
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: "/mnt/models",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	builder, err := NewJobBuilder(ctx, client, "ollama", "", volume, volumeMount, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_NAME": "llama2:7b",
	}

	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", "", params, nil)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// Check default image from Ollama asset config
	if container.Image != "ollama/ollama:latest" {
		t.Errorf("Expected default image 'ollama/ollama:latest', got '%s'", container.Image)
	}

	// Check interpreter
	if container.Command[0] != "bash" {
		t.Errorf("Expected bash interpreter for Ollama, got '%s'", container.Command[0])
	}

	// Check script contains ollama pull
	if !strings.Contains(container.Command[2], "ollama pull") {
		t.Error("Expected Ollama script to contain 'ollama pull'")
	}

	// Check environment variables
	envMap := make(map[string]string)
	for _, env := range container.Env {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}

	if envMap["OLLAMA_HOME"] != "/data/models" {
		t.Errorf("Expected OLLAMA_HOME='/data/models', got '%s'", envMap["OLLAMA_HOME"])
	}
	if envMap["OLLAMA_MODELS"] != "/data/models" {
		t.Errorf("Expected OLLAMA_MODELS='/data/models', got '%s'", envMap["OLLAMA_MODELS"])
	}
	if envMap["MODEL_NAME"] != "llama2:7b" {
		t.Errorf("Expected MODEL_NAME='llama2:7b', got '%s'", envMap["MODEL_NAME"])
	}
}

func TestJobBuilder_CustomMountPath(t *testing.T) {
	// Test: Custom mount path
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	customMountPath := "/custom/path/models"

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "test-pvc",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: customMountPath,
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", customMountPath, volume, volumeMount, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID": "test-model",
	}

	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", "", params, nil)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// Check volume mount path
	if container.VolumeMounts[0].MountPath != customMountPath {
		t.Errorf("Expected mount path '%s', got '%s'", customMountPath, container.VolumeMounts[0].MountPath)
	}

	// Check environment variables use custom mount path
	envMap := make(map[string]string)
	for _, env := range container.Env {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}

	if envMap["HF_HOME"] != customMountPath {
		t.Errorf("Expected HF_HOME='%s', got '%s'", customMountPath, envMap["HF_HOME"])
	}
}

func TestJobBuilder_WithNodeSelector(t *testing.T) {
	// Test: Job with node selector (for HostPath storage)
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: "/mnt/models",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	nodeSelector := map[string]string{
		"kubernetes.io/hostname": "worker-node-1",
		"storage-type":           "nvme",
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nodeSelector, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID": "test-model",
	}

	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", "", params, nil)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	podSpec := job.Spec.Template.Spec

	// Check node selector
	if podSpec.NodeSelector == nil {
		t.Fatal("Expected node selector to be set")
	}

	if podSpec.NodeSelector["kubernetes.io/hostname"] != "worker-node-1" {
		t.Errorf("Expected hostname selector 'worker-node-1', got '%s'", podSpec.NodeSelector["kubernetes.io/hostname"])
	}

	if podSpec.NodeSelector["storage-type"] != "nvme" {
		t.Errorf("Expected storage-type selector 'nvme', got '%s'", podSpec.NodeSelector["storage-type"])
	}
}

func TestJobBuilder_WithNodeAffinity(t *testing.T) {
	// Test: Job with node affinity (for RWO PVC)
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "test-pvc-rwo",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	nodeAffinity := &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      "kubernetes.io/hostname",
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{"bound-node"},
						},
					},
				},
			},
		},
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nil, nodeAffinity)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID": "test-model",
	}

	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", "", params, nil)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	podSpec := job.Spec.Template.Spec

	// Check node affinity
	if podSpec.Affinity == nil || podSpec.Affinity.NodeAffinity == nil {
		t.Fatal("Expected node affinity to be set")
	}

	nodeAffinitySpec := podSpec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	if len(nodeAffinitySpec.NodeSelectorTerms) != 1 {
		t.Fatalf("Expected 1 node selector term, got %d", len(nodeAffinitySpec.NodeSelectorTerms))
	}

	matchExpr := nodeAffinitySpec.NodeSelectorTerms[0].MatchExpressions[0]
	if matchExpr.Key != "kubernetes.io/hostname" {
		t.Errorf("Expected key 'kubernetes.io/hostname', got '%s'", matchExpr.Key)
	}
	if matchExpr.Values[0] != "bound-node" {
		t.Errorf("Expected value 'bound-node', got '%s'", matchExpr.Values[0])
	}
}

func TestJobBuilder_CleanupJob(t *testing.T) {
	// Test: Cleanup job generation
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "test-pvc",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID": "test-model",
	}

	job, err := builder.BuildCleanupJob("cleanup-job", "default", "test-model-repo", "", "", params, nil)
	if err != nil {
		t.Fatalf("Failed to build cleanup job: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// Should use cleanup script
	if !strings.Contains(container.Command[2], "cleanup") || !strings.Contains(container.Command[2], "scan_cache_dir") {
		t.Error("Expected cleanup script to be used")
	}

	// Should have same image as download
	if container.Image != "python:3.11-slim" {
		t.Errorf("Expected cleanup to use same image as download")
	}

	// Environment should be configured
	envMap := make(map[string]string)
	for _, env := range container.Env {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}

	if envMap["HF_HOME"] != "/data/models" {
		t.Errorf("Expected HF_HOME in cleanup job")
	}
}

func TestJobBuilder_AllCombinations(t *testing.T) {
	// Test: All combinations of customization
	testCases := []struct {
		name                string
		origin              string
		customImage         string
		customScript        string
		mountPath           string
		hasSecret           bool
		hasNodeSelector     bool
		hasNodeAffinity     bool
		expectedImage       string
		expectedInterpreter string
	}{
		{
			name:                "HuggingFace-AllDefaults",
			origin:              "huggingface",
			customImage:         "",
			customScript:        "",
			mountPath:           "",
			hasSecret:           false,
			hasNodeSelector:     false,
			hasNodeAffinity:     false,
			expectedImage:       "python:3.11-slim",
			expectedInterpreter: "python3",
		},
		{
			name:                "HuggingFace-CustomImage",
			origin:              "huggingface",
			customImage:         "custom:v1",
			customScript:        "",
			mountPath:           "",
			hasSecret:           false,
			hasNodeSelector:     false,
			hasNodeAffinity:     false,
			expectedImage:       "custom:v1",
			expectedInterpreter: "python3",
		},
		{
			name:                "HuggingFace-CustomScript",
			origin:              "huggingface",
			customImage:         "",
			customScript:        "#!/bin/bash\necho test",
			mountPath:           "",
			hasSecret:           false,
			hasNodeSelector:     false,
			hasNodeAffinity:     false,
			expectedImage:       "python:3.11-slim",
			expectedInterpreter: "python3",
		},
		{
			name:                "HuggingFace-CustomMountPath",
			origin:              "huggingface",
			customImage:         "",
			customScript:        "",
			mountPath:           "/custom/path",
			hasSecret:           false,
			hasNodeSelector:     false,
			hasNodeAffinity:     false,
			expectedImage:       "python:3.11-slim",
			expectedInterpreter: "python3",
		},
		{
			name:                "HuggingFace-WithSecret",
			origin:              "huggingface",
			customImage:         "",
			customScript:        "",
			mountPath:           "",
			hasSecret:           true,
			hasNodeSelector:     false,
			hasNodeAffinity:     false,
			expectedImage:       "python:3.11-slim",
			expectedInterpreter: "python3",
		},
		{
			name:                "HuggingFace-WithNodeSelector",
			origin:              "huggingface",
			customImage:         "",
			customScript:        "",
			mountPath:           "",
			hasSecret:           false,
			hasNodeSelector:     true,
			hasNodeAffinity:     false,
			expectedImage:       "python:3.11-slim",
			expectedInterpreter: "python3",
		},
		{
			name:                "HuggingFace-WithNodeAffinity",
			origin:              "huggingface",
			customImage:         "",
			customScript:        "",
			mountPath:           "",
			hasSecret:           false,
			hasNodeSelector:     false,
			hasNodeAffinity:     true,
			expectedImage:       "python:3.11-slim",
			expectedInterpreter: "python3",
		},
		{
			name:                "HuggingFace-FullyCustom",
			origin:              "huggingface",
			customImage:         "custom:v1",
			customScript:        "#!/bin/bash\necho custom",
			mountPath:           "/custom/path",
			hasSecret:           true,
			hasNodeSelector:     true,
			hasNodeAffinity:     true,
			expectedImage:       "custom:v1",
			expectedInterpreter: "python3",
		},
		{
			name:                "Ollama-AllDefaults",
			origin:              "ollama",
			customImage:         "",
			customScript:        "",
			mountPath:           "",
			hasSecret:           false,
			hasNodeSelector:     false,
			hasNodeAffinity:     false,
			expectedImage:       "ollama/ollama:latest",
			expectedInterpreter: "bash",
		},
		{
			name:                "Ollama-CustomImage",
			origin:              "ollama",
			customImage:         "my-ollama:v2",
			customScript:        "",
			mountPath:           "",
			hasSecret:           false,
			hasNodeSelector:     false,
			hasNodeAffinity:     false,
			expectedImage:       "my-ollama:v2",
			expectedInterpreter: "bash",
		},
		{
			name:                "Ollama-WithNodeSelector",
			origin:              "ollama",
			customImage:         "",
			customScript:        "",
			mountPath:           "",
			hasSecret:           false,
			hasNodeSelector:     true,
			hasNodeAffinity:     false,
			expectedImage:       "ollama/ollama:latest",
			expectedInterpreter: "bash",
		},
		{
			name:                "Ollama-FullyCustom",
			origin:              "ollama",
			customImage:         "custom-ollama:v1",
			customScript:        "#!/bin/bash\necho ollama custom",
			mountPath:           "/ollama/models",
			hasSecret:           false,
			hasNodeSelector:     true,
			hasNodeAffinity:     true,
			expectedImage:       "custom-ollama:v1",
			expectedInterpreter: "bash",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

			mountPath := tc.mountPath
			if mountPath == "" {
				mountPath = "/data/models"
			}

			volume := &corev1.Volume{
				Name: "model-storage",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "test-pvc",
					},
				},
			}
			volumeMount := &corev1.VolumeMount{
				Name:      "model-storage",
				MountPath: mountPath,
			}

			var nodeSelector map[string]string
			if tc.hasNodeSelector {
				nodeSelector = map[string]string{
					"kubernetes.io/hostname": "test-node",
				}
			}

			var nodeAffinity *corev1.NodeAffinity
			if tc.hasNodeAffinity {
				nodeAffinity = &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "test-key",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"test-value"},
									},
								},
							},
						},
					},
				}
			}

			builder, err := NewJobBuilder(ctx, client, tc.origin, tc.mountPath, volume, volumeMount, nodeSelector, nodeAffinity)
			if err != nil {
				t.Fatalf("Failed to create JobBuilder: %v", err)
			}

			params := map[string]string{
				"MODEL_ID": "test-model",
			}

			var secretRef *corev1.SecretKeySelector
			if tc.hasSecret {
				secretRef = &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "test-secret",
					},
					Key: "token",
				}
			}

			job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", tc.customImage, tc.customScript, params, secretRef)
			if err != nil {
				t.Fatalf("Failed to build download job: %v", err)
			}

			container := job.Spec.Template.Spec.Containers[0]

			// Check image
			if container.Image != tc.expectedImage {
				t.Errorf("Expected image '%s', got '%s'", tc.expectedImage, container.Image)
			}

			// Check interpreter
			if container.Command[0] != tc.expectedInterpreter {
				t.Errorf("Expected interpreter '%s', got '%s'", tc.expectedInterpreter, container.Command[0])
			}

			// Check mount path
			if container.VolumeMounts[0].MountPath != mountPath {
				t.Errorf("Expected mount path '%s', got '%s'", mountPath, container.VolumeMounts[0].MountPath)
			}

			// Check node selector
			podSpec := job.Spec.Template.Spec
			if tc.hasNodeSelector && podSpec.NodeSelector == nil {
				t.Error("Expected node selector to be set")
			}
			if !tc.hasNodeSelector && podSpec.NodeSelector != nil {
				t.Error("Expected node selector to be nil")
			}

			// Check node affinity
			if tc.hasNodeAffinity && (podSpec.Affinity == nil || podSpec.Affinity.NodeAffinity == nil) {
				t.Error("Expected node affinity to be set")
			}
			if !tc.hasNodeAffinity && podSpec.Affinity != nil {
				t.Error("Expected affinity to be nil")
			}

			// Check secret
			if tc.hasSecret {
				var found bool
				for _, env := range container.Env {
					if env.Name == "HF_TOKEN" && env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
						found = true
						break
					}
				}
				if !found {
					t.Error("Expected HF_TOKEN secret to be mounted")
				}
			}

			// Check custom script
			if tc.customScript != "" {
				if !strings.Contains(container.Command[2], "echo") {
					t.Error("Expected custom script to be used")
				}
			}
		})
	}
}

func TestJobBuilder_VolumeTypes(t *testing.T) {
	// Test: Different volume types (PVC, NFS, HostPath)
	testCases := []struct {
		name         string
		volumeSource corev1.VolumeSource
		description  string
	}{
		{
			name: "PVC-RWX",
			volumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "rwx-pvc",
					ReadOnly:  false,
				},
			},
			description: "ReadWriteMany PVC",
		},
		{
			name: "PVC-RWO",
			volumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "rwo-pvc",
					ReadOnly:  false,
				},
			},
			description: "ReadWriteOnce PVC",
		},
		{
			name: "NFS",
			volumeSource: corev1.VolumeSource{
				NFS: &corev1.NFSVolumeSource{
					Server: "nfs-server.example.com",
					Path:   "/exports/models",
				},
			},
			description: "NFS volume",
		},
		{
			name: "HostPath",
			volumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: "/mnt/models",
				},
			},
			description: "HostPath volume",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

			volume := &corev1.Volume{
				Name:         "model-storage",
				VolumeSource: tc.volumeSource,
			}
			volumeMount := &corev1.VolumeMount{
				Name:      "model-storage",
				MountPath: "/data/models",
			}

			builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nil, nil)
			if err != nil {
				t.Fatalf("Failed to create JobBuilder: %v", err)
			}

			params := map[string]string{
				"MODEL_ID": "test-model",
			}

			job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", "", params, nil)
			if err != nil {
				t.Fatalf("Failed to build download job: %v", err)
			}

			// Check that volume is correctly set in job
			if len(job.Spec.Template.Spec.Volumes) != 1 {
				t.Fatalf("Expected 1 volume, got %d", len(job.Spec.Template.Spec.Volumes))
			}

			jobVolume := job.Spec.Template.Spec.Volumes[0]
			if jobVolume.Name != "model-storage" {
				t.Errorf("Expected volume name 'model-storage', got '%s'", jobVolume.Name)
			}

			// Verify volume source matches
			if tc.volumeSource.PersistentVolumeClaim != nil && jobVolume.PersistentVolumeClaim == nil {
				t.Error("Expected PVC volume source")
			}
			if tc.volumeSource.NFS != nil && jobVolume.NFS == nil {
				t.Error("Expected NFS volume source")
			}
			if tc.volumeSource.HostPath != nil && jobVolume.HostPath == nil {
				t.Error("Expected HostPath volume source")
			}
		})
	}
}

func TestJobBuilder_ResourceRequirements(t *testing.T) {
	// Test: Resource requirements from asset config
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "test-pvc",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID": "test-model",
	}

	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", "", params, nil)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]

	// Check resource requests (from huggingface/config.yaml)
	expectedMemRequest := resource.MustParse("512Mi")
	expectedCPURequest := resource.MustParse("500m")

	if container.Resources.Requests.Memory().Cmp(expectedMemRequest) != 0 {
		t.Errorf("Expected memory request %s, got %s", expectedMemRequest.String(), container.Resources.Requests.Memory().String())
	}
	if container.Resources.Requests.Cpu().Cmp(expectedCPURequest) != 0 {
		t.Errorf("Expected CPU request %s, got %s", expectedCPURequest.String(), container.Resources.Requests.Cpu().String())
	}

	// Check resource limits
	expectedMemLimit := resource.MustParse("4Gi")
	expectedCPULimit := resource.MustParse("2000m")

	if container.Resources.Limits.Memory().Cmp(expectedMemLimit) != 0 {
		t.Errorf("Expected memory limit %s, got %s", expectedMemLimit.String(), container.Resources.Limits.Memory().String())
	}
	if container.Resources.Limits.Cpu().Cmp(expectedCPULimit) != 0 {
		t.Errorf("Expected CPU limit %s, got %s", expectedCPULimit.String(), container.Resources.Limits.Cpu().String())
	}
}

func TestJobBuilder_SecurityContext(t *testing.T) {
	// Test: Security contexts are properly set
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "test-pvc",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID": "test-model",
	}

	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", "", params, nil)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	// Check pod security context
	podSecurityContext := job.Spec.Template.Spec.SecurityContext
	if podSecurityContext == nil {
		t.Fatal("Expected pod security context to be set")
	}
	if podSecurityContext.SeccompProfile == nil || podSecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("Expected seccomp profile to be RuntimeDefault")
	}

	// Check container security context
	container := job.Spec.Template.Spec.Containers[0]
	containerSecurityContext := container.SecurityContext
	if containerSecurityContext == nil {
		t.Fatal("Expected container security context to be set")
	}

	if containerSecurityContext.AllowPrivilegeEscalation == nil || *containerSecurityContext.AllowPrivilegeEscalation {
		t.Error("Expected AllowPrivilegeEscalation to be false")
	}

	if containerSecurityContext.Capabilities == nil || len(containerSecurityContext.Capabilities.Drop) == 0 {
		t.Fatal("Expected capabilities to be dropped")
	}

	if containerSecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Error("Expected ALL capabilities to be dropped")
	}

	if containerSecurityContext.SeccompProfile == nil || containerSecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("Expected container seccomp profile to be RuntimeDefault")
	}
}

func TestJobBuilder_RestartPolicy(t *testing.T) {
	// Test: Restart policy is OnFailure
	ctx := context.Background()
	client := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: "test-pvc",
			},
		},
	}
	volumeMount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: "/data/models",
	}

	builder, err := NewJobBuilder(ctx, client, "huggingface", "", volume, volumeMount, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create JobBuilder: %v", err)
	}

	params := map[string]string{
		"MODEL_ID": "test-model",
	}

	job, err := builder.BuildDownloadJob("test-job", "default", "test-model-repo", "", "", params, nil)
	if err != nil {
		t.Fatalf("Failed to build download job: %v", err)
	}

	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyOnFailure {
		t.Errorf("Expected restart policy OnFailure, got %s", job.Spec.Template.Spec.RestartPolicy)
	}
}
