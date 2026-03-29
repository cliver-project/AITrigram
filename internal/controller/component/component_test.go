package component

import (
	"context"
	"testing"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestComponentBuilder tests the component builder fluent API
func TestComponentBuilder(t *testing.T) {
	builder := NewLLMEngineComponent("test-component").
		WithDeploymentAdapter(func(ctx LLMEngineContext, deployment *appsv1.Deployment) error {
			deployment.SetName("adapted-deployment")
			return nil
		}).
		WithServiceAdapter(func(ctx LLMEngineContext, service *corev1.Service) error {
			service.SetName("adapted-service")
			return nil
		}).
		WithPredicate(func(ctx LLMEngineContext) (bool, error) {
			return true, nil
		})

	component := builder.Build()

	assert.NotNil(t, component)
	assert.Equal(t, "test-component", component.Name())

	// Verify the component is of the correct type
	workload, ok := component.(*llmEngineWorkload)
	require.True(t, ok, "component should be llmEngineWorkload type")

	assert.NotNil(t, workload.adaptDeployment)
	assert.NotNil(t, workload.adaptService)
	assert.NotNil(t, workload.predicate)
}

// TestComponentBuilder_WithManifestAdapter tests adding manifest adapters
func TestComponentBuilder_WithManifestAdapter(t *testing.T) {
	adapter := NewManifestAdapter(
		func() client.Object { return &corev1.ConfigMap{} },
		func(ctx LLMEngineContext, obj client.Object) error {
			return nil
		},
	)

	builder := NewLLMEngineComponent("test-component").
		WithManifestAdapter("configmap.yaml", adapter)

	component := builder.Build()
	workload := component.(*llmEngineWorkload)

	assert.Len(t, workload.manifestAdapters, 1)
	assert.Contains(t, workload.manifestAdapters, "configmap.yaml")
}

// TestManifestAdapter_NewManifestAdapter tests creating manifest adapter
func TestManifestAdapter_NewManifestAdapter(t *testing.T) {
	adapter := NewManifestAdapter(
		func() client.Object { return &corev1.ConfigMap{} },
		func(ctx LLMEngineContext, obj client.Object) error {
			return nil
		},
	)

	assert.NotNil(t, adapter.create)
	assert.NotNil(t, adapter.adapt)
	assert.Nil(t, adapter.predicate)
}

// TestManifestAdapter_NewManifestAdapterWithPredicate tests creating manifest adapter with predicate
func TestManifestAdapter_NewManifestAdapterWithPredicate(t *testing.T) {
	adapter := NewManifestAdapterWithPredicate(
		func() client.Object { return &corev1.ConfigMap{} },
		func(ctx LLMEngineContext, obj client.Object) error {
			return nil
		},
		func(ctx LLMEngineContext) (bool, error) {
			return true, nil
		},
	)

	assert.NotNil(t, adapter.create)
	assert.NotNil(t, adapter.adapt)
	assert.NotNil(t, adapter.predicate)
}

// setupTestScheme creates a scheme with required types registered
func setupTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = scheme.AddToScheme(s)
	_ = aitrigramv1.AddToScheme(s)
	return s
}

// createTestLLMEngine creates a test LLMEngine
func createTestLLMEngine(name, namespace string, engineType aitrigramv1.LLMEngineType) *aitrigramv1.LLMEngine {
	return &aitrigramv1.LLMEngine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: aitrigramv1.LLMEngineSpec{
			EngineType: engineType,
			ModelRefs: []aitrigramv1.ModelReference{
				{Name: "test-model"},
			},
		},
	}
}

// createTestModelRepository creates a test ModelRepository
func createTestModelRepository(name string) *aitrigramv1.ModelRepository {
	return &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Source: aitrigramv1.ModelSource{
				Origin: aitrigramv1.ModelOriginOllama,
			},
		},
		Status: aitrigramv1.ModelRepositoryStatus{
			Phase: aitrigramv1.DownloadPhaseReady,
		},
	}
}

// TestLLMEngineContext_ReadAccess tests that LLMEngineContext client can read
func TestLLMEngineContext_ReadAccess(t *testing.T) {
	s := setupTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	llmEngine := createTestLLMEngine("test-engine", "default", aitrigramv1.LLMEngineTypeOllama)
	modelRepo := createTestModelRepository("test-model")

	ctx := LLMEngineContext{
		Context:   context.TODO(),
		Client:    fakeClient,
		Scheme:    s,
		LLMEngine: llmEngine,
		ModelRepo: modelRepo,
		Namespace: "default",
	}

	// Verify Client is set
	assert.NotNil(t, ctx.Client)

	// Client should be usable for reading
	deployment := &appsv1.Deployment{}
	err := ctx.Client.Get(ctx, client.ObjectKey{Name: "test", Namespace: "default"}, deployment)
	assert.Error(t, err) // Should error because object doesn't exist, but method works
}

// TestLLMEngineContext_FullClient tests that LLMEngineContext has full client
func TestLLMEngineContext_FullClient(t *testing.T) {
	s := setupTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	llmEngine := createTestLLMEngine("test-engine", "default", aitrigramv1.LLMEngineTypeOllama)
	modelRepo := createTestModelRepository("test-model")

	ctx := LLMEngineContext{
		Context:   context.TODO(),
		Client:    fakeClient,
		Scheme:    s,
		LLMEngine: llmEngine,
		ModelRepo: modelRepo,
		Namespace: "default",
	}

	// Verify Client is set
	assert.NotNil(t, ctx.Client)
	assert.NotNil(t, ctx.Scheme)

	// Client should be usable for writing
	testDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "test", Image: "test:latest"},
					},
				},
			},
		},
	}

	err := ctx.Client.Create(ctx, testDeployment)
	assert.NoError(t, err)

	// Verify it was created
	retrieved := &appsv1.Deployment{}
	err = ctx.Client.Get(ctx, client.ObjectKey{Name: "test", Namespace: "default"}, retrieved)
	assert.NoError(t, err)
	assert.Equal(t, "test", retrieved.Name)
}
