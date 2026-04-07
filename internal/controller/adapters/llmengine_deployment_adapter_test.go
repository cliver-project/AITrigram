package adapters

import (
	"testing"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/component"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// TestAdaptDeployment_BasicOllama tests basic Ollama deployment adaptation
func TestAdaptDeployment_BasicOllama(t *testing.T) {
	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-engine",
			Namespace: "default",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeOllama,
			ModelRefs: []aitrigramv1.ModelReference{
				{Name: "test-model"},
			},
			Replicas: ptr.To(int32(2)),
		},
	}

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginOllama,
				ModelId: "test-ollama-model",
			},
			Storage: aitrigramv1.ModelStorage{
				StorageClass: "standard",
				Size:         "10Gi",
			},
		},
		Status: aitrigramv1.ModelRepositoryStatus{
			Phase: aitrigramv1.DownloadPhaseReady,
			Storage: &aitrigramv1.StorageStatus{
				StorageClass: "standard",
				AccessMode:   "ReadWriteMany",
				BackendRef: &aitrigramv1.BackendReference{
					Type: "pvc",
					Details: map[string]string{
						"pvcName":      "test-model-pvc",
						"pvcNamespace": "default",
					},
				},
			},
		},
	}

	ctx := component.LLMEngineContext{
		LLMEngine: llmEngine,
		ModelRepo: modelRepo,
		Namespace: "default",
	}

	deployment := createMinimalDeployment()

	err := AdaptDeployment(ctx, deployment)
	require.NoError(t, err)

	// Verify name and labels
	assert.Equal(t, "test-engine-test-model", deployment.Name)
	assert.Equal(t, "test-engine", deployment.Labels["app"])
	assert.Equal(t, "test-model", deployment.Labels["model"])
	assert.Equal(t, "ollama", deployment.Labels["engine-type"])

	// Verify replicas
	assert.Equal(t, int32(2), *deployment.Spec.Replicas)

	// Verify selector
	assert.Equal(t, "test-engine", deployment.Spec.Selector.MatchLabels["app"])
	assert.Equal(t, "test-model", deployment.Spec.Selector.MatchLabels["model"])

	// Verify container configuration
	container := &deployment.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "ollama/ollama:latest", container.Image)
	assert.Equal(t, int32(11434), container.Ports[0].ContainerPort)

	// Verify CPU resources (no GPU)
	assert.NotNil(t, container.Resources.Limits)
	assert.NotNil(t, container.Resources.Requests)
	_, hasGPU := container.Resources.Limits["nvidia.com/gpu"]
	assert.False(t, hasGPU, "CPU-only deployment should not have GPU resources")

	// Verify security context (CPU-only should be restrictive)
	assert.NotNil(t, container.SecurityContext)
	assert.True(t, *container.SecurityContext.RunAsNonRoot)
	assert.False(t, *container.SecurityContext.AllowPrivilegeEscalation)
}

// TestAdaptDeployment_vLLM_GPU tests vLLM deployment with GPU
func TestAdaptDeployment_vLLM_GPU(t *testing.T) {
	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gpu-engine",
			Namespace: "default",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeVLLM,
			ModelRefs: []aitrigramv1.ModelReference{
				{Name: "gpu-model"},
			},
			GPU: &aitrigramv1.GPUConfig{
				Enabled: true,
				Count:   2,
				Type:    "nvidia.com/gpu",
			},
		},
	}

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "test-hf-model",
			},
		},
		Status: aitrigramv1.ModelRepositoryStatus{
			Phase: aitrigramv1.DownloadPhaseReady,
			Storage: &aitrigramv1.StorageStatus{
				StorageClass: "standard",
				AccessMode:   "ReadWriteMany",
				BackendRef: &aitrigramv1.BackendReference{
					Type: "pvc",
					Details: map[string]string{
						"pvcName":      "test-model-pvc",
						"pvcNamespace": "default",
					},
				},
			},
		},
	}

	ctx := component.LLMEngineContext{
		LLMEngine: llmEngine,
		ModelRepo: modelRepo,
		Namespace: "default",
	}

	deployment := createMinimalDeployment()

	err := AdaptDeployment(ctx, deployment)
	require.NoError(t, err)

	// Verify GPU resources
	container := &deployment.Spec.Template.Spec.Containers[0]
	gpuLimit := container.Resources.Limits["nvidia.com/gpu"]
	assert.Equal(t, int64(2), gpuLimit.Value())

	gpuRequest := container.Resources.Requests["nvidia.com/gpu"]
	assert.Equal(t, int64(2), gpuRequest.Value())

	// Verify GPU security context — no SYS_ADMIN needed, NVIDIA device plugin handles access
	assert.NotNil(t, container.SecurityContext)

	// Verify vLLM image
	assert.Equal(t, "vllm/vllm-openai:latest", container.Image)
	assert.Equal(t, int32(8000), container.Ports[0].ContainerPort)
}

// TestAdaptDeployment_StorageNodeAffinity tests node affinity for HostPath storage
func TestAdaptDeployment_StorageNodeAffinity(t *testing.T) {
	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hostpath-engine",
			Namespace: "default",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeOllama,
			ModelRefs: []aitrigramv1.ModelReference{
				{Name: "hostpath-model"},
			},
		},
	}

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "local-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginOllama,
				ModelId: "test-ollama-model",
			},
			Storage: aitrigramv1.ModelStorage{
				StorageClass: "standard",
				Size:         "10Gi",
			},
		},
		Status: aitrigramv1.ModelRepositoryStatus{
			Phase: aitrigramv1.DownloadPhaseReady,
			Storage: &aitrigramv1.StorageStatus{
				StorageClass:  "standard",
				AccessMode:    "ReadWriteOnce",
				BoundNodeName: "worker-node-1",
				BackendRef: &aitrigramv1.BackendReference{
					Type: "local",
					Details: map[string]string{
						"pvcName":      "local-model-storage",
						"pvcNamespace": "default",
					},
				},
			},
		},
	}

	ctx := component.LLMEngineContext{
		LLMEngine: llmEngine,
		ModelRepo: modelRepo,
		Namespace: "default",
	}

	deployment := createMinimalDeployment()

	err := AdaptDeployment(ctx, deployment)
	require.NoError(t, err)

	// Verify node selector includes storage node binding
	assert.NotNil(t, deployment.Spec.Template.Spec.NodeSelector)
	assert.Equal(t, "worker-node-1", deployment.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"])
}

// TestAdaptDeployment_RWO_PVC_NodeAffinity tests node affinity for ReadWriteOnce PVC
func TestAdaptDeployment_RWO_PVC_NodeAffinity(t *testing.T) {
	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rwo-engine",
			Namespace: "default",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeVLLM,
			ModelRefs: []aitrigramv1.ModelReference{
				{Name: "rwo-model"},
			},
		},
	}

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rwo-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "test-hf-model",
			},
			Storage: aitrigramv1.ModelStorage{
				StorageClass: "gp2",
			},
		},
		Status: aitrigramv1.ModelRepositoryStatus{
			Phase: aitrigramv1.DownloadPhaseReady,
			Storage: &aitrigramv1.StorageStatus{
				StorageClass:  "gp2",
				AccessMode:    "ReadWriteOnce",
				BoundNodeName: "worker-node-2",
				BackendRef: &aitrigramv1.BackendReference{
					Type: "pvc",
					Details: map[string]string{
						"pvcName":      "rwo-model-pvc",
						"pvcNamespace": "default",
						"nodeName":     "worker-node-2",
					},
				},
			},
		},
	}

	ctx := component.LLMEngineContext{
		LLMEngine: llmEngine,
		ModelRepo: modelRepo,
		Namespace: "default",
	}

	deployment := createMinimalDeployment()

	err := AdaptDeployment(ctx, deployment)
	require.NoError(t, err)

	// Verify node selector includes storage node binding
	assert.NotNil(t, deployment.Spec.Template.Spec.NodeSelector)
	assert.Equal(t, "worker-node-2", deployment.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"])
}

// TestAdaptDeployment_CustomImage tests custom image override
func TestAdaptDeployment_CustomImage(t *testing.T) {
	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-engine",
			Namespace: "default",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeVLLM,
			ModelRefs: []aitrigramv1.ModelReference{
				{Name: "custom-model"},
			},
			Image: "custom/vllm:v0.2.0",
		},
	}

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "custom-model",
		},
		Status: aitrigramv1.ModelRepositoryStatus{
			Phase: aitrigramv1.DownloadPhaseReady,
			Storage: &aitrigramv1.StorageStatus{
				StorageClass: "standard",
				AccessMode:   "ReadWriteMany",
				BackendRef: &aitrigramv1.BackendReference{
					Type: "pvc",
					Details: map[string]string{
						"pvcName":      "test-model-pvc",
						"pvcNamespace": "default",
					},
				},
			},
		},
	}

	ctx := component.LLMEngineContext{
		LLMEngine: llmEngine,
		ModelRepo: modelRepo,
		Namespace: "default",
	}

	deployment := createMinimalDeployment()

	err := AdaptDeployment(ctx, deployment)
	require.NoError(t, err)

	// Verify custom image is used
	container := &deployment.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "custom/vllm:v0.2.0", container.Image)
}

// TestAdaptDeployment_HostIPC tests HostIPC configuration
func TestAdaptDeployment_HostIPC(t *testing.T) {
	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hostipc-engine",
			Namespace: "default",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeVLLM,
			ModelRefs: []aitrigramv1.ModelReference{
				{Name: "hostipc-model"},
			},
			HostIPC: true,
		},
	}

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hostipc-model",
		},
		Status: aitrigramv1.ModelRepositoryStatus{
			Phase: aitrigramv1.DownloadPhaseReady,
			Storage: &aitrigramv1.StorageStatus{
				StorageClass: "standard",
				AccessMode:   "ReadWriteMany",
				BackendRef: &aitrigramv1.BackendReference{
					Type: "pvc",
					Details: map[string]string{
						"pvcName":      "test-model-pvc",
						"pvcNamespace": "default",
					},
				},
			},
		},
	}

	ctx := component.LLMEngineContext{
		LLMEngine: llmEngine,
		ModelRepo: modelRepo,
		Namespace: "default",
	}

	deployment := createMinimalDeployment()

	err := AdaptDeployment(ctx, deployment)
	require.NoError(t, err)

	// Verify HostIPC is enabled
	assert.True(t, deployment.Spec.Template.Spec.HostIPC)
}

// TestSetDeploymentLabels tests label setting logic
func TestSetDeploymentLabels(t *testing.T) {
	deployment := createMinimalDeployment()

	setDeploymentLabels(deployment, "my-engine", "my-model", aitrigramv1.LLMEngineTypeVLLM)

	// Verify deployment labels
	assert.Equal(t, "my-engine", deployment.Labels["app"])
	assert.Equal(t, "my-model", deployment.Labels["model"])
	assert.Equal(t, "vllm", deployment.Labels["engine-type"])

	// Verify selector labels (should not include engine-type)
	assert.Equal(t, "my-engine", deployment.Spec.Selector.MatchLabels["app"])
	assert.Equal(t, "my-model", deployment.Spec.Selector.MatchLabels["model"])
	_, hasEngineType := deployment.Spec.Selector.MatchLabels["engine-type"]
	assert.False(t, hasEngineType, "selector should not include engine-type")

	// Verify pod template labels
	assert.Equal(t, "my-engine", deployment.Spec.Template.Labels["app"])
	assert.Equal(t, "my-model", deployment.Spec.Template.Labels["model"])
	assert.Equal(t, "vllm", deployment.Spec.Template.Labels["engine-type"])
}

// createMinimalDeployment creates a minimal deployment template for testing
func createMinimalDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "placeholder",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":   "placeholder",
					"model": "placeholder",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":         "placeholder",
						"model":       "placeholder",
						"engine-type": "placeholder",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "llm-engine",
							Image: "placeholder:latest",
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: 8080,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env:          []corev1.EnvVar{},
							VolumeMounts: []corev1.VolumeMount{},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(2, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(4*1024*1024*1024, resource.BinarySI),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    *resource.NewQuantity(4, resource.DecimalSI),
									corev1.ResourceMemory: *resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsNonRoot:             ptr.To(true),
								AllowPrivilegeEscalation: ptr.To(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
					Volumes:       []corev1.Volume{},
					NodeSelector:  map[string]string{},
					Tolerations:   []corev1.Toleration{},
					RestartPolicy: corev1.RestartPolicyAlways,
				},
			},
		},
	}
}

// TestBuildVLLMArgs tests vLLM CLI argument construction
func TestBuildVLLMArgs(t *testing.T) {
	tests := []struct {
		name     string
		port     int32
		expected []string
	}{
		{
			name: "default port",
			port: 8000,
			expected: []string{
				"--config", "/etc/vllm/vllm-config.yaml",
				"--host", "0.0.0.0",
				"--port", "8000",
			},
		},
		{
			name: "custom port",
			port: 8080,
			expected: []string{
				"--config", "/etc/vllm/vllm-config.yaml",
				"--host", "0.0.0.0",
				"--port", "8080",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildVLLMArgs(tt.port)
			assert.Equal(t, tt.expected, result)
		})
	}
}
