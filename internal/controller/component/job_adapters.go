package component

import (
	"fmt"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
	"github.com/cliver-project/AITrigram/internal/controller/assets"
	"github.com/cliver-project/AITrigram/internal/controller/storage"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

// AdaptDownloadJob is the adapter function for download jobs.
// It customizes the download job template based on the ModelRepository configuration.
func AdaptDownloadJob(ctx ModelRepositoryJobContext, job *batchv1.Job) error {
	return adaptModelRepositoryJob(ctx, job, "download")
}

// AdaptCleanupJob is the adapter function for cleanup jobs.
// It customizes the cleanup job template based on the ModelRepository configuration.
func AdaptCleanupJob(ctx ModelRepositoryJobContext, job *batchv1.Job) error {
	return adaptModelRepositoryJob(ctx, job, "cleanup")
}

// adaptModelRepositoryJob is the common implementation for both download and cleanup jobs.
// It customizes the job template based on the ModelRepository configuration.
func adaptModelRepositoryJob(ctx ModelRepositoryJobContext, job *batchv1.Job, jobType string) error {
	origin := string(ctx.ModelRepo.Spec.Source.Origin)

	// Get script file name and model mount path based on origin
	scriptFile, modelPath := getOriginConfig(origin, jobType, ctx.ModelRepo)

	// Set job name and namespace
	job.SetName(ctx.JobName)
	job.SetNamespace(ctx.Namespace)

	// Merge labels with existing template labels (preserving template labels like app: llm-downloader/llm-cleaner)
	if job.Labels == nil {
		job.Labels = make(map[string]string)
	}
	job.Labels["modelrepository.aitrigram.cliver-project.github.io/name"] = ctx.ModelRepo.Name

	if job.Spec.Template.Labels == nil {
		job.Spec.Template.Labels = make(map[string]string)
	}
	job.Spec.Template.Labels["modelrepository.aitrigram.cliver-project.github.io/name"] = ctx.ModelRepo.Name

	// Get container reference
	container := &job.Spec.Template.Spec.Containers[0]

	// Override image if custom image is provided
	if ctx.CustomImage != "" {
		container.Image = ctx.CustomImage
	}

	// Build script args
	var script string
	var err error
	if ctx.CustomScript != "" {
		script = ctx.CustomScript
	} else {
		script, err = assets.LoadModelRepositoryScript(origin, scriptFile)
		if err != nil {
			return fmt.Errorf("failed to load %s script: %w", jobType, err)
		}
	}
	container.Args = []string{"-c", script}

	// Build environment variables
	params := map[string]string{
		"MODEL_ID":   ctx.ModelRepo.Spec.Source.ModelId,
		"MODEL_NAME": ctx.ModelRepo.Spec.ModelName,
		"MOUNT_PATH": ctx.Provider.GetMountPath(),
	}
	if ctx.RevisionRef != nil {
		params["REVISION_ID"] = ctx.RevisionRef.Ref
	}

	container.Env = buildJobEnvironment(origin, params, modelPath, &ctx.ModelRepo.Spec.Source)

	// Prepare volumes
	volume, volumeMount, err := ctx.Provider.PrepareDownloadVolume()
	if err != nil {
		return fmt.Errorf("failed to prepare download volume: %w", err)
	}

	container.VolumeMounts = []corev1.VolumeMount{*volumeMount}
	job.Spec.Template.Spec.Volumes = []corev1.Volume{*volume}

	// Set node selector and affinity
	if len(ctx.ModelRepo.Spec.NodeSelector) > 0 {
		job.Spec.Template.Spec.NodeSelector = ctx.ModelRepo.Spec.NodeSelector
	}

	if nodeAffinity := ctx.Provider.GetNodeAffinity(); nodeAffinity != nil {
		job.Spec.Template.Spec.Affinity = &corev1.Affinity{
			NodeAffinity: nodeAffinity,
		}
	}

	return nil
}

// getOriginConfig returns script file name and model mount path for a given origin and job type
// The mount path honors the ModelRepository spec, falling back to DefaultModelStoragePath
func getOriginConfig(origin, jobType string, modelRepo *aitrigramv1.ModelRepository) (scriptFile, modelPath string) {
	// Get model path from ModelRepository spec, or use default
	modelPath = storage.DefaultModelStoragePath
	if modelRepo != nil && modelRepo.Spec.Storage.MountPath != "" {
		modelPath = modelRepo.Spec.Storage.MountPath
	}

	switch origin {
	case "huggingface":
		if jobType == "download" {
			scriptFile = "download.py"
		} else {
			scriptFile = "cleanup.py"
		}
	case "ollama":
		if jobType == "download" {
			scriptFile = "download.sh"
		} else {
			scriptFile = "cleanup.sh"
		}
	}

	return scriptFile, modelPath
}

// buildJobEnvironment creates the environment variable list for a job
func buildJobEnvironment(
	origin string,
	params map[string]string,
	mountPath string,
	modelSource *aitrigramv1.ModelSource,
) []corev1.EnvVar {
	env := []corev1.EnvVar{}

	// Add origin-specific environment variables
	switch origin {
	case "huggingface":
		// HuggingFace environment variables
		env = append(env, corev1.EnvVar{
			Name:  "HF_HOME",
			Value: mountPath,
		})
		env = append(env, corev1.EnvVar{
			Name:  "TRANSFORMERS_CACHE",
			Value: mountPath,
		})
		env = append(env, corev1.EnvVar{
			Name:  "HUGGINGFACE_HUB_CACHE",
			Value: mountPath + "/hub",
		})
		// Add HF_TOKEN if provided (optional)
		if modelSource != nil && modelSource.HFTokenSecret != "" {
			key := modelSource.HFTokenSecretKey
			if key == "" {
				key = "HFToken"
			}
			env = append(env, corev1.EnvVar{
				Name: "HF_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: modelSource.HFTokenSecret,
						},
						Key: key,
					},
				},
			})
		}

	case "ollama":
		// Ollama environment variables
		env = append(env, corev1.EnvVar{
			Name:  "HOME",
			Value: mountPath,
		})
		env = append(env, corev1.EnvVar{
			Name:  "OLLAMA_HOME",
			Value: mountPath,
		})
		env = append(env, corev1.EnvVar{
			Name:  "OLLAMA_MODELS",
			Value: mountPath,
		})
	}

	// Add parameters as environment variables (MODEL_ID, REVISION, etc.)
	for key, value := range params {
		env = append(env, corev1.EnvVar{
			Name:  key,
			Value: value,
		})
	}

	return env
}
