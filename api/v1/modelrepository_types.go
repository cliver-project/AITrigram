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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ModelStorage defines where and how model files are stored
// This simplified design allows users to specify minimal configuration
// while the controller intelligently provisions or refers to the appropriate storage backend
type ModelStorage struct {
	// Size of the storage volume (e.g., "50Gi", "100Gi")
	// +optional
	// +kubebuilder:default="10Gi"
	Size string `json:"size"`

	// MountPath is the path where models will be stored/mounted inside containers
	// Defaults to /data/models if not specified
	// The data of this Model is normally under this path
	// +optional
	// +kubebuilder:default=/data/models
	MountPath string `json:"mountPath,omitempty"`

	// StorageClass specifies which Kubernetes StorageClass to use for the PVC
	// Options:
	//   - "auto" (default): Auto-detect the best available StorageClass (prefers RWX, then default SC, then RWO)
	//   - "<storageclass-name>": Use a specific Kubernetes StorageClass by name (e.g., "longhorn", "nfs-client", "standard")
	// The controller creates a PVC with the appropriate access mode (RWX or RWO) based on the StorageClass provisioner.
	// +optional
	// +kubebuilder:default=auto
	StorageClass string `json:"storageClass,omitempty"`
}

// ModelOrigin defines the type of model source origin.
// +kubebuilder:validation:Enum=huggingface;ollama
type ModelOrigin string

const (
	ModelOriginHuggingFace ModelOrigin = "huggingface"
	ModelOriginOllama      ModelOrigin = "ollama"
)

// ModelSource defines the source of the model
type ModelSource struct {
	// Origin specifies the source origin (huggingface, ollama)
	// +optional
	// +kubebuilder:default=huggingface
	Origin ModelOrigin `json:"origin"`

	// ModelId is the identifier of the model within the origin
	// +kubebuilder:validation:Required
	ModelId string `json:"modelId"`

	// Revision is the current active revision to download and serve
	// For HuggingFace: git ref (branch, tag, or commit hash), defaults to "main"
	// For Ollama: model tag (e.g., "0.5b", "7b"), defaults to "latest"
	// +optional
	Revision string `json:"revision,omitempty"`

	// Revisions is a list of additional revisions to track and maintain
	// Each revision will be downloaded and stored separately alongside the current revision
	// +optional
	Revisions []RevisionReference `json:"revisions,omitempty"`

	// HFTokenSecret is the name of the secret containing the HuggingFace token
	// The secret should exist in the controller's namespace
	// Only used when origin is "huggingface"
	// +optional
	HFTokenSecret string `json:"hfTokenSecret,omitempty"`

	// HFTokenSecretKey is the key within the secret that contains the HuggingFace token
	// Defaults to "HFToken" if not specified
	// Only used when HFTokenSecret is specified
	// +optional
	// +kubebuilder:default=HFToken
	HFTokenSecretKey string `json:"hfTokenSecretKey,omitempty"`
}

// RevisionReference references a specific model revision
type RevisionReference struct {
	// Name is a user-friendly name for this revision (e.g., "v1.0.0", "stable")
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Ref is the actual reference (tag, branch, or commit hash)
	// For HuggingFace: git ref
	// For other sources: version identifier
	// +kubebuilder:validation:Required
	Ref string `json:"ref"`
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
	Storage ModelStorage `json:"storage"`

	// AutoDownload indicates whether to automatically download the model
	// +optional
	// +kubebuilder:default=true
	AutoDownload bool `json:"autoDownload"`

	// DownloadImage specifies the container image to use for downloading the model
	// If not specified, a default image will be selected based on the source origin
	// +optional
	DownloadImage string `json:"downloadImage,omitempty"`

	// DownloadScripts specifies custom scripts to run for downloading the model
	// If not specified, default scripts will be used based on the source origin
	//
	// It supports Jinja2 template variables: {{ ModelId }}, {{ ModelName }}, {{ MountPath }}
	// Can be either bash or Python script (auto-detected)
	// +optional
	DownloadScripts string `json:"downloadScripts,omitempty"`

	// DeleteScripts specifies custom scripts to run for deleting the model when the ModelRepository is deleted
	// If not specified, default scripts will be used based on the source origin
	//
	// It supports Jinja2 template variables: {{ ModelId }}, {{ ModelName }}, {{ MountPath }}
	// Can be either bash or Python script (auto-detected)
	// +optional
	DeleteScripts string `json:"deleteScripts,omitempty"`

	// NodeSelector specifies node selector requirements for scheduling download and cleanup jobs
	// This is particularly important when using the storages which needs to ensure jobs run on the correct nodes
	// Example: {"kubernetes.io/hostname": "kind-control-plane"}
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// DownloadPhase defines the phase of download operations for both ModelRepository and Revisions
// +kubebuilder:validation:Enum=Pending;Downloading;Ready;Failed;Deprecated
type DownloadPhase string

const (
	DownloadPhasePending     DownloadPhase = "Pending"
	DownloadPhaseDownloading DownloadPhase = "Downloading"
	DownloadPhaseReady       DownloadPhase = "Ready"
	DownloadPhaseFailed      DownloadPhase = "Failed"
	DownloadPhaseDeprecated  DownloadPhase = "Deprecated"
)

// RevisionStatus describes a specific revision's state
type RevisionStatus struct {
	// Name is the user-friendly name
	Name string `json:"name"`

	// CommitHash is the immutable reference (git commit for HF)
	// +optional
	CommitHash string `json:"commitHash"`

	// Status is the current status of this revision
	Status DownloadPhase `json:"status"`
}

// ModelRepositoryStatus defines the observed state of ModelRepository
type ModelRepositoryStatus struct {
	// Phase represents the current phase of the model repository
	// +optional
	Phase DownloadPhase `json:"phase,omitempty"`

	// Message provides additional information about the current phase
	// +optional
	Message string `json:"message,omitempty"`

	// LastUpdated is the timestamp of the last status update
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`

	// Conditions represent the latest available observations of the ModelRepository's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Storage contains detailed information about provisioned storage
	// Only populated after storage is successfully provisioned
	// +optional
	Storage *StorageStatus `json:"storage,omitempty"`

	// AvailableRevisions lists all downloaded and available revisions
	// Only populated when spec.source.revisions is specified
	// +optional
	AvailableRevisions []RevisionStatus `json:"availableRevisions,omitempty"`
}

// StorageStatus contains detailed storage backend information
type StorageStatus struct {
	// StorageClass is the actual storage class used (nfs, hostpath, custom name)
	// +optional
	StorageClass string `json:"storageClass,omitempty"`

	// AccessMode is the volume access mode (ReadWriteMany, ReadWriteOnce)
	// +optional
	AccessMode string `json:"accessMode,omitempty"`

	// VolumeMode is the volume mode (Filesystem, Block)
	// +optional
	// +kubebuilder:default=Filesystem
	VolumeMode string `json:"volumeMode,omitempty"`

	// BackendRef contains backend-specific reference information
	// This allows workloads in different namespaces to mount the same storage
	// +optional
	BackendRef *BackendReference `json:"backendRef,omitempty"`

	// BoundNodeName for node-bound storage (HostPath, RWO PVCs)
	// +optional
	BoundNodeName string `json:"boundNodeName,omitempty"`

	// Capacity is the total storage capacity
	// +optional
	Capacity string `json:"capacity,omitempty"`
}

// BackendReference contains backend-specific storage reference as generic key-value pairs
// This allows new storage types to be added without CRD updates
type BackendReference struct {
	// Type is the actual backend storage type detected from the PV (e.g., "nfs", "cephfs", "longhorn", "csi", "ebs")
	// +optional
	Type string `json:"type,omitempty"`

	// Details contains backend-specific reference information as key-value pairs
	// Common keys:
	//   For PVC: "pvcName", "pvcNamespace"
	//   For NFS: "server", "path"
	//   For HostPath: "path"
	//   For CephFS: "monitors", "path"
	// +optional
	Details map[string]string `json:"details,omitempty"`
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
