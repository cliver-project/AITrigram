package adapters

import (
	"testing"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/component"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// TestAdaptService_BasicOllama tests basic service adaptation for Ollama
func TestAdaptService_BasicOllama(t *testing.T) {
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

	service := createMinimalService()

	err := AdaptService(ctx, service)
	require.NoError(t, err)

	// Verify name and labels
	assert.Equal(t, "test-engine-test-model", service.Name)
	assert.Equal(t, "test-engine", service.Labels["app"])
	assert.Equal(t, "test-model", service.Labels["model"])

	// Verify selector
	assert.Equal(t, "test-engine", service.Spec.Selector["app"])
	assert.Equal(t, "test-model", service.Spec.Selector["model"])

	// Verify ports (Ollama defaults)
	assert.Len(t, service.Spec.Ports, 1)
	assert.Equal(t, "http", service.Spec.Ports[0].Name)
	assert.Equal(t, int32(8080), service.Spec.Ports[0].Port)                 // Default service port
	assert.Equal(t, intstr.FromInt(11434), service.Spec.Ports[0].TargetPort) // Ollama pod port
}

// TestAdaptService_vLLM tests service adaptation for vLLM
func TestAdaptService_vLLM(t *testing.T) {
	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vllm-engine",
			Namespace: "default",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeVLLM,
			ModelRefs: []aitrigramv1.ModelReference{
				{Name: "vllm-model"},
			},
		},
	}

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "vllm-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "test-vllm-model",
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

	service := createMinimalService()

	err := AdaptService(ctx, service)
	require.NoError(t, err)

	// Verify ports (vLLM defaults)
	assert.Equal(t, int32(8080), service.Spec.Ports[0].Port)                // Default service port
	assert.Equal(t, intstr.FromInt(8000), service.Spec.Ports[0].TargetPort) // vLLM pod port
}

// TestAdaptService_CustomServicePort tests custom service port override
func TestAdaptService_CustomServicePort(t *testing.T) {
	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-engine",
			Namespace: "default",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType:  aitrigramv1.LLMEngineTypeOllama,
			ModelRefs:   []aitrigramv1.ModelReference{{Name: "custom-model"}},
			ServicePort: 9090, // Custom service port
		},
	}

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "custom-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginOllama,
				ModelId: "test-model",
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

	service := createMinimalService()

	err := AdaptService(ctx, service)
	require.NoError(t, err)

	// Verify custom service port
	assert.Equal(t, int32(9090), service.Spec.Ports[0].Port)
}

// TestAdaptService_CustomPodPort tests custom pod port override
func TestAdaptService_CustomPodPort(t *testing.T) {
	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-engine",
			Namespace: "default",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeVLLM,
			ModelRefs:  []aitrigramv1.ModelReference{{Name: "custom-model"}},
			Port:       7777, // Custom pod port
		},
	}

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "custom-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginHuggingFace,
				ModelId: "test-model",
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

	service := createMinimalService()

	err := AdaptService(ctx, service)
	require.NoError(t, err)

	// Verify custom pod port is used as target port
	assert.Equal(t, intstr.FromInt(7777), service.Spec.Ports[0].TargetPort)
}

// TestAdaptService_MultipleModels tests service naming for multiple models
func TestAdaptService_MultipleModels(t *testing.T) {
	llmEngine := &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-engine",
			Namespace: "default",
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: aitrigramv1.LLMEngineTypeOllama,
			ModelRefs: []aitrigramv1.ModelReference{
				{Name: "model-a"},
			},
		},
	}

	modelRepoA := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "model-a",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginOllama,
				ModelId: "test-model-a",
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
						"pvcName":      "model-a-pvc",
						"pvcNamespace": "default",
					},
				},
			},
		},
	}

	modelRepoB := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "model-b",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin:  aitrigramv1.ModelOriginOllama,
				ModelId: "test-model-b",
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
						"pvcName":      "model-b-pvc",
						"pvcNamespace": "default",
					},
				},
			},
		},
	}

	// Service for model-a
	ctxA := component.LLMEngineContext{
		LLMEngine: llmEngine,
		ModelRepo: modelRepoA,
		Namespace: "default",
	}

	serviceA := createMinimalService()
	err := AdaptService(ctxA, serviceA)
	require.NoError(t, err)
	assert.Equal(t, "multi-engine-model-a", serviceA.Name)
	assert.Equal(t, "model-a", serviceA.Labels["model"])

	// Service for model-b
	ctxB := component.LLMEngineContext{
		LLMEngine: llmEngine,
		ModelRepo: modelRepoB,
		Namespace: "default",
	}

	serviceB := createMinimalService()
	err = AdaptService(ctxB, serviceB)
	require.NoError(t, err)
	assert.Equal(t, "multi-engine-model-b", serviceB.Name)
	assert.Equal(t, "model-b", serviceB.Labels["model"])

	// Verify unique names
	assert.NotEqual(t, serviceA.Name, serviceB.Name, "Services should have unique names")
}

// createMinimalService creates a minimal service template for testing
func createMinimalService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "placeholder",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app":   "placeholder",
				"model": "placeholder",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       8080,
					TargetPort: intstr.FromString("http"),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}
