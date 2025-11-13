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

package storage

const (
	// DefaultModelStoragePath is the default mount path for model storage
	DefaultModelStoragePath = "/data/models"

	// DefaultPVCNameSuffix is appended to ModelRepository name for auto-created PVCs
	DefaultPVCNameSuffix = "-pvc"
)

// getOrDefaultMountPath returns the mount path, or default if empty
func getOrDefaultMountPath(mountPath string) string {
	if mountPath == "" {
		return DefaultModelStoragePath
	}
	return mountPath
}
