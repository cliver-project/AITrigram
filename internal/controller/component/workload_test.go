package component

import (
	"context"
	"errors"
	"testing"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestWorkload_Name tests that workload returns correct name
func TestWorkload_Name(t *testing.T) {
	workload := &llmEngineWorkload{
		name: "test-component",
	}

	assert.Equal(t, "test-component", workload.Name())
}

// TestWorkload_Reconcile_PredicateFalse tests that workload is deleted when predicate returns false
func TestWorkload_Reconcile_PredicateFalse(t *testing.T) {
	s := setupTestScheme()
	llmEngine := createTestLLMEngine("test-engine", "default", aitrigramv1.LLMEngineTypeOllama)
	modelRepo := createTestModelRepository("test-model")

	// Create existing deployment and service
	existingDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-component",
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

	existingService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-component",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(existingDeployment, existingService).
		Build()

	workload := &llmEngineWorkload{
		name: "test-component",
		predicate: func(ctx LLMEngineContext) (bool, error) {
			return false, nil // Predicate returns false
		},
	}

	ctx := LLMEngineContext{
		Context:   context.TODO(),
		Client:    fakeClient,
		Scheme:    s,
		LLMEngine: llmEngine,
		ModelRepo: modelRepo,
		Namespace: "default",
	}

	// Reconcile should delete resources
	err := workload.Reconcile(ctx)
	assert.NoError(t, err)

	// Verify resources are deleted
	deployment := &appsv1.Deployment{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "test-component", Namespace: "default"}, deployment)
	assert.True(t, client.IgnoreNotFound(err) == nil, "deployment should be deleted")

	service := &corev1.Service{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "test-component", Namespace: "default"}, service)
	assert.True(t, client.IgnoreNotFound(err) == nil, "service should be deleted")
}

// TestWorkload_Reconcile_PredicateError tests that reconciliation fails when predicate returns error
func TestWorkload_Reconcile_PredicateError(t *testing.T) {
	s := setupTestScheme()
	llmEngine := createTestLLMEngine("test-engine", "default", aitrigramv1.LLMEngineTypeOllama)
	modelRepo := createTestModelRepository("test-model")

	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	expectedErr := errors.New("predicate error")
	workload := &llmEngineWorkload{
		name: "test-component",
		predicate: func(ctx LLMEngineContext) (bool, error) {
			return false, expectedErr
		},
	}

	ctx := LLMEngineContext{
		Context:   context.TODO(),
		Client:    fakeClient,
		Scheme:    s,
		LLMEngine: llmEngine,
		ModelRepo: modelRepo,
		Namespace: "default",
	}

	err := workload.Reconcile(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "predicate failed")
}

// TestServerSideApply_CreateDeployment tests creating a new deployment via server-side apply
func TestServerSideApply_CreateDeployment(t *testing.T) {
	s := setupTestScheme()
	llmEngine := createTestLLMEngine("test-engine", "default", aitrigramv1.LLMEngineTypeOllama)

	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deployment",
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

	ctx := LLMEngineContext{
		Context:   context.TODO(),
		Client:    fakeClient,
		Scheme:    s,
		LLMEngine: llmEngine,
		Namespace: "default",
	}

	err := serverSideApply(ctx, deployment)
	require.NoError(t, err)

	// Verify deployment was created
	retrieved := &appsv1.Deployment{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "test-deployment", Namespace: "default"}, retrieved)
	require.NoError(t, err)
	assert.Equal(t, "test-deployment", retrieved.Name)
	assert.Equal(t, "test:latest", retrieved.Spec.Template.Spec.Containers[0].Image)
}

// TestServerSideApply_UpdateDeployment tests updating an existing deployment via server-side apply
func TestServerSideApply_UpdateDeployment(t *testing.T) {
	s := setupTestScheme()
	llmEngine := createTestLLMEngine("test-engine", "default", aitrigramv1.LLMEngineTypeOllama)

	existingDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deployment",
			Namespace: "default",
			Labels:    map[string]string{"old": "label"},
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
						{Name: "test", Image: "old:latest"},
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(existingDeployment).
		Build()

	updatedDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deployment",
			Namespace: "default",
			Labels:    map[string]string{"new": "label"},
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
						{Name: "test", Image: "new:latest"},
					},
				},
			},
		},
	}

	ctx := LLMEngineContext{
		Context:   context.TODO(),
		Client:    fakeClient,
		Scheme:    s,
		LLMEngine: llmEngine,
		Namespace: "default",
	}

	err := serverSideApply(ctx, updatedDeployment)
	require.NoError(t, err)

	// Verify deployment was updated
	retrieved := &appsv1.Deployment{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "test-deployment", Namespace: "default"}, retrieved)
	require.NoError(t, err)
	assert.Equal(t, "new:latest", retrieved.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, "label", retrieved.Labels["new"])
}

// TestServerSideApply_CreateService tests creating a service via server-side apply
func TestServerSideApply_CreateService(t *testing.T) {
	s := setupTestScheme()
	llmEngine := createTestLLMEngine("test-engine", "default", aitrigramv1.LLMEngineTypeOllama)

	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "test"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080},
			},
		},
	}

	ctx := LLMEngineContext{
		Context:   context.TODO(),
		Client:    fakeClient,
		Scheme:    s,
		LLMEngine: llmEngine,
		Namespace: "default",
	}

	err := serverSideApply(ctx, service)
	require.NoError(t, err)

	// Verify service was created
	retrieved := &corev1.Service{}
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "test-service", Namespace: "default"}, retrieved)
	require.NoError(t, err)
	assert.Equal(t, "test-service", retrieved.Name)
	assert.Equal(t, int32(8080), retrieved.Spec.Ports[0].Port)
}
