/*
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

package storage

import (
	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

// GetBoundNodeName returns the bound node name from ModelRepository status
func GetBoundNodeName(modelRepo *aitrigramv1.ModelRepository) string {
	if modelRepo.Status.Storage != nil {
		return modelRepo.Status.Storage.BoundNodeName
	}
	return ""
}

// GetStorageType returns the storage type from ModelRepository
func GetStorageType(modelRepo *aitrigramv1.ModelRepository) string {
	if modelRepo.Status.Storage != nil {
		return modelRepo.Status.Storage.StorageClass
	}
	return modelRepo.Spec.Storage.StorageClass
}

// RequiresNodeAffinity checks if the ModelRepository requires node affinity
// RWO storage requires node affinity to ensure pods schedule on the bound node
func RequiresNodeAffinity(modelRepo *aitrigramv1.ModelRepository) bool {
	if modelRepo.Status.Storage == nil {
		return false
	}
	return modelRepo.Status.Storage.AccessMode == "ReadWriteOnce"
}
