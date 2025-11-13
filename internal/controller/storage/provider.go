// Package storage provides storage provider abstractions for ModelRepository
package storage

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

// Provider defines the interface for storage backends
type Provider interface {
	// GetType returns the storage type
	GetType() aitrigramv1.StorageType

	// ValidateConfig validates storage configuration
	ValidateConfig() error

	// PrepareDownloadVolume prepares volume for download job
	// Returns volume and volume mount for the download job
	PrepareDownloadVolume() (*corev1.Volume, *corev1.VolumeMount, error)

	// GetReadOnlyVolumeSource returns a volume source for read-only mounting
	// Used by LLMEngine pods to access models
	GetReadOnlyVolumeSource() (*corev1.VolumeSource, error)

	// GetMountPath returns the path where storage should be mounted
	GetMountPath() string
}

// NodeBoundProvider is implemented by providers that bind to specific nodes
type NodeBoundProvider interface {
	Provider

	// GetNodeAffinity returns node affinity requirements
	// Returns nil if storage is accessible from any node
	GetNodeAffinity() *corev1.NodeAffinity

	// IsShared returns true if storage is accessible from multiple nodes
	IsShared() bool
}

// ProvisionableProvider is implemented by providers that need to provision resources
type ProvisionableProvider interface {
	Provider

	// CreateStorage provisions underlying storage if needed
	// E.g., creates PVC, validates NFS server connectivity, etc.
	CreateStorage(ctx context.Context, namespace string) error

	// Cleanup removes provisioned storage
	// namespace is the namespace where the controller is running (typically aitrigram-system)
	Cleanup(ctx context.Context, namespace string) error

	// GetStatus returns current storage status
	// namespace is the namespace where the controller is running (typically aitrigram-system)
	GetStatus(ctx context.Context, namespace string) (*StorageStatus, error)
}

// StorageStatus represents storage state
type StorageStatus struct {
	// Ready indicates if storage is ready for use
	Ready bool

	// Message provides status details
	Message string

	// BoundNodeName for node-local storage (HostPath, RWO PVC)
	BoundNodeName string

	// Capacity of storage
	Capacity string

	// Used space
	Used string
}

// ProviderFactory creates storage providers
type ProviderFactory struct {
	client client.Client
	scheme *runtime.Scheme
}

// NewProviderFactory creates a new provider factory
func NewProviderFactory(client client.Client, scheme *runtime.Scheme) *ProviderFactory {
	return &ProviderFactory{
		client: client,
		scheme: scheme,
	}
}

// CreateProvider creates a storage provider for the given ModelRepository
func (f *ProviderFactory) CreateProvider(modelRepo *aitrigramv1.ModelRepository) (Provider, error) {
	storage := &modelRepo.Spec.Storage
	if storage.Type == "" {
		return nil, fmt.Errorf("storage type is required")
	}

	switch storage.Type {
	case aitrigramv1.StorageTypePVC:
		return NewPVCProvider(f.client, f.scheme, modelRepo, storage.PersistentVolumeClaim, storage.MountPath)
	case aitrigramv1.StorageTypeNFS:
		return NewNFSProvider(modelRepo, storage.NFS, storage.MountPath)
	case aitrigramv1.StorageTypeHostPath:
		return NewHostPathProvider(modelRepo, storage.HostPath, storage.MountPath)
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storage.Type)
	}
}
