package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

func TestHostPathProvider_GetType(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.HostPathStorageSpec{
		Path: "/mnt/models",
	}

	provider, err := NewHostPathProvider(modelRepo, spec, "")
	require.NoError(t, err)

	assert.Equal(t, aitrigramv1.StorageTypeHostPath, provider.GetType())
}

func TestHostPathProvider_ValidateConfig(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	tests := []struct {
		name        string
		spec        *aitrigramv1.HostPathStorageSpec
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config",
			spec: &aitrigramv1.HostPathStorageSpec{
				Path: "/mnt/models",
			},
			wantErr: false,
		},
		{
			name: "valid config with type",
			spec: &aitrigramv1.HostPathStorageSpec{
				Path: "/mnt/models",
				Type: hostPathTypePtr(corev1.HostPathDirectory),
			},
			wantErr: false,
		},
		{
			name: "valid config with node name",
			spec: &aitrigramv1.HostPathStorageSpec{
				Path:     "/mnt/models",
				NodeName: stringPtr("worker-1"),
			},
			wantErr: false,
		},
		{
			name:        "missing path",
			spec:        &aitrigramv1.HostPathStorageSpec{},
			wantErr:     true,
			errContains: "path is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewHostPathProvider(modelRepo, tt.spec, "")
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

func TestHostPathProvider_PrepareDownloadVolume(t *testing.T) {
	tests := []struct {
		name          string
		modelRepo     *aitrigramv1.ModelRepository
		spec          *aitrigramv1.HostPathStorageSpec
		mountPath     string
		wantVolName   string
		wantMountPath string
		checkVolume   func(*testing.T, *corev1.Volume)
	}{
		{
			name: "basic HostPath configuration",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
			},
			spec: &aitrigramv1.HostPathStorageSpec{
				Path: "/mnt/models",
			},
			mountPath:     "",
			wantVolName:   "model-storage",
			wantMountPath: DefaultModelStoragePath,
			checkVolume: func(t *testing.T, vol *corev1.Volume) {
				require.NotNil(t, vol.VolumeSource.HostPath)
				assert.Equal(t, "/mnt/models", vol.VolumeSource.HostPath.Path)
				assert.Equal(t, corev1.HostPathDirectoryOrCreate, *vol.VolumeSource.HostPath.Type)
			},
		},
		{
			name: "HostPath with custom type",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
			},
			spec: &aitrigramv1.HostPathStorageSpec{
				Path: "/mnt/models",
				Type: hostPathTypePtr(corev1.HostPathDirectory),
			},
			mountPath:     "/custom/path",
			wantVolName:   "model-storage",
			wantMountPath: "/custom/path",
			checkVolume: func(t *testing.T, vol *corev1.Volume) {
				require.NotNil(t, vol.VolumeSource.HostPath)
				assert.Equal(t, corev1.HostPathDirectory, *vol.VolumeSource.HostPath.Type)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewHostPathProvider(tt.modelRepo, tt.spec, tt.mountPath)
			require.NoError(t, err)

			volume, mount, err := provider.PrepareDownloadVolume()
			require.NoError(t, err)
			require.NotNil(t, volume)
			require.NotNil(t, mount)

			assert.Equal(t, tt.wantVolName, volume.Name)
			if tt.checkVolume != nil {
				tt.checkVolume(t, volume)
			}

			assert.Equal(t, tt.wantVolName, mount.Name)
			assert.Equal(t, tt.wantMountPath, mount.MountPath)
			assert.False(t, mount.ReadOnly)
		})
	}
}

func TestHostPathProvider_GetReadOnlyVolumeSource(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.HostPathStorageSpec{
		Path: "/mnt/models",
		Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
	}

	provider, err := NewHostPathProvider(modelRepo, spec, "")
	require.NoError(t, err)

	volumeSource, err := provider.GetReadOnlyVolumeSource()
	require.NoError(t, err)
	require.NotNil(t, volumeSource)
	require.NotNil(t, volumeSource.HostPath)

	assert.Equal(t, "/mnt/models", volumeSource.HostPath.Path)
	assert.Equal(t, corev1.HostPathDirectory, *volumeSource.HostPath.Type)
}

func TestHostPathProvider_IsShared(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.HostPathStorageSpec{
		Path: "/mnt/models",
	}

	provider, err := NewHostPathProvider(modelRepo, spec, "")
	require.NoError(t, err)

	assert.False(t, provider.IsShared())
}

func TestHostPathProvider_GetNodeAffinity(t *testing.T) {
	tests := []struct {
		name         string
		modelRepo    *aitrigramv1.ModelRepository
		spec         *aitrigramv1.HostPathStorageSpec
		wantAffinity bool
		wantNode     string
	}{
		{
			name: "no bound node, no node name",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Status: aitrigramv1.ModelRepositoryStatus{},
			},
			spec: &aitrigramv1.HostPathStorageSpec{
				Path: "/mnt/models",
			},
			wantAffinity: false,
		},
		{
			name: "no bound node, with node name",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Status: aitrigramv1.ModelRepositoryStatus{},
			},
			spec: &aitrigramv1.HostPathStorageSpec{
				Path:     "/mnt/models",
				NodeName: stringPtr("worker-1"),
			},
			wantAffinity: true,
			wantNode:     "worker-1",
		},
		{
			name: "with bound node",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Status: aitrigramv1.ModelRepositoryStatus{
					BoundNodeName: "worker-2",
				},
			},
			spec: &aitrigramv1.HostPathStorageSpec{
				Path: "/mnt/models",
			},
			wantAffinity: true,
			wantNode:     "worker-2",
		},
		{
			name: "bound node overrides node name",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Status: aitrigramv1.ModelRepositoryStatus{
					BoundNodeName: "worker-3",
				},
			},
			spec: &aitrigramv1.HostPathStorageSpec{
				Path:     "/mnt/models",
				NodeName: stringPtr("worker-1"),
			},
			wantAffinity: true,
			wantNode:     "worker-3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewHostPathProvider(tt.modelRepo, tt.spec, "")
			require.NoError(t, err)

			affinity := provider.GetNodeAffinity()

			if tt.wantAffinity {
				require.NotNil(t, affinity)
				require.NotNil(t, affinity.RequiredDuringSchedulingIgnoredDuringExecution)
				require.Len(t, affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms, 1)

				term := affinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0]
				require.NotEmpty(t, term.MatchExpressions)

				found := false
				for _, expr := range term.MatchExpressions {
					if expr.Key == "kubernetes.io/hostname" {
						found = true
						assert.Equal(t, corev1.NodeSelectorOpIn, expr.Operator)
						assert.Contains(t, expr.Values, tt.wantNode)
						break
					}
				}
				assert.True(t, found, "Expected to find kubernetes.io/hostname in node affinity")
			} else {
				assert.Nil(t, affinity)
			}
		})
	}
}

func TestHostPathProvider_CreateStorage(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.HostPathStorageSpec{
		Path: "/mnt/models",
	}

	provider, err := NewHostPathProvider(modelRepo, spec, "")
	require.NoError(t, err)

	err = provider.CreateStorage(context.TODO(), "test-namespace")
	assert.NoError(t, err)
}

func TestHostPathProvider_Cleanup(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.HostPathStorageSpec{
		Path: "/mnt/models",
	}

	provider, err := NewHostPathProvider(modelRepo, spec, "")
	require.NoError(t, err)

	err = provider.Cleanup(context.TODO(), "aitrigram-system")
	assert.NoError(t, err)
}

func TestHostPathProvider_GetMountPath(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	tests := []struct {
		name      string
		spec      *aitrigramv1.HostPathStorageSpec
		mountPath string
		wantPath  string
	}{
		{
			name: "default mount path",
			spec: &aitrigramv1.HostPathStorageSpec{
				Path: "/mnt/models",
			},
			mountPath: "",
			wantPath:  DefaultModelStoragePath,
		},
		{
			name: "custom mount path",
			spec: &aitrigramv1.HostPathStorageSpec{
				Path: "/mnt/models",
			},
			mountPath: "/custom/path",
			wantPath:  "/custom/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewHostPathProvider(modelRepo, tt.spec, tt.mountPath)
			require.NoError(t, err)

			assert.Equal(t, tt.wantPath, provider.GetMountPath())
		})
	}
}

func TestHostPathProvider_GetStatus(t *testing.T) {
	tests := []struct {
		name      string
		modelRepo *aitrigramv1.ModelRepository
		spec      *aitrigramv1.HostPathStorageSpec
		wantNode  string
	}{
		{
			name: "no bound node",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Status: aitrigramv1.ModelRepositoryStatus{},
			},
			spec: &aitrigramv1.HostPathStorageSpec{
				Path: "/mnt/models",
			},
			wantNode: "",
		},
		{
			name: "with bound node",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
				Status: aitrigramv1.ModelRepositoryStatus{
					BoundNodeName: "worker-1",
				},
			},
			spec: &aitrigramv1.HostPathStorageSpec{
				Path: "/mnt/models",
			},
			wantNode: "worker-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewHostPathProvider(tt.modelRepo, tt.spec, "")
			require.NoError(t, err)

			status, err := provider.GetStatus(context.TODO(), "aitrigram-system")
			require.NoError(t, err)
			require.NotNil(t, status)

			assert.True(t, status.Ready)
			assert.Contains(t, status.Message, "HostPath is ready")
			assert.Equal(t, tt.wantNode, status.BoundNodeName)
		})
	}
}

func hostPathTypePtr(t corev1.HostPathType) *corev1.HostPathType {
	return &t
}

func stringPtr(s string) *string {
	return &s
}
