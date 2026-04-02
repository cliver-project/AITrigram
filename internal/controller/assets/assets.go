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

package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed llmengine/*/*
var llmEngineAssets embed.FS

//go:embed modelrepository/*/*
var modelRepositoryAssets embed.FS

var (
	// Scheme for decoding Kubernetes resources
	scheme = runtime.NewScheme()
	// Codecs for YAML decoding
	codecs = serializer.NewCodecFactory(scheme)
)

func init() {
	// Register types for LLMEngine and ModelRepository asset loading
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = batchv1.AddToScheme(scheme)
}

// GetModelRepositoryOriginAssets returns assets for a specific origin (e.g., "huggingface", "ollama")
func GetModelRepositoryOriginAssets(origin string) (fs.FS, error) {
	return fs.Sub(modelRepositoryAssets, "modelrepository/"+origin)
}

// LoadLLMEngineDeploymentManifest loads the deployment.yaml template for the given engine type.
// engineType should be "ollama" or "vllm".
func LoadLLMEngineDeploymentManifest(engineType string) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	if err := loadLLMEngineManifestInto(engineType, "deployment.yaml", deployment); err != nil {
		return nil, err
	}
	return deployment, nil
}

// LoadLLMEngineServiceManifest loads the service.yaml template for the given engine type.
// engineType should be "ollama" or "vllm".
func LoadLLMEngineServiceManifest(engineType string) (*corev1.Service, error) {
	service := &corev1.Service{}
	if err := loadLLMEngineManifestInto(engineType, "service.yaml", service); err != nil {
		return nil, err
	}
	return service, nil
}

// loadLLMEngineManifestInto loads a manifest file and decodes it into the provided object.
func loadLLMEngineManifestInto(engineType, fileName string, into client.Object) error {
	filePath := path.Join("llmengine", engineType, fileName)
	data, err := llmEngineAssets.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", filePath, err)
	}

	deserializer := codecs.UniversalDeserializer()
	_, _, err = deserializer.Decode(data, nil, into)
	if err != nil {
		return fmt.Errorf("failed to decode %s: %w", filePath, err)
	}

	return nil
}

// LoadModelRepositoryJobManifest loads a job manifest file for modelrepository.
// origin should be "huggingface" or "ollama".
// jobType should be "download_job" or "cleanup_job".
func LoadModelRepositoryJobManifest(origin, jobType string) (*batchv1.Job, error) {
	job := &batchv1.Job{}
	if err := loadModelRepositoryManifestInto(origin, jobType+".yaml", job); err != nil {
		return nil, err
	}
	return job, nil
}

// loadModelRepositoryManifestInto loads a modelrepository manifest file and decodes it into the provided object.
func loadModelRepositoryManifestInto(origin, fileName string, into client.Object) error {
	filePath := path.Join("modelrepository", origin, fileName)
	data, err := modelRepositoryAssets.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", filePath, err)
	}

	deserializer := codecs.UniversalDeserializer()
	_, _, err = deserializer.Decode(data, nil, into)
	if err != nil {
		return fmt.Errorf("failed to decode %s: %w", filePath, err)
	}

	return nil
}
