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

package assets

import (
	"fmt"
	"io/fs"
)

// LoadModelRepositoryScript loads a script file for a given origin
func LoadModelRepositoryScript(origin, scriptFile string) (string, error) {
	originFS, err := GetModelRepositoryOriginAssets(origin)
	if err != nil {
		return "", fmt.Errorf("failed to get assets for origin %s: %w", origin, err)
	}

	scriptData, err := fs.ReadFile(originFS, scriptFile)
	if err != nil {
		return "", fmt.Errorf("failed to read script %s: %w", scriptFile, err)
	}

	return string(scriptData), nil
}
