package storage

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

// NFSProvider implements storage using NFS
type NFSProvider struct {
	modelRepo *aitrigramv1.ModelRepository
	spec      *aitrigramv1.NFSStorageSpec
	mountPath string
}

// NewNFSProvider creates a new NFS storage provider
func NewNFSProvider(
	modelRepo *aitrigramv1.ModelRepository,
	spec *aitrigramv1.NFSStorageSpec,
	mountPath string,
) (*NFSProvider, error) {
	if spec == nil {
		return nil, fmt.Errorf("NFS storage spec is required")
	}

	mountPath = getOrDefaultMountPath(mountPath)

	return &NFSProvider{
		modelRepo: modelRepo,
		spec:      spec,
		mountPath: mountPath,
	}, nil
}

// GetType returns the storage type
func (p *NFSProvider) GetType() aitrigramv1.StorageType {
	return aitrigramv1.StorageTypeNFS
}

// ValidateConfig validates the NFS configuration
func (p *NFSProvider) ValidateConfig() error {
	if p.spec.Server == "" {
		return fmt.Errorf("NFS server is required")
	}

	if p.spec.Path == "" {
		return fmt.Errorf("NFS path is required")
	}

	// Warn if ReadOnly is true (download jobs need write access)
	if p.spec.ReadOnly {
		return fmt.Errorf("NFS ReadOnly is set to true, but download jobs require write access")
	}

	return nil
}

// PrepareDownloadVolume prepares volume for download job
func (p *NFSProvider) PrepareDownloadVolume() (*corev1.Volume, *corev1.VolumeMount, error) {
	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			NFS: &corev1.NFSVolumeSource{
				Server:   p.spec.Server,
				Path:     p.spec.Path,
				ReadOnly: false, // Download job needs write access
			},
		},
	}

	mount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: p.mountPath,
	}

	return volume, mount, nil
}

// GetReadOnlyVolumeSource returns a read-only volume source
func (p *NFSProvider) GetReadOnlyVolumeSource() (*corev1.VolumeSource, error) {
	return &corev1.VolumeSource{
		NFS: &corev1.NFSVolumeSource{
			Server:   p.spec.Server,
			Path:     p.spec.Path,
			ReadOnly: true, // LLMEngine pods should mount read-only
		},
	}, nil
}

// GetMountPath returns the mount path
func (p *NFSProvider) GetMountPath() string {
	return p.mountPath
}

// GetNodeAffinity returns node affinity (nil for NFS - accessible from any node)
func (p *NFSProvider) GetNodeAffinity() *corev1.NodeAffinity {
	return nil // NFS is network storage, accessible from any node
}

// IsShared returns true (NFS is always shared)
func (p *NFSProvider) IsShared() bool {
	return true
}

// CreateStorage is a no-op for NFS (no provisioning needed)
func (p *NFSProvider) CreateStorage(ctx context.Context, namespace string) error {
	// NFS doesn't require any provisioning from the operator
	// The NFS server and export should already exist
	return nil
}

// GetStatus returns the status (always ready for NFS)
// namespace is the namespace where the controller is running (typically aitrigram-system)
func (p *NFSProvider) GetStatus(ctx context.Context, namespace string) (*StorageStatus, error) {
	return &StorageStatus{
		Ready:   true,
		Message: "NFS storage is ready",
	}, nil
}

// Cleanup is a no-op for NFS
// namespace is the namespace where the controller is running (typically aitrigram-system)
func (p *NFSProvider) Cleanup(ctx context.Context, namespace string) error {
	// We don't delete NFS exports
	return nil
}
