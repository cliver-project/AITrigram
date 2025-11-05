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

// LLMEngineType defines the type of LLM engine.
// +kubebuilder:validation:Enum=ollama;vllm
type LLMEngineType string

const (
	LLMEngineTypeOllama LLMEngineType = "ollama"
	// vllm actually serves the openai compatible API
	LLMEngineTypeVLLM LLMEngineType = "vllm"
)

// GPUConfig defines GPU resource configuration for LLMEngine
type GPUConfig struct {
	// Enabled indicates whether GPU support is enabled
	// When enabled, the deployment will request GPU resources and target GPU nodes
	// +kubebuilder:default:=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Count specifies the number of GPUs to request per pod
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default:=1
	// +optional
	Count int `json:"count,omitempty"`

	// Type specifies the GPU resource type to request
	// Common values: "nvidia.com/gpu", "amd.com/gpu"
	// +kubebuilder:default:="nvidia.com/gpu"
	// +optional
	Type string `json:"type,omitempty"`

	// NodeSelector specifies additional node selector requirements for GPU nodes
	// These will be merged with the default GPU node selector
	// Common labels: "nvidia.com/gpu.present=true", "cloud.google.com/gke-accelerator=nvidia-tesla-t4"
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations specifies tolerations for GPU nodes
	// GPU nodes are often tainted to prevent non-GPU workloads from scheduling
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// LLMEngineSpec defines the desired state of LLMEngine.
type LLMEngineSpec struct {
	// EngineType specifies the type of LLM engine (e.g., ollama, vllm).
	// +kubebuilder:validation:Required
	// +kubebuilder:default:=vllm
	EngineType LLMEngineType `json:"engineType"`

	// ModelRefs lists the ModelRepository names that this engine will serve
	// The engine will automatically mount storage from these ModelRepositories
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	ModelRefs []string `json:"modelRefs"`

	// Image specifies the container image to use for the engine.
	// If not specified, a default image will be selected based on engineType
	// +optional
	Image string `json:"image,omitempty"`

	// Args specifies arguments to start the engine container.
	// +optional
	Args []string `json:"args,omitempty"`

	// Env specifies environment variables for the engine container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Port specifies the HTTP port for the engine inside the container
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// ServicePort specifies the port for the ClusterIP Service managed by this engine.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	ServicePort int32 `json:"servicePort,omitempty"`

	// Cache specifies the storage configuration for the k-v cache
	// +optional
	Cache *ModelStorage `json:"cache,omitempty"`

	// Replicas specifies the number of pod replicas for each model deployment
	// Each ModelRef will get its own Deployment with this many replicas
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default:=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// GPU specifies GPU resource configuration
	// When GPU is enabled, pods will request GPU resources and be scheduled on GPU nodes
	// If GPU nodes are not available or lack sufficient resources, deployment will fail
	// +optional
	GPU *GPUConfig `json:"gpu,omitempty"`
}

// LLMEngineStatus defines the observed state of LLMEngine.
type LLMEngineStatus struct {
	// Phase represents the current phase of the LLM engine
	// +optional
	Phase string `json:"phase,omitempty"`

	// Reason provides the reason for the current phase
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message provides additional information about the current phase
	// +optional
	Message string `json:"message,omitempty"`

	// LastUpdated is the timestamp of the last status update
	// +optional
	LastUpdated *metav1.Time `json:"lastUpdated,omitempty"`

	// Conditions represent the latest available observations of the LLMEngine's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ModelRepositories lists the ModelRepository resources being used
	// +optional
	ModelRepositories []string `json:"modelRepositories,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Engine",type=string,JSONPath=`.spec.engineType`
// +kubebuilder:printcolumn:name="Models",type=string,JSONPath=`.spec.modelRefs`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type LLMEngine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LLMEngineSpec   `json:"spec,omitempty"`
	Status LLMEngineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type LLMEngineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LLMEngine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LLMEngine{}, &LLMEngineList{})
}
