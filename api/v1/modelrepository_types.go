/*
Copyright 2025 Lin Gao.

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

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ModelStorage defines a generic storage mount configuration
// This is the unified storage type used across ModelRepository and LLMEngine
type ModelStorage struct {
	// Path is the mount path inside the container
	// +kubebuilder:validation:Required
	Path string `json:"path"`
	// VolumeSource specifies the source of the volume (supports all Kubernetes volume types)
	corev1.VolumeSource `json:",inline"`
}

// ModelOrigin defines the type of model source origin.
// +kubebuilder:validation:Enum=huggingface;gguf;local;ollama
type ModelOrigin string

const (
	ModelOriginHuggingFace ModelOrigin = "huggingface"
	ModelOriginGGUF        ModelOrigin = "gguf"
	ModelOriginLocal       ModelOrigin = "local"
	ModelOriginOllama      ModelOrigin = "ollama"
)

// ModelSource defines the source of the model
type ModelSource struct {
	// Origin specifies the source origin (huggingface, gguf, local, ollama)
	// +kubebuilder:validation:Required
	// +kubebuilder:default:=huggingface
	Origin ModelOrigin `json:"origin"`

	// ModelId is the identifier of the model within the origin
	// +kubebuilder:validation:Required
	ModelId string `json:"modelId"`

	// HFTokenSecretRef is specific to huggingface, other types may have their own way to access the model
	// +optional
	HFTokenSecretRef *corev1.SecretKeySelector `json:"hfTokenSecretRef,omitempty"`
}

// ModelRepositorySpec defines the desired state of ModelRepository
type ModelRepositorySpec struct {
	// ModelName is the identifier within all ModelRepositories in the cluster
	// If not specified, defaults to metadata.name
	// +optional
	ModelName string `json:"modelName,omitempty"`

	// Source defines where to download the model from
	// +kubebuilder:validation:Required
	Source ModelSource `json:"source"`

	// Storage configuration for the model
	// This defines where and how the model files are stored, the LLMEngine may have different storage than this one, although it has option to reuse this storage.
	// +kubebuilder:validation:Required
	// +kubebuilder:default:={path:"/data/models"}
	Storage ModelStorage `json:"storage"`

	// AutoDownload indicates whether to automatically download the model
	// +kubebuilder:validation:Required
	// +kubebuilder:default:=true
	AutoDownload bool `json:"autoDownload"`

	// DownloadImage specifies the container image to use for downloading the model
	// If not specified, a default image will be selected based on the source origin
	// +optional
	DownloadImage string `json:"downloadImage,omitempty"`

	// DownloadScripts specifies custom scripts to run for downloading the model
	// If not specified, default scripts will be used based on the source origin
	// Supports Jinja2 template variables: {{ ModelId }}, {{ ModelName }}, {{ MountPath }}
	// Can be either bash or Python script (auto-detected)
	// +optional
	DownloadScripts string `json:"downloadScripts,omitempty"`

	// DeleteScripts specifies custom scripts to run for deleting the model when the ModelRepository is deleted
	// If not specified, default scripts will be used based on the source origin
	// Supports Jinja2 template variables: {{ ModelId }}, {{ ModelName }}, {{ MountPath }}
	// Can be either bash or Python script (auto-detected)
	// +optional
	DeleteScripts string `json:"deleteScripts,omitempty"`
}

// ModelRepositoryPhase defines the phase of the model repository.
// +kubebuilder:validation:Enum=pending;downloading;downloaded;failed
type ModelRepositoryPhase string

const (
	ModelRepositoryPhasePending     ModelRepositoryPhase = "pending"
	ModelRepositoryPhaseDownloading ModelRepositoryPhase = "downloading"
	ModelRepositoryPhaseDownloaded  ModelRepositoryPhase = "downloaded"
	ModelRepositoryPhaseFailed      ModelRepositoryPhase = "failed"
)

// ModelRepositoryStatus defines the observed state of ModelRepository
type ModelRepositoryStatus struct {
	// Phase represents the current phase of the model download
	// +optional
	Phase ModelRepositoryPhase `json:"phase,omitempty"`

	// Reason provides the reason for the current phase
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message provides additional information about the current phase
	// +optional
	Message string `json:"message,omitempty"`

	// LastUpdated is the timestamp of the last status update
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`

	// Conditions represent the latest available observations of the ModelRepository's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// ModelRepository is the Schema for the modelrepositories API
// ModelRepository is a cluster wide CRD so that models can be shared across namespaces.
type ModelRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelRepositorySpec   `json:"spec,omitempty"`
	Status ModelRepositoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ModelRepositoryList contains a list of ModelRepository
type ModelRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelRepository `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelRepository{}, &ModelRepositoryList{})
}
