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

func TestNewStorageClassProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name              string
		modelRepo         *aitrigramv1.ModelRepository
		scInfo            *StorageClassInfo
		expectedMountPath string
		expectedPVCName   string
	}{
		{
			name: "default mount path with RWX storage",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "nfs-client",
					},
				},
			},
			scInfo: &StorageClassInfo{
				Name:       "nfs-client",
				Type:       "nfs",
				AccessMode: corev1.ReadWriteMany,
			},
			expectedMountPath: DefaultModelStoragePath,
			expectedPVCName:   "test-model-storage",
		},
		{
			name: "custom mount path with RWO storage",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "gp2",
						MountPath:    "/custom/models",
					},
				},
			},
			scInfo: &StorageClassInfo{
				Name:       "gp2",
				Type:       "block",
				AccessMode: corev1.ReadWriteOnce,
			},
			expectedMountPath: "/custom/models",
			expectedPVCName:   "test-model-storage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).Build()

			provider, err := NewStorageClassProvider(client, scheme, tt.modelRepo, tt.scInfo)
			require.NoError(t, err)
			require.NotNil(t, provider)

			assert.Equal(t, tt.expectedMountPath, provider.GetMountPath())
			assert.Equal(t, tt.expectedPVCName, provider.pvcName)
		})
	}
}

func TestStorageClassProvider_ValidateConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name        string
		modelRepo   *aitrigramv1.ModelRepository
		scInfo      *StorageClassInfo
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "50Gi",
						StorageClass: "nfs-client",
					},
				},
			},
			scInfo: &StorageClassInfo{
				Name:       "nfs-client",
				AccessMode: corev1.ReadWriteMany,
			},
			wantErr: false,
		},
		{
			name: "missing size",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						StorageClass: "nfs-client",
					},
				},
			},
			scInfo: &StorageClassInfo{
				Name:       "nfs-client",
				AccessMode: corev1.ReadWriteMany,
			},
			wantErr:     true,
			errContains: "storage size is required",
		},
		{
			name: "invalid size format",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "invalid-size",
						StorageClass: "nfs-client",
					},
				},
			},
			scInfo: &StorageClassInfo{
				Name:       "nfs-client",
				AccessMode: corev1.ReadWriteMany,
			},
			wantErr:     true,
			errContains: "invalid size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).Build()

			provider, err := NewStorageClassProvider(client, scheme, tt.modelRepo, tt.scInfo)
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

func TestStorageClassProvider_PrepareDownloadVolume(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
		Spec: aitrigramv1.ModelRepositorySpec{
			Storage: aitrigramv1.ModelStorage{
				Size:         "10Gi",
				StorageClass: "nfs-client",
				MountPath:    "/data/models",
			},
		},
	}

	scInfo := &StorageClassInfo{
		Name:       "nfs-client",
		AccessMode: corev1.ReadWriteMany,
	}

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	provider, err := NewStorageClassProvider(client, scheme, modelRepo, scInfo)
	require.NoError(t, err)

	volume, mount, err := provider.PrepareDownloadVolume()
	require.NoError(t, err)
	require.NotNil(t, volume)
	require.NotNil(t, mount)

	// Verify volume configuration
	assert.Equal(t, "model-storage", volume.Name)
	assert.NotNil(t, volume.VolumeSource.PersistentVolumeClaim)
	assert.Equal(t, "test-model-storage", volume.VolumeSource.PersistentVolumeClaim.ClaimName)

	// Verify mount configuration
	assert.Equal(t, "model-storage", mount.Name)
	assert.Equal(t, "/data/models", mount.MountPath)
}

func TestStorageClassProvider_CreateStorage(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name         string
		modelRepo    *aitrigramv1.ModelRepository
		scInfo       *StorageClassInfo
		existingPVC  *corev1.PersistentVolumeClaim
		expectCreate bool
		expectedMode corev1.PersistentVolumeAccessMode
	}{
		{
			name: "create new RWX PVC",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "nfs-client",
					},
				},
			},
			scInfo: &StorageClassInfo{
				Name:       "nfs-client",
				AccessMode: corev1.ReadWriteMany,
			},
			expectCreate: true,
			expectedMode: corev1.ReadWriteMany,
		},
		{
			name: "create new RWO PVC",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "gp2",
					},
				},
			},
			scInfo: &StorageClassInfo{
				Name:       "gp2",
				AccessMode: corev1.ReadWriteOnce,
			},
			expectCreate: true,
			expectedMode: corev1.ReadWriteOnce,
		},
		{
			name: "PVC already exists",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "nfs-client",
					},
				},
			},
			scInfo: &StorageClassInfo{
				Name:       "nfs-client",
				AccessMode: corev1.ReadWriteMany,
			},
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model-storage",
					Namespace: "default",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				},
			},
			expectCreate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.existingPVC != nil {
				clientBuilder = clientBuilder.WithObjects(tt.existingPVC)
			}
			client := clientBuilder.Build()

			provider, err := NewStorageClassProvider(client, scheme, tt.modelRepo, tt.scInfo)
			require.NoError(t, err)

			err = provider.CreateStorage(context.Background(), "default")
			require.NoError(t, err)

			// Verify PVC was created or already exists
			pvc := &corev1.PersistentVolumeClaim{}
			pvcKey := types.NamespacedName{Name: "test-model-storage", Namespace: "default"}
			err = client.Get(context.Background(), pvcKey, pvc)
			require.NoError(t, err)

			if tt.expectCreate {
				assert.Equal(t, "test-model-storage", pvc.Name)
				assert.Contains(t, pvc.Spec.AccessModes, tt.expectedMode)
				assert.Equal(t, resource.MustParse("10Gi"), pvc.Spec.Resources.Requests[corev1.ResourceStorage])

				// Verify labels
				assert.Equal(t, "test-model", pvc.Labels["aitrigram.io/model-repo"])
			}
		})
	}
}

func TestStorageClassProvider_IsShared(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name         string
		scInfo       *StorageClassInfo
		expectShared bool
	}{
		{
			name: "RWX is shared",
			scInfo: &StorageClassInfo{
				AccessMode: corev1.ReadWriteMany,
			},
			expectShared: true,
		},
		{
			name: "RWO is not shared",
			scInfo: &StorageClassInfo{
				AccessMode: corev1.ReadWriteOnce,
			},
			expectShared: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modelRepo := &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "test-sc",
					},
				},
			}

			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			provider, err := NewStorageClassProvider(client, scheme, modelRepo, tt.scInfo)
			require.NoError(t, err)

			shared := provider.IsShared()
			assert.Equal(t, tt.expectShared, shared)
		})
	}
}

func TestStorageClassProvider_GetStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name            string
		existingPVC     *corev1.PersistentVolumeClaim
		expectedReady   bool
		expectedMessage string
	}{
		{
			name:            "PVC not found",
			expectedReady:   false,
			expectedMessage: "PVC not created yet",
		},
		{
			name: "PVC pending",
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model-storage",
					Namespace: "default",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimPending,
				},
			},
			expectedReady:   false,
			expectedMessage: "PVC is pending, waiting for provisioning",
		},
		{
			name: "PVC bound",
			existingPVC: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model-storage",
					Namespace: "default",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Phase: corev1.ClaimBound,
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			expectedReady:   true,
			expectedMessage: "PVC is bound and ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.existingPVC != nil {
				clientBuilder = clientBuilder.WithObjects(tt.existingPVC)
			}
			client := clientBuilder.Build()

			modelRepo := &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "nfs-client",
					},
				},
			}

			scInfo := &StorageClassInfo{
				Name:       "nfs-client",
				AccessMode: corev1.ReadWriteMany,
			}

			provider, err := NewStorageClassProvider(client, scheme, modelRepo, scInfo)
			require.NoError(t, err)

			status, err := provider.GetStatus(context.Background(), "default")
			require.NoError(t, err)
			require.NotNil(t, status)

			assert.Equal(t, tt.expectedReady, status.Ready)
			assert.Contains(t, status.Message, tt.expectedMessage)

			if tt.existingPVC != nil && tt.existingPVC.Status.Phase == corev1.ClaimBound {
				assert.Equal(t, "10Gi", status.Capacity)
			}
		})
	}
}

func TestStorageClassProvider_GetNodeAffinity(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aitrigramv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name           string
		scInfo         *StorageClassInfo
		modelRepo      *aitrigramv1.ModelRepository
		expectAffinity bool
		expectedNode   string
	}{
		{
			name: "RWX has no node affinity",
			scInfo: &StorageClassInfo{
				AccessMode: corev1.ReadWriteMany,
			},
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "nfs-client",
					},
				},
			},
			expectAffinity: false,
		},
		{
			name: "RWO without bound node has no affinity",
			scInfo: &StorageClassInfo{
				AccessMode: corev1.ReadWriteOnce,
			},
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "gp2",
					},
				},
			},
			expectAffinity: false,
		},
		{
			name: "RWO with bound node has affinity",
			scInfo: &StorageClassInfo{
				AccessMode: corev1.ReadWriteOnce,
			},
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Spec: aitrigramv1.ModelRepositorySpec{
					Storage: aitrigramv1.ModelStorage{
						Size:         "10Gi",
						StorageClass: "gp2",
					},
				},
				Status: aitrigramv1.ModelRepositoryStatus{
					Storage: &aitrigramv1.StorageStatus{
						BoundNodeName: "node-1",
					},
				},
			},
			expectAffinity: true,
			expectedNode:   "node-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			provider, err := NewStorageClassProvider(client, scheme, tt.modelRepo, tt.scInfo)
			require.NoError(t, err)

			affinity := provider.GetNodeAffinity()

			if !tt.expectAffinity {
				assert.Nil(t, affinity)
				return
			}

			require.NotNil(t, affinity)
			require.NotNil(t, affinity.RequiredDuringSchedulingIgnoredDuringExecution)
			require.Len(t, affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms, 1)

			term := affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0]
			require.Len(t, term.MatchExpressions, 1)

			assert.Equal(t, "kubernetes.io/hostname", term.MatchExpressions[0].Key)
			assert.Equal(t, corev1.NodeSelectorOpIn, term.MatchExpressions[0].Operator)
			assert.Contains(t, term.MatchExpressions[0].Values, tt.expectedNode)
		})
	}
}
