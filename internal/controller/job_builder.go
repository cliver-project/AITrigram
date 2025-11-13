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

package controller

import (
	"context"
	"fmt"

	"github.com/cliver-project/AITrigram/internal/controller/assets"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// JobBuilder builds Kubernetes Jobs for model download and cleanup using asset configurations
type JobBuilder struct {
	client       client.Client
	config       *assets.AssetConfig
	mountPath    string
	volume       *corev1.Volume
	volumeMount  *corev1.VolumeMount
	nodeSelector map[string]string
	nodeAffinity *corev1.NodeAffinity
	renderer     *TemplateRenderer
}

// NewJobBuilder creates a new JobBuilder for the specified origin
func NewJobBuilder(
	ctx context.Context,
	c client.Client,
	origin string,
	mountPath string,
	volume *corev1.Volume,
	volumeMount *corev1.VolumeMount,
	nodeSelector map[string]string,
	nodeAffinity *corev1.NodeAffinity,
) (*JobBuilder, error) {
	// Load asset configuration for the origin
	config, err := assets.LoadAssetConfig(origin)
	if err != nil {
		return nil, fmt.Errorf("failed to load asset config for origin %s: %w", origin, err)
	}

	// Validate the configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid asset config: %w", err)
	}

	// Use default mount path from config if not specified
	if mountPath == "" {
		mountPath = config.VolumeMount.DefaultPath
	}

	return &JobBuilder{
		client:       c,
		config:       config,
		mountPath:    mountPath,
		volume:       volume,
		volumeMount:  volumeMount,
		nodeSelector: nodeSelector,
		nodeAffinity: nodeAffinity,
		renderer:     NewTemplateRenderer(),
	}, nil
}

// BuildDownloadJob creates a Kubernetes Job for downloading a model
func (jb *JobBuilder) BuildDownloadJob(
	name string,
	namespace string,
	modelRepoName string,
	customImage string,
	customScript string,
	params map[string]string,
	secretRef *corev1.SecretKeySelector,
) (*batchv1.Job, error) {
	container, err := jb.buildContainer(
		jb.config.Download,
		customImage,
		customScript,
		params,
		secretRef,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build download container: %w", err)
	}

	return jb.buildJob(name, namespace, modelRepoName, "download", container), nil
}

// BuildCleanupJob creates a Kubernetes Job for cleaning up a model
func (jb *JobBuilder) BuildCleanupJob(
	name string,
	namespace string,
	modelRepoName string,
	customImage string,
	customScript string,
	params map[string]string,
	secretRef *corev1.SecretKeySelector,
) (*batchv1.Job, error) {
	container, err := jb.buildContainer(
		jb.config.Cleanup,
		customImage,
		customScript,
		params,
		secretRef,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build cleanup container: %w", err)
	}

	return jb.buildJob(name, namespace, modelRepoName, "cleanup", container), nil
}

// buildContainer creates a container spec with the script and configuration
func (jb *JobBuilder) buildContainer(
	jobConfig assets.JobConfig,
	customImage string,
	customScript string,
	params map[string]string,
	secretRef *corev1.SecretKeySelector,
) (corev1.Container, error) {
	// Determine the image (custom or default from asset)
	image := jb.config.Image
	if customImage != "" {
		image = customImage
	}

	// Build the command and args
	command, args, err := jb.buildCommand(jobConfig, customScript, params)
	if err != nil {
		return corev1.Container{}, err
	}

	// Build environment variables
	env := jb.buildEnvironment(jobConfig, params, secretRef)

	// Build resource requirements
	resources, err := jobConfig.Resources.ToK8sResourceRequirements()
	if err != nil {
		return corev1.Container{}, fmt.Errorf("failed to convert resources: %w", err)
	}

	container := corev1.Container{
		Name:            "worker",
		Image:           image,
		Command:         command,
		Args:            args,
		Env:             env,
		VolumeMounts:    []corev1.VolumeMount{*jb.volumeMount},
		ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &[]bool{false}[0],
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
	}

	if resources != nil {
		container.Resources = *resources
	}

	return container, nil
}

// buildCommand creates the command and args for the container
func (jb *JobBuilder) buildCommand(
	jobConfig assets.JobConfig,
	customScript string,
	params map[string]string,
) ([]string, []string, error) {
	var script string
	var err error

	// Determine script source (custom or default from asset)
	if customScript != "" {
		// Use custom script provided via CRD
		script = customScript
	} else {
		// Use default embedded script from asset
		script, err = assets.LoadScript(jb.config.Metadata.Origin, jobConfig.ScriptFile)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load script: %w", err)
		}
	}

	// Convert params map[string]string to map[string]interface{} for template rendering
	templateParams := make(map[string]interface{})
	for k, v := range params {
		templateParams[k] = v
	}

	// Render template variables in script using Jinja2-style rendering
	script, err = jb.renderer.RenderModelScript(script, templateParams)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to render script template: %w", err)
	}

	// Build command based on interpreter
	interpreter := jobConfig.Interpreter
	command := []string{interpreter, "-c", script}
	return command, nil, nil
}

// buildEnvironment creates the environment variable list
func (jb *JobBuilder) buildEnvironment(
	jobConfig assets.JobConfig,
	params map[string]string,
	secretRef *corev1.SecretKeySelector,
) []corev1.EnvVar {
	env := []corev1.EnvVar{}

	// Add environment variables from asset config
	for _, e := range jobConfig.Environment {
		value := e.Value
		if e.ValueFrom == "mountPath" {
			value = jb.mountPath
		}
		env = append(env, corev1.EnvVar{
			Name:  e.Name,
			Value: value,
		})
	}

	// Add parameters as environment variables
	for key, value := range params {
		env = append(env, corev1.EnvVar{
			Name:  key,
			Value: value,
		})
	}

	// Add secret environment variables from asset config
	// These are optional secrets that the asset expects (like HF_TOKEN)
	for _, assetSecretRef := range jobConfig.SecretRefs {
		// If user provided a secret for this environment variable, use it
		if secretRef != nil && assetSecretRef.Name == "HF_TOKEN" {
			env = append(env, corev1.EnvVar{
				Name: assetSecretRef.Name,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: secretRef,
				},
			})
		} else {
			// Otherwise, try to use a default secret if it exists (marked as optional)
			env = append(env, corev1.EnvVar{
				Name: assetSecretRef.Name,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: assetSecretRef.Name,
						},
						Key:      assetSecretRef.SecretKey,
						Optional: &assetSecretRef.Optional,
					},
				},
			})
		}
	}

	return env
}

// buildJob creates the Job manifest with labels
func (jb *JobBuilder) buildJob(name, namespace, modelRepoName, jobType string, container corev1.Container) *batchv1.Job {
	// Create labels for the job
	labels := map[string]string{
		"app": jobType,
		"modelrepository.aitrigram.cliver-project.github.io/name": modelRepoName,
		"app.kubernetes.io/managed-by":                            "modelrepository-controller",
	}

	podSpec := corev1.PodSpec{
		SecurityContext: &corev1.PodSecurityContext{
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		Containers:    []corev1.Container{container},
		Volumes:       []corev1.Volume{*jb.volume},
		RestartPolicy: corev1.RestartPolicyOnFailure,
	}

	// Apply node selector
	if len(jb.nodeSelector) > 0 {
		podSpec.NodeSelector = jb.nodeSelector
	}

	// Apply node affinity
	if jb.nodeAffinity != nil {
		podSpec.Affinity = &corev1.Affinity{
			NodeAffinity: jb.nodeAffinity,
		}
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
		},
	}

	return job
}
