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

func TestNFSProvider_GetType(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.NFSStorageSpec{
		Server: "nfs.example.com",
		Path:   "/exports/models",
	}

	provider, err := NewNFSProvider(modelRepo, spec, "")
	require.NoError(t, err)

	assert.Equal(t, aitrigramv1.StorageTypeNFS, provider.GetType())
}

func TestNFSProvider_ValidateConfig(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	tests := []struct {
		name        string
		spec        *aitrigramv1.NFSStorageSpec
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config",
			spec: &aitrigramv1.NFSStorageSpec{
				Server: "nfs.example.com",
				Path:   "/exports/models",
			},
			wantErr: false,
		},
		{
			name: "read-only is invalid for downloads",
			spec: &aitrigramv1.NFSStorageSpec{
				Server:   "nfs.example.com",
				Path:     "/exports/models",
				ReadOnly: true,
			},
			wantErr:     true,
			errContains: "download jobs require write access",
		},
		{
			name: "missing server",
			spec: &aitrigramv1.NFSStorageSpec{
				Path: "/exports/models",
			},
			wantErr:     true,
			errContains: "server is required",
		},
		{
			name: "missing path",
			spec: &aitrigramv1.NFSStorageSpec{
				Server: "nfs.example.com",
			},
			wantErr:     true,
			errContains: "path is required",
		},
		{
			name:        "empty config",
			spec:        &aitrigramv1.NFSStorageSpec{},
			wantErr:     true,
			errContains: "server is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewNFSProvider(modelRepo, tt.spec, "")
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

func TestNFSProvider_PrepareDownloadVolume(t *testing.T) {
	tests := []struct {
		name          string
		modelRepo     *aitrigramv1.ModelRepository
		spec          *aitrigramv1.NFSStorageSpec
		mountPath     string
		wantVolName   string
		wantMountPath string
		checkVolume   func(*testing.T, *corev1.Volume)
	}{
		{
			name: "basic NFS configuration",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "llama2-7b",
				},
			},
			spec: &aitrigramv1.NFSStorageSpec{
				Server: "nfs.example.com",
				Path:   "/exports/models",
			},
			mountPath:     "",
			wantVolName:   "model-storage",
			wantMountPath: DefaultModelStoragePath,
			checkVolume: func(t *testing.T, vol *corev1.Volume) {
				require.NotNil(t, vol.VolumeSource.NFS)
				assert.Equal(t, "nfs.example.com", vol.VolumeSource.NFS.Server)
				assert.Equal(t, "/exports/models", vol.VolumeSource.NFS.Path)
				assert.False(t, vol.VolumeSource.NFS.ReadOnly)
			},
		},
		{
			name: "NFS with custom path",
			modelRepo: &aitrigramv1.ModelRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-model",
				},
			},
			spec: &aitrigramv1.NFSStorageSpec{
				Server:   "nfs.example.com",
				Path:     "/exports/models",
				ReadOnly: false,
			},
			mountPath:     "/custom/path",
			wantVolName:   "model-storage",
			wantMountPath: "/custom/path",
			checkVolume: func(t *testing.T, vol *corev1.Volume) {
				require.NotNil(t, vol.VolumeSource.NFS)
				assert.False(t, vol.VolumeSource.NFS.ReadOnly)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewNFSProvider(tt.modelRepo, tt.spec, tt.mountPath)
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

func TestNFSProvider_GetReadOnlyVolumeSource(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	tests := []struct {
		name   string
		spec   *aitrigramv1.NFSStorageSpec
		wantRO bool
	}{
		{
			name: "writable NFS becomes read-only",
			spec: &aitrigramv1.NFSStorageSpec{
				Server:   "nfs.example.com",
				Path:     "/exports/models",
				ReadOnly: false,
			},
			wantRO: true,
		},
		{
			name: "read-only NFS stays read-only",
			spec: &aitrigramv1.NFSStorageSpec{
				Server:   "nfs.example.com",
				Path:     "/exports/models",
				ReadOnly: true,
			},
			wantRO: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewNFSProvider(modelRepo, tt.spec, "")
			require.NoError(t, err)

			volumeSource, err := provider.GetReadOnlyVolumeSource()
			require.NoError(t, err)
			require.NotNil(t, volumeSource)
			require.NotNil(t, volumeSource.NFS)

			assert.Equal(t, "nfs.example.com", volumeSource.NFS.Server)
			assert.Equal(t, "/exports/models", volumeSource.NFS.Path)
			assert.Equal(t, tt.wantRO, volumeSource.NFS.ReadOnly)
		})
	}
}

func TestNFSProvider_IsShared(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.NFSStorageSpec{
		Server: "nfs.example.com",
		Path:   "/exports/models",
	}

	provider, err := NewNFSProvider(modelRepo, spec, "")
	require.NoError(t, err)

	assert.True(t, provider.IsShared())
}

func TestNFSProvider_GetNodeAffinity(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.NFSStorageSpec{
		Server: "nfs.example.com",
		Path:   "/exports/models",
	}

	provider, err := NewNFSProvider(modelRepo, spec, "")
	require.NoError(t, err)

	affinity := provider.GetNodeAffinity()
	assert.Nil(t, affinity)
}

func TestNFSProvider_CreateStorage(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.NFSStorageSpec{
		Server: "nfs.example.com",
		Path:   "/exports/models",
	}

	provider, err := NewNFSProvider(modelRepo, spec, "")
	require.NoError(t, err)

	err = provider.CreateStorage(context.TODO(), "test-namespace")
	assert.NoError(t, err)
}

func TestNFSProvider_Cleanup(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.NFSStorageSpec{
		Server: "nfs.example.com",
		Path:   "/exports/models",
	}

	provider, err := NewNFSProvider(modelRepo, spec, "")
	require.NoError(t, err)

	err = provider.Cleanup(context.TODO(), "aitrigram-system")
	assert.NoError(t, err)
}

func TestNFSProvider_GetMountPath(t *testing.T) {
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
			mountPath: "/custom/nfs/path",
			wantPath:  "/custom/nfs/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &aitrigramv1.NFSStorageSpec{
				Server: "nfs.example.com",
				Path:   "/exports/models",
			}

			provider, err := NewNFSProvider(modelRepo, spec, tt.mountPath)
			require.NoError(t, err)

			assert.Equal(t, tt.wantPath, provider.GetMountPath())
		})
	}
}

func TestNFSProvider_GetStatus(t *testing.T) {
	modelRepo := &aitrigramv1.ModelRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-model",
		},
	}

	spec := &aitrigramv1.NFSStorageSpec{
		Server: "nfs.example.com",
		Path:   "/exports/models",
	}

	provider, err := NewNFSProvider(modelRepo, spec, "")
	require.NoError(t, err)

	status, err := provider.GetStatus(context.TODO(), "aitrigram-system")
	require.NoError(t, err)
	require.NotNil(t, status)

	assert.True(t, status.Ready)
	assert.Contains(t, status.Message, "NFS storage is ready")
}
