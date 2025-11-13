package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

func TestPVCProvider_GetType(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.PVCStorageSpec{
		Size: "10Gi",
	}

	provider, err := NewPVCProvider(client, scheme, modelRepo, spec, "")
	require.NoError(t, err)

	assert.Equal(t, aitrigramv1.StorageTypePVC, provider.GetType())
}

func TestPVCProvider_ValidateConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	tests := []struct {
		name        string
		spec        *aitrigramv1.PVCStorageSpec
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config with RWX",
			spec: &aitrigramv1.PVCStorageSpec{
				Size:       "50Gi",
				AccessMode: corev1.ReadWriteMany,
			},
			wantErr: false,
		},
		{
			name: "valid config with RWO",
			spec: &aitrigramv1.PVCStorageSpec{
				Size:       "50Gi",
				AccessMode: corev1.ReadWriteOnce,
			},
			wantErr: false,
		},
		{
			name: "missing size",
			spec: &aitrigramv1.PVCStorageSpec{
				Size:       "",
				AccessMode: corev1.ReadWriteMany,
			},
			wantErr:     true,
			errContains: "storage size is required",
		},
		{
			name: "invalid size format",
			spec: &aitrigramv1.PVCStorageSpec{
				Size:       "invalid-size",
				AccessMode: corev1.ReadWriteMany,
			},
			wantErr:     true,
			errContains: "invalid size",
		},
		{
			name: "invalid access mode",
			spec: &aitrigramv1.PVCStorageSpec{
				Size:       "50Gi",
				AccessMode: corev1.ReadOnlyMany,
			},
			wantErr:     true,
			errContains: "access mode must be ReadWriteMany or ReadWriteOnce",
		},
		{
			name: "default access mode (empty)",
			spec: &aitrigramv1.PVCStorageSpec{
				Size: "50Gi",
				// AccessMode not specified, should default to RWX
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewPVCProvider(client, scheme, modelRepo, tt.spec, "")
			require.NoError(t, err)

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

func TestPVCProvider_PrepareDownloadVolume(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	tests := []struct {
		name          string
		modelRepo     *aitrigramv1.ModelRepository
		spec          *aitrigramv1.PVCStorageSpec
		mountPath     string
		wantVolName   string
		wantMountPath string
		wantPVCName   string
	}{
		{
			name: "default configuration",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "llama2-7b",
				},
			},
			spec: &aitrigramv1.PVCStorageSpec{
				Size: "50Gi",
			},
			mountPath:     "",
			wantVolName:   "model-storage",
			wantMountPath: DefaultModelStoragePath,
			wantPVCName:   "llama2-7b-storage",
		},
		{
			name: "custom mount path",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "llama2-13b",
				},
			},
			spec: &aitrigramv1.PVCStorageSpec{
				Size: "100Gi",
			},
			mountPath:     "/custom/models",
			wantVolName:   "model-storage",
			wantMountPath: "/custom/models",
			wantPVCName:   "llama2-13b-storage",
		},
		{
			name: "existing PVC",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-model",
				},
			},
			spec: &aitrigramv1.PVCStorageSpec{
				Size:          "50Gi",
				ExistingClaim: stringPtr("my-existing-pvc"),
			},
			mountPath:     "",
			wantVolName:   "model-storage",
			wantMountPath: DefaultModelStoragePath,
			wantPVCName:   "my-existing-pvc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewPVCProvider(client, scheme, tt.modelRepo, tt.spec, tt.mountPath)
			require.NoError(t, err)

			volume, mount, err := provider.PrepareDownloadVolume()
			require.NoError(t, err)
			require.NotNil(t, volume)
			require.NotNil(t, mount)

			// Check volume
			assert.Equal(t, tt.wantVolName, volume.Name)
			assert.NotNil(t, volume.VolumeSource.PersistentVolumeClaim)
			assert.Equal(t, tt.wantPVCName, volume.VolumeSource.PersistentVolumeClaim.ClaimName)

			// Check volume mount
			assert.Equal(t, tt.wantVolName, mount.Name)
			assert.Equal(t, tt.wantMountPath, mount.MountPath)
			assert.False(t, mount.ReadOnly)
		})
	}
}

func TestPVCProvider_GetReadOnlyVolumeSource(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.PVCStorageSpec{
		Size: "50Gi",
	}

	provider, err := NewPVCProvider(client, scheme, modelRepo, spec, "")
	require.NoError(t, err)

	volumeSource, err := provider.GetReadOnlyVolumeSource()
	require.NoError(t, err)
	require.NotNil(t, volumeSource)
	require.NotNil(t, volumeSource.PersistentVolumeClaim)

	assert.Equal(t, "test-model-storage", volumeSource.PersistentVolumeClaim.ClaimName)
	assert.True(t, volumeSource.PersistentVolumeClaim.ReadOnly)
}

func TestPVCProvider_IsShared(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	tests := []struct {
		name       string
		accessMode corev1.PersistentVolumeAccessMode
		wantShared bool
	}{
		{
			name:       "RWX is shared",
			accessMode: corev1.ReadWriteMany,
			wantShared: true,
		},
		{
			name:       "RWO is not shared",
			accessMode: corev1.ReadWriteOnce,
			wantShared: false,
		},
		{
			name:       "default (empty) is shared",
			accessMode: "",
			wantShared: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &aitrigramv1.PVCStorageSpec{
				Size:       "50Gi",
				AccessMode: tt.accessMode,
			}

			provider, err := NewPVCProvider(client, scheme, modelRepo, spec, "")
			require.NoError(t, err)

			assert.Equal(t, tt.wantShared, provider.IsShared())
		})
	}
}

func TestPVCProvider_GetNodeAffinity(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	tests := []struct {
		name         string
		accessMode   corev1.PersistentVolumeAccessMode
		boundNode    string
		wantAffinity bool
		wantNode     string
	}{
		{
			name:         "RWX - no affinity",
			accessMode:   corev1.ReadWriteMany,
			boundNode:    "",
			wantAffinity: false,
		},
		{
			name:         "RWO without bound node - no affinity yet",
			accessMode:   corev1.ReadWriteOnce,
			boundNode:    "",
			wantAffinity: false,
		},
		{
			name:         "RWO with bound node - has affinity",
			accessMode:   corev1.ReadWriteOnce,
			boundNode:    "worker-1",
			wantAffinity: true,
			wantNode:     "worker-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modelRepo := &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Status: aitrigramv1.ModelRepositoryStatus{
					BoundNodeName: tt.boundNode,
				},
			}

			spec := &aitrigramv1.PVCStorageSpec{
				Size:       "50Gi",
				AccessMode: tt.accessMode,
			}

			provider, err := NewPVCProvider(client, scheme, modelRepo, spec, "")
			require.NoError(t, err)

			affinity := provider.GetNodeAffinity()

			if tt.wantAffinity {
				require.NotNil(t, affinity)
				require.NotNil(t, affinity.RequiredDuringSchedulingIgnoredDuringExecution)
				require.Len(t, affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms, 1)

				term := affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0]
				require.Len(t, term.MatchExpressions, 1)

				expr := term.MatchExpressions[0]
				assert.Equal(t, "kubernetes.io/hostname", expr.Key)
				assert.Equal(t, corev1.NodeSelectorOpIn, expr.Operator)
				assert.Contains(t, expr.Values, tt.wantNode)
			} else {
				assert.Nil(t, affinity)
			}
		})
	}
}

func TestPVCProvider_CreateStorage(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name      string
		modelRepo *aitrigramv1.ModelRepository
		spec      *aitrigramv1.PVCStorageSpec
		namespace string
		wantPVC   bool
		checkPVC  func(*testing.T, *corev1.PersistentVolumeClaim)
	}{
		{
			name: "create new PVC",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
			},
			spec: &aitrigramv1.PVCStorageSpec{
				Size:       "50Gi",
				AccessMode: corev1.ReadWriteMany,
			},
			namespace: "aitrigram-system",
			wantPVC:   true,
			checkPVC: func(t *testing.T, pvc *corev1.PersistentVolumeClaim) {
				assert.Equal(t, "test-model-storage", pvc.Name)
				assert.Equal(t, "aitrigram-system", pvc.Namespace)
				assert.Contains(t, pvc.Labels, "aitrigram.io/model-repo")
				assert.Equal(t, "test-model", pvc.Labels["aitrigram.io/model-repo"])

				assert.Contains(t, pvc.Spec.AccessModes, corev1.ReadWriteMany)

				size := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
				expectedSize := resource.MustParse("50Gi")
				assert.True(t, size.Equal(expectedSize))
			},
		},
		{
			name: "use existing PVC",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
			},
			spec: &aitrigramv1.PVCStorageSpec{
				Size:          "50Gi",
				ExistingClaim: stringPtr("my-existing-pvc"),
			},
			namespace: "aitrigram-system",
			wantPVC:   false, // Should not create PVC
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with existing PVC if needed
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.spec.ExistingClaim != nil {
				existingPVC := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      *tt.spec.ExistingClaim,
						Namespace: tt.namespace,
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("50Gi"),
							},
						},
					},
					Status: corev1.PersistentVolumeClaimStatus{
						Phase: corev1.ClaimBound,
					},
				}
				clientBuilder = clientBuilder.WithObjects(existingPVC)
			}
			client := clientBuilder.Build()

			provider, err := NewPVCProvider(client, scheme, tt.modelRepo, tt.spec, "")
			require.NoError(t, err)

			err = provider.CreateStorage(context.TODO(), tt.namespace)
			require.NoError(t, err)

			if tt.wantPVC {
				// Verify PVC was created
				pvc := &corev1.PersistentVolumeClaim{}
				key := types.NamespacedName{
					Name:      "test-model-storage",
					Namespace: tt.namespace,
				}
				err = client.Get(context.TODO(), key, pvc)
				require.NoError(t, err)

				if tt.checkPVC != nil {
					tt.checkPVC(t, pvc)
				}
			}
		})
	}
}

func TestPVCProvider_GetMountPath(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	tests := []struct {
		name      string
		mountPath string
		wantPath  string
	}{
		{
			name:      "default mount path",
			mountPath: "",
			wantPath:  DefaultModelStoragePath,
		},
		{
			name:      "custom mount path",
			mountPath: "/custom/path",
			wantPath:  "/custom/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &aitrigramv1.PVCStorageSpec{
				Size: "50Gi",
			}

			provider, err := NewPVCProvider(client, scheme, modelRepo, spec, tt.mountPath)
			require.NoError(t, err)

			assert.Equal(t, tt.wantPath, provider.GetMountPath())
		})
	}
}
