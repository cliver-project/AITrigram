// Package storage provides storage provisioning for ModelRepository.
// All storage goes through StorageClassProvider which creates a PVC
// backed by the detected Kubernetes StorageClass.
package storage

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

// StorageStatus represents storage state
type StorageStatus struct {
	Ready         bool
	Message       string
	BoundNodeName string
	Capacity      string
}

// ProviderFactory discovers the StorageClass and creates a StorageClassProvider
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

// CreateProvider discovers the StorageClass and creates a StorageClassProvider that provisions a PVC
func (f *ProviderFactory) CreateProvider(ctx context.Context, modelRepo *aitrigramv1.ModelRepository) (*StorageClassProvider, error) {
	storage := &modelRepo.Spec.Storage
	if storage.Size == "" {
		return nil, fmt.Errorf("storage size is required")
	}

	discovery := NewStorageClassDiscovery(f.client)
	scInfo, err := discovery.SelectBestStorageClass(ctx, storage.StorageClass)
	if err != nil {
		return nil, fmt.Errorf("failed to select storage class: %w", err)
	}

	return NewStorageClassProvider(f.client, f.scheme, modelRepo, scInfo)
}
