package storage

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

// HostPathProvider implements storage using HostPath
type HostPathProvider struct {
	modelRepo *aitrigramv1.ModelRepository
	spec      *aitrigramv1.HostPathStorageSpec
	mountPath string
}

// NewHostPathProvider creates a new HostPath storage provider
func NewHostPathProvider(
	modelRepo *aitrigramv1.ModelRepository,
	spec *aitrigramv1.HostPathStorageSpec,
	mountPath string,
) (*HostPathProvider, error) {
	if spec == nil {
		return nil, fmt.Errorf("HostPath storage spec is required")
	}

	mountPath = getOrDefaultMountPath(mountPath)

	return &HostPathProvider{
		modelRepo: modelRepo,
		spec:      spec,
		mountPath: mountPath,
	}, nil
}

// GetType returns the storage type
func (p *HostPathProvider) GetType() aitrigramv1.StorageType {
	return aitrigramv1.StorageTypeHostPath
}

// ValidateConfig validates the HostPath configuration
func (p *HostPathProvider) ValidateConfig() error {
	if p.spec.Path == "" {
		return fmt.Errorf("HostPath path is required")
	}

	// Type validation is already handled by CRD enum validation
	// No need to re-validate here

	return nil
}

// PrepareDownloadVolume prepares volume for download job
func (p *HostPathProvider) PrepareDownloadVolume() (*corev1.Volume, *corev1.VolumeMount, error) {
	pathType := p.getPathType()

	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: p.spec.Path,
				Type: &pathType,
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
func (p *HostPathProvider) GetReadOnlyVolumeSource() (*corev1.VolumeSource, error) {
	pathType := corev1.HostPathDirectory

	return &corev1.VolumeSource{
		HostPath: &corev1.HostPathVolumeSource{
			Path: p.spec.Path,
			Type: &pathType, // Use Directory for read access
		},
	}, nil
}

// GetMountPath returns the mount path
func (p *HostPathProvider) GetMountPath() string {
	return p.mountPath
}

// GetNodeAffinity returns node affinity for HostPath (always required)
func (p *HostPathProvider) GetNodeAffinity() *corev1.NodeAffinity {
	// Determine which node to bind to
	var nodeName string

	// Priority 1: Use bound node if download already happened (reality trumps intent)
	if p.modelRepo.Status.BoundNodeName != "" {
		// Download job already ran, use the bound node
		nodeName = p.modelRepo.Status.BoundNodeName
	} else if p.spec.NodeName != nil {
		// User specified a node hint for download
		nodeName = *p.spec.NodeName
	} else {
		// No node specified yet, return nil
		// The download job can run on any node, and we'll update BoundNodeName after it completes
		return nil
	}

	// Create node affinity for the specific node
	return &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      "kubernetes.io/hostname",
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{nodeName},
						},
					},
				},
			},
		},
	}
}

// IsShared returns false (HostPath is always node-local)
func (p *HostPathProvider) IsShared() bool {
	return false
}

// CreateStorage is a no-op for HostPath
func (p *HostPathProvider) CreateStorage(ctx context.Context, namespace string) error {
	// HostPath doesn't require provisioning
	// The path should exist or be created by DirectoryOrCreate type
	return nil
}

// GetStatus returns the status
// namespace is the namespace where the controller is running (typically aitrigram-system)
func (p *HostPathProvider) GetStatus(ctx context.Context, namespace string) (*StorageStatus, error) {
	status := &StorageStatus{
		Ready:   true,
		Message: "HostPath is ready",
	}

	// Include bound node information if available
	if p.modelRepo.Status.BoundNodeName != "" {
		status.BoundNodeName = p.modelRepo.Status.BoundNodeName
		status.Message = fmt.Sprintf("HostPath is ready on node %s", status.BoundNodeName)
	}

	return status, nil
}

// Cleanup is a no-op for HostPath
// namespace is the namespace where the controller is running (typically aitrigram-system)
func (p *HostPathProvider) Cleanup(ctx context.Context, namespace string) error {
	// We don't delete host paths
	return nil
}

// getPathType returns the HostPath type, defaulting to DirectoryOrCreate
func (p *HostPathProvider) getPathType() corev1.HostPathType {
	if p.spec.Type != nil {
		return *p.spec.Type
	}
	return corev1.HostPathDirectoryOrCreate
}
