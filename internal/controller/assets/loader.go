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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
	ScriptFile  string               `yaml:"scriptFile"`
	Interpreter string               `yaml:"interpreter"`
	Environment []EnvVar             `yaml:"environment,omitempty"`
	SecretRefs  []SecretRef          `yaml:"secretRefs,omitempty"`
	Resources   ResourceRequirements `yaml:"resources,omitempty"`
}

// EnvVar represents an environment variable
type EnvVar struct {
	Name      string `yaml:"name"`
	Value     string `yaml:"value,omitempty"`
	ValueFrom string `yaml:"valueFrom,omitempty"` // "mountPath" or actual value
}

// SecretRef references a secret for authentication
type SecretRef struct {
	Name      string `yaml:"name"`
	Optional  bool   `yaml:"optional"`
	SecretKey string `yaml:"secretKey"`
}

// ResourceRequirements specifies resource requirements for a job
type ResourceRequirements struct {
	Requests ResourceList `yaml:"requests,omitempty"`
	Limits   ResourceList `yaml:"limits,omitempty"`
}

// ResourceList is a map of resource name to quantity
type ResourceList struct {
	Memory string `yaml:"memory,omitempty"`
	CPU    string `yaml:"cpu,omitempty"`
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

// ToK8sResourceRequirements converts ResourceRequirements to Kubernetes ResourceRequirements
func (rr *ResourceRequirements) ToK8sResourceRequirements() (*corev1.ResourceRequirements, error) {
	k8sReqs := &corev1.ResourceRequirements{
		Requests: corev1.ResourceList{},
		Limits:   corev1.ResourceList{},
	}

	// Parse requests
	if rr.Requests.Memory != "" {
		qty, err := resource.ParseQuantity(rr.Requests.Memory)
		if err != nil {
			return nil, fmt.Errorf("invalid memory request: %w", err)
		}
		k8sReqs.Requests[corev1.ResourceMemory] = qty
	}
	if rr.Requests.CPU != "" {
		qty, err := resource.ParseQuantity(rr.Requests.CPU)
		if err != nil {
			return nil, fmt.Errorf("invalid CPU request: %w", err)
		}
		k8sReqs.Requests[corev1.ResourceCPU] = qty
	}

	// Parse limits
	if rr.Limits.Memory != "" {
		qty, err := resource.ParseQuantity(rr.Limits.Memory)
		if err != nil {
			return nil, fmt.Errorf("invalid memory limit: %w", err)
		}
		k8sReqs.Limits[corev1.ResourceMemory] = qty
	}
	if rr.Limits.CPU != "" {
		qty, err := resource.ParseQuantity(rr.Limits.CPU)
		if err != nil {
			return nil, fmt.Errorf("invalid CPU limit: %w", err)
		}
		k8sReqs.Limits[corev1.ResourceCPU] = qty
	}

	return k8sReqs, nil
}
