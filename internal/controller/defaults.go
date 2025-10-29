package controller

import (
	aitrigramv1 "github.com/gaol/AITrigram/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func cacheAndModelsMount(storage *aitrigramv1.LLMEngineStorage) ([]corev1.Volume, []corev1.VolumeMount) {
	// storage may be nil
	if storage == nil {
		return nil, nil
	}

	modelStorage := storage.ModelsStorage
	cacheStorage := storage.CacheStorage
	modelVolume := corev1.Volume{
		Name:         "models",
		VolumeSource: modelStorage.VolumeSource,
	}
	modelVolumeMount := corev1.VolumeMount{
		Name:      "models",
		MountPath: modelStorage.Path,
	}
	if cacheStorage != nil {
		cacheVolume := corev1.Volume{
			Name: "cache",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: cacheStorage.EmptyDirVolumeSource,
			},
		}
		cacheVolumeMount := corev1.VolumeMount{
			Name:      "cache",
			MountPath: cacheStorage.Path,
		}
		return []corev1.Volume{modelVolume, cacheVolume}, []corev1.VolumeMount{modelVolumeMount, cacheVolumeMount}
	}
	return []corev1.Volume{modelVolume}, []corev1.VolumeMount{modelVolumeMount}
}

func DefaultLLMEngineSpec(engineType *aitrigramv1.LLMEngineType) *aitrigramv1.LLMEngineSpec {
	if *engineType == aitrigramv1.LLMEngineTypeOllama {
		return defaultsOfOllamaEngine()
	}
	if *engineType == aitrigramv1.LLMEngineTypeVLLM {
		return defaultsOfVLLMEngine()
	}
	// TODO add more for other types
	return &aitrigramv1.LLMEngineSpec{}
}

// Returns default setup for Ollama engine
func defaultsOfOllamaEngine() *aitrigramv1.LLMEngineSpec {
	ollama := aitrigramv1.LLMEngineTypeOllama
	ollamaEngine := &aitrigramv1.LLMEngineSpec{
		EngineType:              ollama,
		Image:                   defaultOllamaImage,
		Port:                    11434,
		ServicePort:             8080,
		ModelDeploymentTemplate: defaultsModelDeploymentSpecOllama(),
	}
	return ollamaEngine
}

const (
	defaultOllamaImage string = "ollama/ollama:latest"
	defaultVLLMImage   string = "vllm/vllm-openai:latest"
)

func defaultsModelDeploymentSpecVLLM() *aitrigramv1.ModelDeploymentTemplate {
	cacheSizeLimit := resource.MustParse("10Gi")
	return &aitrigramv1.ModelDeploymentTemplate{
		Args:            []string{"--host", "0.0.0.0", "--port", "8000"},
		DownloadImage:   defaultVLLMImage,
		DownloadScripts: `mkdir -p {{ .ModelDir }} && cd {{ .ModelDir }} && python -c "from huggingface_hub import snapshot_download; snapshot_download('{{ .ModelName }}', cache_dir='{{ .ModelDir }}', token=os.getenv('HUGGING_FACE_HUB_TOKEN'))"`,
		Storage: &aitrigramv1.LLMEngineStorage{
			ModelsStorage: &aitrigramv1.ModelStorage{
				Path: "/data/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			CacheStorage: &aitrigramv1.CacheStorage{
				Path: "/tmp",
				EmptyDirVolumeSource: &corev1.EmptyDirVolumeSource{
					SizeLimit: &cacheSizeLimit,
				},
			},
		},
		Envs: &[]corev1.EnvVar{
			{
				Name:  "HF_HOME",
				Value: "/data/models",
			},
		},
	}
}

func defaultsModelDeploymentSpecOllama() *aitrigramv1.ModelDeploymentTemplate {
	cacheSizeLimit := resource.MustParse("2Gi")
	return &aitrigramv1.ModelDeploymentTemplate{
		Args:            []string{"/bin/ollama", "serve"},
		DownloadImage:   defaultOllamaImage,
		DownloadScripts: `ollama serve & sleep 10 && ollama pull {{ .ModelName }}`,
		Storage: &aitrigramv1.LLMEngineStorage{
			ModelsStorage: &aitrigramv1.ModelStorage{
				Path: "/models",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			CacheStorage: &aitrigramv1.CacheStorage{
				Path: "/cache_dir",
				EmptyDirVolumeSource: &corev1.EmptyDirVolumeSource{
					SizeLimit: &cacheSizeLimit,
				},
			},
		},
		Envs: &[]corev1.EnvVar{
			{
				Name:  "OLLAMA_MODELS",
				Value: "/models",
			},
			{
				Name:  "OLLAMA_CACHE_DIR",
				Value: "/cache_dir",
			},
		},
	}
}

// Returns default setup for VLLM engine
func defaultsOfVLLMEngine() *aitrigramv1.LLMEngineSpec {
	vllm := aitrigramv1.LLMEngineTypeVLLM
	vllmEngine := &aitrigramv1.LLMEngineSpec{
		EngineType:              vllm,
		Image:                   defaultVLLMImage,
		Port:                    8000,
		ServicePort:             8080,
		ModelDeploymentTemplate: defaultsModelDeploymentSpecVLLM(),
	}
	return vllmEngine
}

var (
	ollamaEngineType                                   = aitrigramv1.LLMEngineTypeOllama
	DefaultOllamaEngineSpec *aitrigramv1.LLMEngineSpec = DefaultLLMEngineSpec(&ollamaEngineType)
	vllmEngineType                                     = aitrigramv1.LLMEngineTypeVLLM
	DefaultVLLMEngineSpec   *aitrigramv1.LLMEngineSpec = DefaultLLMEngineSpec(&vllmEngineType)
)

// Merge the ModelDeploymentTemplate, the later settings overrides the previous ones
// So make sure the ones you want to keep in the last arguments.
func MergeModelDeploymentTemplate(modelSpecs ...*aitrigramv1.ModelDeploymentTemplate) (*aitrigramv1.ModelDeploymentTemplate, error) {
	if len(modelSpecs) == 0 {
		return nil, nil
	}
	if len(modelSpecs) == 1 {
		return modelSpecs[0], nil
	}
	result := modelSpecs[0]
	for _, ms := range modelSpecs[1:] {
		if ms == nil {
			continue
		}
		if ms.Args != nil {
			result.Args = ms.Args
		}
		if ms.DownloadImage != "" {
			result.DownloadImage = ms.DownloadImage
		}
		if ms.DownloadScripts != "" {
			result.DownloadScripts = ms.DownloadScripts
		}
		if ms.Envs != nil {
			envs, err := MergeSliceByName(result.Envs, ms.Envs)
			if err != nil {
				return nil, err
			}
			result.Envs = envs
		}
		if ms.Storage != nil {
			result.Storage = mergeStorages(result.Storage, ms.Storage)
		}
	}
	return result, nil
}

// Merge the LLMEngineSpec, the later overrides the previous ones
func MergeLLMSpecs(llmEngineSpecs ...*aitrigramv1.LLMEngineSpec) (*aitrigramv1.LLMEngineSpec, error) {
	if len(llmEngineSpecs) == 0 {
		return nil, nil
	}
	if len(llmEngineSpecs) == 1 {
		return llmEngineSpecs[0], nil
	}
	result := llmEngineSpecs[0]
	for _, llmSpec := range llmEngineSpecs[1:] {
		if llmSpec == nil {
			continue
		}
		if llmSpec.EngineType != "" {
			result.EngineType = llmSpec.EngineType
		}
		if llmSpec.Port != 0 {
			result.Port = llmSpec.Port
		}
		if llmSpec.Image != "" {
			result.Image = llmSpec.Image
		}
		if llmSpec.ServicePort != 0 {
			result.ServicePort = llmSpec.ServicePort
		}
		if llmSpec.ModelDeploymentTemplate != nil {
			modelDeploymentTemplate, err := MergeModelDeploymentTemplate(result.ModelDeploymentTemplate, llmSpec.ModelDeploymentTemplate)
			if err != nil {
				return nil, err
			}
			result.ModelDeploymentTemplate = modelDeploymentTemplate
		}
	}
	return result, nil
}

func mergeStorages(storages ...*aitrigramv1.LLMEngineStorage) *aitrigramv1.LLMEngineStorage {
	if len(storages) == 0 {
		return nil
	}
	if len(storages) == 1 {
		return storages[0]
	}
	result := storages[0]
	for _, storage := range storages[1:] {
		if storage == nil {
			continue
		}
		if storage.CacheStorage != nil {
			result.CacheStorage = storage.CacheStorage
		}
		if storage.ModelsStorage != nil {
			result.ModelsStorage = storage.ModelsStorage
		}

	}
	return result
}
