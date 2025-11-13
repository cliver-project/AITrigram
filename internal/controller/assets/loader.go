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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// AssetConfig represents the configuration for model repository assets
type AssetConfig struct {
	APIVersion  string            `yaml:"apiVersion"`
	Kind        string            `yaml:"kind"`
	Metadata    AssetMetadata     `yaml:"metadata"`
	Image       string            `yaml:"image"`
	Download    JobConfig         `yaml:"download"`
	Cleanup     JobConfig         `yaml:"cleanup"`
	VolumeMount VolumeMountConfig `yaml:"volumeMount"`
}

// AssetMetadata contains metadata about the asset
type AssetMetadata struct {
	Name   string `yaml:"name"`
	Origin string `yaml:"origin"`
}

// JobConfig represents configuration for a job (download or cleanup)
type JobConfig struct {
	ScriptFile      string                       `yaml:"scriptFile"`
	Interpreter     string                       `yaml:"interpreter"`
	Environment     []EnvVar                     `yaml:"environment,omitempty"`
	Resources       *corev1.ResourceRequirements `yaml:"resources,omitempty"`
	SecurityContext *corev1.SecurityContext      `yaml:"securityContext,omitempty"`
}

// EnvVar represents an environment variable with flexible value resolution
type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value,omitempty"`
	// ValueFrom specifies where to get the value from at runtime:
	//   - "mountPath": Use the storage mount path
	//   - "secret:hfToken": Use HFTokenSecretRef from CRD
	// If both Value and ValueFrom are empty, the variable name is used to lookup from params
	ValueFrom string `yaml:"valueFrom,omitempty"`
}

// IsSecretRef returns true if this env var references a secret
func (e *EnvVar) IsSecretRef() bool {
	return strings.HasPrefix(e.ValueFrom, "secret:")
}

// GetSecretType returns the secret type (e.g., "hfToken" from "secret:hfToken")
func (e *EnvVar) GetSecretType() string {
	if e.IsSecretRef() {
		return strings.TrimPrefix(e.ValueFrom, "secret:")
	}
	return ""
}

// VolumeMountConfig specifies volume mount configuration
type VolumeMountConfig struct {
	DefaultPath string `yaml:"defaultPath"`
	Name        string `yaml:"name"`
	ReadOnly    bool   `yaml:"readOnly"`
}

// LoadAssetConfig loads the config.yaml for a given origin
func LoadAssetConfig(origin string) (*AssetConfig, error) {
	originFS, err := GetOriginAssets(origin)
	if err != nil {
		return nil, fmt.Errorf("failed to get assets for origin %s: %w", origin, err)
	}

	configData, err := fs.ReadFile(originFS, "config.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read config.yaml: %w", err)
	}

	var config AssetConfig
	if err := yaml.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config.yaml: %w", err)
	}

	return &config, nil
}

// LoadScript loads a script file for a given origin
func LoadScript(origin, scriptFile string) (string, error) {
	originFS, err := GetOriginAssets(origin)
	if err != nil {
		return "", fmt.Errorf("failed to get assets for origin %s: %w", origin, err)
	}

	scriptData, err := fs.ReadFile(originFS, scriptFile)
	if err != nil {
		return "", fmt.Errorf("failed to read script %s: %w", scriptFile, err)
	}

	return string(scriptData), nil
}

// Validate validates the asset configuration
func (ac *AssetConfig) Validate() error {
	if ac.APIVersion == "" {
		return fmt.Errorf("apiVersion is required")
	}
	if ac.Kind != "AssetConfig" {
		return fmt.Errorf("kind must be 'AssetConfig', got %s", ac.Kind)
	}
	if ac.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if ac.Metadata.Origin == "" {
		return fmt.Errorf("metadata.origin is required")
	}
	if ac.Image == "" {
		return fmt.Errorf("image is required")
	}
	if ac.Download.ScriptFile == "" {
		return fmt.Errorf("download.scriptFile is required")
	}
	if ac.Download.Interpreter == "" {
		return fmt.Errorf("download.interpreter is required")
	}
	if ac.Cleanup.ScriptFile == "" {
		return fmt.Errorf("cleanup.scriptFile is required")
	}
	if ac.Cleanup.Interpreter == "" {
		return fmt.Errorf("cleanup.interpreter is required")
	}
	if ac.VolumeMount.DefaultPath == "" {
		return fmt.Errorf("volumeMount.defaultPath is required")
	}
	if ac.VolumeMount.Name == "" {
		return fmt.Errorf("volumeMount.name is required")
	}
	return nil
}
