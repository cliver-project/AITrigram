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

package storage

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

// LLMEngineStorageResult contains the PVC name and mount path for LLMEngine to use
type LLMEngineStorageResult struct {
	PVCName   string
	MountPath string
}

// EnsureLLMEngineStorage creates a read-only PV+PVC in the LLMEngine's namespace
// that mirrors the ModelRepository's backend storage. The PV+PVC are always read-only.
// On deletion, only the PV+PVC are removed — the underlying data is never touched.
func EnsureLLMEngineStorage(
	ctx context.Context,
	c client.Client,
	llmEngine *aitrigramv1.LLMEngine,
	modelRepo *aitrigramv1.ModelRepository,
) (*LLMEngineStorageResult, error) {
	if modelRepo.Status.Storage == nil || modelRepo.Status.Storage.BackendRef == nil {
		return nil, fmt.Errorf("ModelRepository %s storage status not available", modelRepo.Name)
	}

	storageStatus := modelRepo.Status.Storage
	backendRef := storageStatus.BackendRef

	pvName := llmEnginePVName(llmEngine.Name, modelRepo.Name)
	pvcName := llmEnginePVCName(llmEngine.Name, modelRepo.Name)
	namespace := llmEngine.Namespace
	mountPath := getOrDefaultMountPath(modelRepo.Spec.Storage.MountPath)

	// Build PV spec from backend reference
	pvSpec, err := buildReadOnlyPVSpec(backendRef, storageStatus)
	if err != nil {
		return nil, fmt.Errorf("unsupported backend type %q: %w", backendRef.Type, err)
	}

	// Set PV metadata and bind to the PVC we'll create
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: pvName,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":                 "aitrigram-operator",
				"llmengine.aitrigram.cliver-project.github.io": llmEngine.Name,
				"aitrigram.io/model-repo":                      modelRepo.Name,
			},
		},
		Spec: *pvSpec,
	}

	// Pre-bind PV to the PVC
	pv.Spec.ClaimRef = &corev1.ObjectReference{
		Namespace: namespace,
		Name:      pvcName,
	}

	// Retain policy — never delete the underlying data
	pv.Spec.PersistentVolumeReclaimPolicy = corev1.PersistentVolumeReclaimRetain

	// Protect with finalizer — only LLMEngine controller can delete
	controllerutil.AddFinalizer(pv, LLMEngineStorageFinalizer)

	// Create PV if it doesn't exist
	existingPV := &corev1.PersistentVolume{}
	if err := c.Get(ctx, types.NamespacedName{Name: pvName}, existingPV); err != nil {
		if !errors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to check PV %s: %w", pvName, err)
		}
		if err := c.Create(ctx, pv); err != nil {
			return nil, fmt.Errorf("failed to create PV %s: %w", pvName, err)
		}
	}

	// Create PVC with finalizer — only LLMEngine controller can delete
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":                 "aitrigram-operator",
				"llmengine.aitrigram.cliver-project.github.io": llmEngine.Name,
				"aitrigram.io/model-repo":                      modelRepo.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadOnlyMany,
			},
			VolumeName: pvName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(storageStatus.Capacity),
				},
			},
		},
	}

	controllerutil.AddFinalizer(pvc, LLMEngineStorageFinalizer)

	existingPVC := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: namespace}, existingPVC); err != nil {
		if !errors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to check PVC %s: %w", pvcName, err)
		}
		if err := c.Create(ctx, pvc); err != nil {
			return nil, fmt.Errorf("failed to create PVC %s: %w", pvcName, err)
		}
		// Just created — not bound yet, requeue
		return nil, fmt.Errorf("PVC %s/%s created, waiting for binding", namespace, pvcName)
	}

	// PVC exists — verify it's bound before proceeding
	if existingPVC.Status.Phase != corev1.ClaimBound {
		return nil, fmt.Errorf("PVC %s/%s not bound yet (phase: %s)", namespace, pvcName, existingPVC.Status.Phase)
	}

	return &LLMEngineStorageResult{
		PVCName:   pvcName,
		MountPath: mountPath,
	}, nil
}

// CleanupLLMEngineStorage deletes the PV+PVC created for an LLMEngine.
// Only removes the PV+PVC — never touches the underlying storage data.
func CleanupLLMEngineStorage(
	ctx context.Context,
	c client.Client,
	llmEngine *aitrigramv1.LLMEngine,
	modelRepoName string,
) error {
	namespace := llmEngine.Namespace
	pvName := llmEnginePVName(llmEngine.Name, modelRepoName)
	pvcName := llmEnginePVCName(llmEngine.Name, modelRepoName)

	// Remove finalizer and delete PVC
	existingPVC := &corev1.PersistentVolumeClaim{}
	if err := c.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: namespace}, existingPVC); err == nil {
		controllerutil.RemoveFinalizer(existingPVC, LLMEngineStorageFinalizer)
		if err := c.Update(ctx, existingPVC); err != nil {
			return fmt.Errorf("failed to remove finalizer from PVC %s: %w", pvcName, err)
		}
		if err := c.Delete(ctx, existingPVC); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete PVC %s/%s: %w", namespace, pvcName, err)
		}
	}

	// Remove finalizer and delete PV
	existingPV := &corev1.PersistentVolume{}
	if err := c.Get(ctx, types.NamespacedName{Name: pvName}, existingPV); err == nil {
		controllerutil.RemoveFinalizer(existingPV, LLMEngineStorageFinalizer)
		if err := c.Update(ctx, existingPV); err != nil {
			return fmt.Errorf("failed to remove finalizer from PV %s: %w", pvName, err)
		}
		if err := c.Delete(ctx, existingPV); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete PV %s: %w", pvName, err)
		}
	}

	return nil
}

// buildReadOnlyPVSpec creates a PV spec that mirrors the backend storage as read-only
func buildReadOnlyPVSpec(backendRef *aitrigramv1.BackendReference, storageStatus *aitrigramv1.StorageStatus) (*corev1.PersistentVolumeSpec, error) {
	capacity := storageStatus.Capacity
	if capacity == "" {
		capacity = "10Gi"
	}

	spec := &corev1.PersistentVolumeSpec{
		Capacity: corev1.ResourceList{
			corev1.ResourceStorage: resource.MustParse(capacity),
		},
		AccessModes: []corev1.PersistentVolumeAccessMode{
			corev1.ReadOnlyMany,
		},
		VolumeMode: volumeModePtr(corev1.PersistentVolumeFilesystem),
	}

	details := backendRef.Details

	switch backendRef.Type {
	case BackendTypeNFS:
		server := details["server"]
		exportPath := details["exportPath"]
		if server == "" || exportPath == "" {
			return nil, fmt.Errorf("NFS backend missing server or exportPath")
		}
		spec.PersistentVolumeSource = corev1.PersistentVolumeSource{
			NFS: &corev1.NFSVolumeSource{
				Server:   server,
				Path:     exportPath,
				ReadOnly: true,
			},
		}

	case BackendTypeHostPath:
		path := details["path"]
		if path == "" {
			return nil, fmt.Errorf("hostpath backend missing path")
		}
		pathType := corev1.HostPathDirectory
		spec.PersistentVolumeSource = corev1.PersistentVolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: path,
				Type: &pathType,
			},
		}
		// HostPath needs node affinity if boundNodeName is known
		if storageStatus.BoundNodeName != "" {
			spec.NodeAffinity = &corev1.VolumeNodeAffinity{
				Required: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "kubernetes.io/hostname",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{storageStatus.BoundNodeName},
								},
							},
						},
					},
				},
			}
		}

	case BackendTypeCephFS:
		monitors := details["monitors"]
		path := details["path"]
		if monitors == "" {
			return nil, fmt.Errorf("cephfs backend missing monitors")
		}
		spec.PersistentVolumeSource = corev1.PersistentVolumeSource{
			CephFS: &corev1.CephFSPersistentVolumeSource{
				Monitors: []string{monitors},
				Path:     path,
				ReadOnly: true,
			},
		}

	case BackendTypeLonghorn, BackendTypeCSI:
		driver := details["driver"]
		volumeID := details["volumeID"]
		if driver == "" || volumeID == "" {
			return nil, fmt.Errorf("CSI backend missing driver or volumeID")
		}
		// Rebuild volume attributes from csi.* keys
		volumeAttributes := map[string]string{}
		for k, v := range details {
			if strings.HasPrefix(k, "csi.") {
				volumeAttributes[strings.TrimPrefix(k, "csi.")] = v
			}
		}
		spec.PersistentVolumeSource = corev1.PersistentVolumeSource{
			CSI: &corev1.CSIPersistentVolumeSource{
				Driver:           driver,
				VolumeHandle:     volumeID,
				ReadOnly:         true,
				VolumeAttributes: volumeAttributes,
			},
		}

	default:
		return nil, fmt.Errorf("unsupported backend type: %s", backendRef.Type)
	}

	return spec, nil
}

// llmEnginePVName generates a PV name within the 63-char K8s limit.
// Format: "le-{engine}-{model}" truncated + hash suffix for uniqueness.
func llmEnginePVName(engineName, modelRepoName string) string {
	return TruncatedName(63, "le", engineName, modelRepoName)
}

// llmEnginePVCName generates a PVC name within the 63-char K8s limit.
func llmEnginePVCName(engineName, modelRepoName string) string {
	return TruncatedName(63, "le", engineName, modelRepoName)
}

// TruncatedName generates a name within maxLen chars: "{prefix}-{a}-{b}" with
// a hash suffix when truncation is needed. The result is deterministic for the
// same inputs. Use maxLen=63 for resources that don't spawn children (PV, PVC,
// Service), or maxLen=52 for Deployments (leaves room for RS+pod hash suffixes).
func TruncatedName(maxLen int, prefix, a, b string) string {
	var full string
	if prefix == "" {
		full = fmt.Sprintf("%s-%s", a, b)
	} else {
		full = fmt.Sprintf("%s-%s-%s", prefix, a, b)
	}
	if len(full) <= maxLen {
		return full
	}

	// Truncate and add hash for uniqueness
	h := fnv.New32a()
	h.Write([]byte(full))
	hash := fmt.Sprintf("%x", h.Sum32())[:6]

	// Reserve space for separators and hash
	separators := 2 // "-" between a/b and "-" before hash
	if prefix != "" {
		separators = 3 // extra "-" after prefix
	}
	available := maxLen - len(prefix) - separators - len(hash)
	halfLen := available / 2

	truncA := a
	if len(truncA) > halfLen {
		truncA = truncA[:halfLen]
	}
	truncB := b
	remaining := available - len(truncA)
	if len(truncB) > remaining {
		truncB = truncB[:remaining]
	}

	if prefix == "" {
		return fmt.Sprintf("%s-%s-%s", truncA, truncB, hash)
	}
	return fmt.Sprintf("%s-%s-%s-%s", prefix, truncA, truncB, hash)
}

func volumeModePtr(mode corev1.PersistentVolumeMode) *corev1.PersistentVolumeMode {
	return &mode
}
