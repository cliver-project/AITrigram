package adapters

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/component"
	"github.com/cliver-project/AITrigram/internal/controller/storage"
)

// AdaptDeployment customizes the deployment template for a specific LLMEngine + ModelRepository.
// It applies dynamic configuration to the asset template based on the LLMEngine spec.
func AdaptDeployment(ctx component.LLMEngineContext, deployment *appsv1.Deployment) error {
	llmEngine := ctx.LLMEngine
	modelRepo := ctx.ModelRepo

	// Set name and labels
	name := ComponentName(llmEngine.Name, modelRepo.Name)
	deployment.SetName(name)
	setDeploymentLabels(deployment, llmEngine.Name, modelRepo.Name, llmEngine.Spec.EngineType)

	// Set replicas
	if llmEngine.Spec.Replicas != nil {
		deployment.Spec.Replicas = llmEngine.Spec.Replicas
	}

	// Get container reference
	container := &deployment.Spec.Template.Spec.Containers[0]

	// Set image (user-specified takes precedence, otherwise use engine-specific default)
	if llmEngine.Spec.Image != "" {
		container.Image = llmEngine.Spec.Image
	} else {
		// Set default image based on engine type (overrides asset template placeholder)
		switch llmEngine.Spec.EngineType {
		case aitrigramv1.LLMEngineTypeOllama:
			container.Image = DefaultOllamaImage
		case aitrigramv1.LLMEngineTypeVLLM:
			container.Image = DefaultVLLMImage
		default:
			container.Image = DefaultVLLMImage
		}
	}

	// Set pod port
	podPort := GetPodPort(llmEngine)
	container.Ports[0].ContainerPort = podPort

	// Build storage paths
	modelPath, _ := GetStoragePaths(llmEngine, modelRepo)

	// Use the read-only PVC created by EnsureLLMEngineStorage in the LLMEngine's namespace
	volumeSource := corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
			ClaimName: ctx.ModelPVCName,
			ReadOnly:  true,
		},
	}

	// Add model storage volume mount to container
	container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: modelPath,
		ReadOnly:  true,
	})

	// Check GPU
	requestGPU := DetectGPURequest(llmEngine)

	// Build environment variables
	var envs []corev1.EnvVar
	switch llmEngine.Spec.EngineType {
	case aitrigramv1.LLMEngineTypeOllama:
		envs = BuildOllamaEnv(llmEngine, modelRepo, requestGPU)
	case aitrigramv1.LLMEngineTypeVLLM:
		envs = BuildVLLMEnv(llmEngine, modelRepo, requestGPU)
	default:
		envs = BuildVLLMEnv(llmEngine, modelRepo, requestGPU)
	}
	// Merge environment variables with new envs taking precedence
	container.Env = MergeEnvVars(container.Env, envs)

	// Build arguments
	switch llmEngine.Spec.EngineType {
	case aitrigramv1.LLMEngineTypeVLLM:
		// For vLLM, CLI args only set config file path and network binding.
		// Model, revision, and all other params go into the config file.
		container.Args = buildVLLMArgs(podPort)
	case aitrigramv1.LLMEngineTypeOllama:
		// For Ollama, use user-provided args or keep template default ("serve")
		if len(llmEngine.Spec.Args) > 0 {
			container.Args = llmEngine.Spec.Args
		}
	default:
		// Default: keep template args
		if len(llmEngine.Spec.Args) > 0 {
			container.Args = llmEngine.Spec.Args
		}
	}

	// Override resources from spec (takes precedence over template defaults)
	if llmEngine.Spec.Resources != nil {
		container.Resources = MergeResourceRequirements(container.Resources, *llmEngine.Spec.Resources)
	}

	// Set resources and security context based on GPU/CPU
	if requestGPU {
		// Merge GPU resources with template's CPU/memory resources
		gpuResources := BuildGPUResources(llmEngine.Spec.GPU)
		container.Resources = MergeResourceRequirements(container.Resources, gpuResources)

		// Add GPU-specific capabilities to the template's security context
		AddGPUCapabilities(container.SecurityContext)
	}

	// Pod spec configuration
	podSpec := &deployment.Spec.Template.Spec

	// Add model storage volume to pod (template already has vllm-config, shm, etc.)
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name:         "model-storage",
		VolumeSource: volumeSource,
	})

	// Update vLLM config ConfigMap name if using vLLM engine
	if llmEngine.Spec.EngineType == aitrigramv1.LLMEngineTypeVLLM {
		configMapName := GetVLLMConfigMapName(llmEngine.Name, modelRepo.Name)
		for i := range podSpec.Volumes {
			if podSpec.Volumes[i].Name == "vllm-config" && podSpec.Volumes[i].ConfigMap != nil {
				podSpec.Volumes[i].ConfigMap.Name = configMapName
				break
			}
		}
	}

	// Build node selector (storage + GPU)
	nodeSelector := make(map[string]string)

	// Storage node affinity (highest priority)
	if storage.RequiresNodeAffinity(modelRepo) {
		boundNode := storage.GetBoundNodeName(modelRepo)
		if boundNode != "" {
			nodeSelector["kubernetes.io/hostname"] = boundNode
		}
	}

	// GPU node selector (merged, storage takes precedence)
	if requestGPU {
		gpuNodeSelector := BuildGPUNodeSelector(llmEngine.Spec.GPU, llmEngine)
		for k, v := range gpuNodeSelector {
			if _, exists := nodeSelector[k]; !exists {
				nodeSelector[k] = v
			}
		}
	}

	if len(nodeSelector) > 0 {
		podSpec.NodeSelector = nodeSelector
	}

	// Set tolerations (for GPU)
	if requestGPU {
		podSpec.Tolerations = BuildGPUTolerations(llmEngine.Spec.GPU)
	}

	// Set HostIPC
	if llmEngine.Spec.HostIPC {
		podSpec.HostIPC = true
	}

	return nil
}

// setDeploymentLabels sets the labels for deployment, selector, and pod template.
func setDeploymentLabels(deployment *appsv1.Deployment, engineName, modelName string, engineType aitrigramv1.LLMEngineType) {
	labels := map[string]string{
		"app":         engineName,
		"model":       modelName,
		"engine-type": string(engineType),
	}

	// Set deployment labels (merge instead of override)
	if deployment.Labels == nil {
		deployment.Labels = make(map[string]string)
	}
	for k, v := range labels {
		deployment.Labels[k] = v
	}

	// Set selector labels (only app and model should be sufficient)
	deployment.Spec.Selector.MatchLabels = map[string]string{
		"app":   engineName,
		"model": modelName,
	}

	// Set pod template labels (merge instead of override)
	if deployment.Spec.Template.Labels == nil {
		deployment.Spec.Template.Labels = make(map[string]string)
	}
	for k, v := range labels {
		deployment.Spec.Template.Labels[k] = v
	}
}

// buildVLLMArgs constructs the minimal vLLM CLI arguments.
// Only the config file path and network binding are set here.
// All other parameters (model, revision, dtype, etc.) go into the config file
// via BuildVLLMConfig.
func buildVLLMArgs(port int32) []string {
	return []string{
		"--config", VLLMConfigFilePath,
		"--host", "0.0.0.0",
		"--port", fmt.Sprintf("%d", port),
	}
}
