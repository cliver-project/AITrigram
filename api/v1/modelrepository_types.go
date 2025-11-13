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

package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StorageType defines the type of storage backend
// +kubebuilder:validation:Enum=PersistentVolumeClaim;NFS;HostPath
type StorageType string

const (
	StorageTypePVC      StorageType = "PersistentVolumeClaim"
	StorageTypeNFS      StorageType = "NFS"
	StorageTypeHostPath StorageType = "HostPath"
)

// ModelStorage defines where and how model files are stored
// This is the unified storage type used across ModelRepository and LLMEngine
type ModelStorage struct {
	// Type indicates which storage backend to use
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=PersistentVolumeClaim;NFS;HostPath
	Type StorageType `json:"type"`

	// MountPath is the path where models will be stored/mounted inside containers
	// Defaults to /data/models if not specified
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// PersistentVolumeClaim storage configuration
	// Required when Type is PersistentVolumeClaim
	// if existingClaim is specified, a new PVC will not be created, and the operator will try to search it within the same namespace as the ModelRepository CR, typically it is 'aitrigram-system'.
	// otherwise, a new PVC will be created in the same namespace as the ModelRepository CR.
	// +optional
	PersistentVolumeClaim *PVCStorageSpec `json:"persistentVolumeClaim,omitempty"`

	// NFS storage configuration
	// Required when Type is NFS
	// +optional
	NFS *NFSStorageSpec `json:"nfs,omitempty"`

	// HostPath storage configuration
	// Required when Type is HostPath
	// +optional
	HostPath *HostPathStorageSpec `json:"hostPath,omitempty"`
}

// PVCStorageSpec configures PersistentVolumeClaim storage
type PVCStorageSpec struct {
	// StorageClassName for the PVC
	// If not specified, uses the cluster default storage class
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// Size of the volume (e.g., "50Gi", "100Gi")
	// +kubebuilder:validation:Required
	Size string `json:"size"`

	// AccessMode for the PVC
	// ReadWriteMany (RWX) allows multiple nodes to mount the volume
	// ReadWriteOnce (RWO) binds to a single node
	// +optional
	// +kubebuilder:default=ReadWriteMany
	// +kubebuilder:validation:Enum=ReadWriteMany;ReadWriteOnce
	AccessMode corev1.PersistentVolumeAccessMode `json:"accessMode,omitempty"`

	// ExistingClaim references an existing PVC instead of creating a new one
	// When specified, Size, StorageClassName, and AccessMode are ignored
	// +optional
	ExistingClaim *string `json:"existingClaim,omitempty"`

	// VolumeMode defines whether the volume is a filesystem or block device
	// Defaults to Filesystem if not specified
	// +optional
	VolumeMode *corev1.PersistentVolumeMode `json:"volumeMode,omitempty"`

	// Selector is used to find matching PersistentVolumes
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// RetainPolicy determines what happens to the PVC when ModelRepository is deleted
	// "Delete" removes the PVC (default for newly created PVCs)
	// "Retain" keeps the PVC (default for existingClaim)
	// +optional
	// +kubebuilder:validation:Enum=Delete;Retain
	RetainPolicy *RetainPolicy `json:"retainPolicy,omitempty"`
}

// NFSStorageSpec configures NFS storage
type NFSStorageSpec struct {
	// Server is the hostname or IP address of the NFS server
	// +kubebuilder:validation:Required
	Server string `json:"server"`

	// Path exported by the NFS server
	// +kubebuilder:validation:Required
	Path string `json:"path"`

	// ReadOnly mounts the NFS volume as read-only
	// Note: Download jobs require write access, so this should typically be false
	// +optional
	ReadOnly bool `json:"readOnly,omitempty"`
}

// HostPathStorageSpec configures HostPath storage
type HostPathStorageSpec struct {
	// Path on the host node's filesystem
	// +kubebuilder:validation:Required
	Path string `json:"path"`

	// Type of HostPath volume
	// DirectoryOrCreate creates the directory if it doesn't exist
	// Directory requires the directory to exist
	// +optional
	// +kubebuilder:validation:Enum=DirectoryOrCreate;Directory;File;Socket;CharDevice;BlockDevice
	Type *corev1.HostPathType `json:"type,omitempty"`

	// NodeName specifies which node to use for HostPath storage
	// If not specified, the download job can run on any node, and the
	// BoundNodeName will be set in status after the job completes
	// +optional
	NodeName *string `json:"nodeName,omitempty"`
}

// RetainPolicy determines storage cleanup behavior
// This is the policy if the PVC should be retained or deleted upon ModelRepository deletion, not the real backend PVC reclaim policy
type RetainPolicy string

const (
	RetainPolicyDelete RetainPolicy = "Delete"
	RetainPolicyRetain RetainPolicy = "Retain"
)

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

	// NodeSelector specifies node selector requirements for scheduling download and cleanup jobs
	// This is particularly important when using HostPath storage to ensure jobs run on the correct node
	// Example: {"kubernetes.io/hostname": "kind-control-plane"}
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
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

	// BoundNodeName is the name of the node where the download job ran
	// This is used for storage types that require node affinity (HostPath, ReadWriteOnce PVC)
	// LLMEngine pods will be scheduled on this node to access the model files
	// +optional
	BoundNodeName string `json:"boundNodeName,omitempty"`
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
