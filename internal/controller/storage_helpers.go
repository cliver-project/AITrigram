/*
Copyright 2025 Red Hat, Inc.

Authors: Lin Gao <lgao@redhat.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/storage"
)

// requiresNodeAffinity determines if the storage requires node affinity (single-node storage)
// Returns true for non-shared storage (HostPath or ReadWriteOnce PVC)
// Deprecated: Use storage.Provider interface directly instead
func requiresNodeAffinity(modelRepo *aitrigramv1.ModelRepository) bool {
	// Create a temporary provider factory to check storage type
	// This is a fallback for code that hasn't been migrated yet
	factory := storage.NewProviderFactory(nil, nil)
	provider, err := factory.CreateProvider(modelRepo)
	if err != nil {
		// Fallback to old logic if provider creation fails
		return modelRepo.Spec.Storage.HostPath != nil ||
			modelRepo.Spec.Storage.PersistentVolumeClaim != nil
	}

	// Check if provider implements NodeBoundProvider and is not shared
	if nodeBound, ok := provider.(storage.NodeBoundProvider); ok {
		return !nodeBound.IsShared()
	}

	return false
}

// getStorageType returns a human-readable storage type for logging
// Deprecated: Use storage.Provider.GetType() directly instead
func getStorageType(modelRepo *aitrigramv1.ModelRepository) string {
	// Create a temporary provider factory to get storage type
	// This is a fallback for code that hasn't been migrated yet
	factory := storage.NewProviderFactory(nil, nil)
	provider, err := factory.CreateProvider(modelRepo)
	if err != nil {
		return "Unknown"
	}

	return string(provider.GetType())
}

// createStorageProvider creates a storage provider for the given ModelRepository
// This is a helper function for controllers that need to work with storage providers
func createStorageProvider(c client.Client, scheme *runtime.Scheme, modelRepo *aitrigramv1.ModelRepository) (storage.Provider, error) {
	factory := storage.NewProviderFactory(c, scheme)
	return factory.CreateProvider(modelRepo)
}
