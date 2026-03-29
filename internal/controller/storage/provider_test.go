package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

func TestProviderFactory_CreateProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = storagev1.AddToScheme(scheme)

	tests := []struct {
		name        string
		modelRepo   *aitrigramv1.ModelRepository
		existingSCs []storagev1.StorageClass
		wantErr     bool
		errContains string
	}{
		{
			name: "minikube hostpath SC creates StorageClassProvider",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-minikube",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "standard",
					},
				},
			},
			existingSCs: []storagev1.StorageClass{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "standard"},
					Provisioner: "k8s.io/minikube-hostpath",
				},
			},
		},
		{
			name: "auto with NFS creates StorageClassProvider",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-auto-nfs",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "auto",
					},
				},
			},
			existingSCs: []storagev1.StorageClass{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "nfs-client"},
					Provisioner: "nfs.csi.k8s.io",
				},
			},
		},
		{
			name: "auto with no storage classes returns error",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-auto-fallback",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "auto",
					},
				},
			},
			existingSCs: []storagev1.StorageClass{},
			wantErr:     true,
			errContains: "no StorageClass found",
		},
		{
			name: "specific storage class name creates StorageClassProvider",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-specific-sc",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "custom-sc",
					},
				},
			},
			existingSCs: []storagev1.StorageClass{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "custom-sc"},
					Provisioner: "custom.example.com/storage",
				},
			},
		},
		{
			name: "missing size returns error",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-no-size",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						StorageClass: "auto",
					},
				},
			},
			wantErr:     true,
			errContains: "storage size is required",
		},
		{
			name: "non-existent storage class returns error",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-not-found",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "does-not-exist",
					},
				},
			},
			existingSCs: []storagev1.StorageClass{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "other-sc"},
					Provisioner: "other.example.com/storage",
				},
			},
			wantErr:     true,
			errContains: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := toStorageObjects(tt.existingSCs)
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			factory := NewProviderFactory(client, scheme)

			provider, err := factory.CreateProvider(context.Background(), tt.modelRepo)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, provider)
		})
	}
}

func TestProviderFactory_ValidateConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = storagev1.AddToScheme(scheme)

	tests := []struct {
		name        string
		modelRepo   *aitrigramv1.ModelRepository
		existingSCs []storagev1.StorageClass
		wantErr     bool
		errContains string
	}{
		{
			name: "valid StorageClass config",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-valid-pvc",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "50Gi",
						StorageClass: "auto",
					},
				},
			},
			existingSCs: []storagev1.StorageClass{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "nfs-client"},
					Provisioner: "nfs.csi.k8s.io",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid storage size",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-invalid-size",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "invalid-size",
						StorageClass: "auto",
					},
				},
			},
			existingSCs: []storagev1.StorageClass{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "nfs-client"},
					Provisioner: "nfs.csi.k8s.io",
				},
			},
			wantErr:     true,
			errContains: "invalid size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := toStorageObjects(tt.existingSCs)
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
			factory := NewProviderFactory(client, scheme)

			provider, err := factory.CreateProvider(context.Background(), tt.modelRepo)
			require.NoError(t, err, "provider creation should succeed")

			err = provider.ValidateConfig()

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestProviderFactory_GetMountPath(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = storagev1.AddToScheme(scheme)

	standardSC := storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: "standard"},
		Provisioner: "k8s.io/minikube-hostpath",
	}

	tests := []struct {
		name      string
		modelRepo *aitrigramv1.ModelRepository
		wantPath  string
	}{
		{
			name: "custom mount path",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-custom-path",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "standard",
						MountPath:    "/custom/path",
					},
				},
			},
			wantPath: "/custom/path",
		},
		{
			name: "default mount path",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-default-path",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "standard",
					},
				},
			},
			wantPath: DefaultModelStoragePath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&standardSC).Build()
			factory := NewProviderFactory(client, scheme)

			provider, err := factory.CreateProvider(context.Background(), tt.modelRepo)
			require.NoError(t, err)

			path := provider.GetMountPath()
			assert.Equal(t, tt.wantPath, path)
		})
	}
}

// Helper function to convert StorageClass slice to client.Object slice
func toStorageObjects(classes []storagev1.StorageClass) []client.Object {
	objects := make([]client.Object, len(classes))
	for i := range classes {
		objects[i] = &classes[i]
	}
	return objects
}
