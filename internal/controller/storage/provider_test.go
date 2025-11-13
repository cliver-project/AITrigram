package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

func TestProviderFactory_CreateProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := NewProviderFactory(client, scheme)

	tests := []struct {
		name        string
		modelRepo   *aitrigramv1.ModelRepository
		wantType    aitrigramv1.StorageType
		wantErr     bool
		errContains string
	}{
		{
			name: "create PVC provider",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-pvc",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type: aitrigramv1.StorageTypePVC,
						PersistentVolumeClaim: &aitrigramv1.PVCStorageSpec{
							Size:       "10Gi",
							AccessMode: corev1.ReadWriteMany,
						},
					},
				},
			},
			wantType: aitrigramv1.StorageTypePVC,
			wantErr:  false,
		},
		{
			name: "create NFS provider",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nfs",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type: aitrigramv1.StorageTypeNFS,
						NFS: &aitrigramv1.NFSStorageSpec{
							Server: "nfs.example.com",
							Path:   "/exports/models",
						},
					},
				},
			},
			wantType: aitrigramv1.StorageTypeNFS,
			wantErr:  false,
		},
		{
			name: "create HostPath provider",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hostpath",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type: aitrigramv1.StorageTypeHostPath,
						HostPath: &aitrigramv1.HostPathStorageSpec{
							Path: "/mnt/models",
						},
					},
				},
			},
			wantType: aitrigramv1.StorageTypeHostPath,
			wantErr:  false,
		},
		{
			name: "empty storage type",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nil",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type: "",
					},
				},
			},
			wantErr:     true,
			errContains: "storage type is required",
		},
		{
			name: "missing storage spec for type",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-missing-spec",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type: aitrigramv1.StorageTypePVC,
						// Missing PersistentVolumeClaim field
					},
				},
			},
			wantErr:     true,
			errContains: "PVC storage spec is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := factory.CreateProvider(tt.modelRepo)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, provider)
			assert.Equal(t, tt.wantType, provider.GetType())
		})
	}
}

func TestProviderFactory_ValidateConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := NewProviderFactory(client, scheme)

	tests := []struct {
		name        string
		modelRepo   *aitrigramv1.ModelRepository
		wantErr     bool
		errContains string
	}{
		{
			name: "valid PVC config",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-valid-pvc",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type: aitrigramv1.StorageTypePVC,
						PersistentVolumeClaim: &aitrigramv1.PVCStorageSpec{
							Size:       "50Gi",
							AccessMode: corev1.ReadWriteMany,
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid PVC size",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-invalid-size",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type: aitrigramv1.StorageTypePVC,
						PersistentVolumeClaim: &aitrigramv1.PVCStorageSpec{
							Size:       "invalid",
							AccessMode: corev1.ReadWriteMany,
						},
					},
				},
			},
			wantErr:     true,
			errContains: "invalid size",
		},
		{
			name: "valid NFS config",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-valid-nfs",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type: aitrigramv1.StorageTypeNFS,
						NFS: &aitrigramv1.NFSStorageSpec{
							Server: "nfs.example.com",
							Path:   "/exports/models",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing NFS server",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-missing-server",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type: aitrigramv1.StorageTypeNFS,
						NFS: &aitrigramv1.NFSStorageSpec{
							Path: "/exports/models",
						},
					},
				},
			},
			wantErr:     true,
			errContains: "server is required",
		},
		{
			name: "valid HostPath config",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-valid-hostpath",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type: aitrigramv1.StorageTypeHostPath,
						HostPath: &aitrigramv1.HostPathStorageSpec{
							Path: "/mnt/models",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing HostPath path",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-missing-path",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Type:     aitrigramv1.StorageTypeHostPath,
						HostPath: &aitrigramv1.HostPathStorageSpec{},
					},
				},
			},
			wantErr:     true,
			errContains: "path is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := factory.CreateProvider(tt.modelRepo)
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

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	factory := NewProviderFactory(client, scheme)

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
						Type:      aitrigramv1.StorageTypePVC,
						MountPath: "/custom/path",
						PersistentVolumeClaim: &aitrigramv1.PVCStorageSpec{
							Size: "10Gi",
						},
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
						Type: aitrigramv1.StorageTypePVC,
						PersistentVolumeClaim: &aitrigramv1.PVCStorageSpec{
							Size: "10Gi",
						},
					},
				},
			},
			wantPath: DefaultModelStoragePath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := factory.CreateProvider(tt.modelRepo)
			require.NoError(t, err)

			path := provider.GetMountPath()
			assert.Equal(t, tt.wantPath, path)
		})
	}
}
