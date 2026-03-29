package storage

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

// StorageClassProvider implements storage using a Kubernetes StorageClass.
// It creates a PVC backed by the specified StorageClass, which serves as the
// universal intermediary for LLMEngine to mount models read-only.
// Works with any StorageClass (NFS, Longhorn, Ceph, EBS, local-path, etc.).
type StorageClassProvider struct {
	client         client.Client
	scheme         *runtime.Scheme
	modelRepo      *aitrigramv1.ModelRepository
	storageSize    string
	storageClass   string
	mountPath      string
	pvcName        string
	detectedSCInfo *StorageClassInfo
}

// NewStorageClassProvider creates a new StorageClass-based storage provider
func NewStorageClassProvider(
	c client.Client,
	scheme *runtime.Scheme,
	modelRepo *aitrigramv1.ModelRepository,
	scInfo *StorageClassInfo,
) (*StorageClassProvider, error) {
	storage := &modelRepo.Spec.Storage
	mountPath := getOrDefaultMountPath(storage.MountPath)

	// PVC name is deterministic based on ModelRepository name
	pvcName := fmt.Sprintf("%s-storage", modelRepo.Name)

	return &StorageClassProvider{
		client:         c,
		scheme:         scheme,
		modelRepo:      modelRepo,
		storageSize:    storage.Size,
		storageClass:   storage.StorageClass,
		mountPath:      mountPath,
		pvcName:        pvcName,
		detectedSCInfo: scInfo,
	}, nil
}

// ValidateConfig validates the PVC configuration
func (p *StorageClassProvider) ValidateConfig() error {
	if p.storageSize == "" {
		return fmt.Errorf("storage size is required")
	}

	if _, err := resource.ParseQuantity(p.storageSize); err != nil {
		return fmt.Errorf("invalid size %q: %w", p.storageSize, err)
	}

	return nil
}

// PrepareDownloadVolume prepares volume for download job
func (p *StorageClassProvider) PrepareDownloadVolume() (*corev1.Volume, *corev1.VolumeMount, error) {
	volume := &corev1.Volume{
		Name: "model-storage",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: p.pvcName,
			},
		},
	}

	mount := &corev1.VolumeMount{
		Name:      "model-storage",
		MountPath: p.mountPath,
	}

	return volume, mount, nil
}

// GetMountPath returns the mount path
func (p *StorageClassProvider) GetMountPath() string {
	return p.mountPath
}

// CreateStorage provisions the PVC
func (p *StorageClassProvider) CreateStorage(ctx context.Context, namespace string) error {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.pvcName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "model-storage",
				"app.kubernetes.io/managed-by": "aitrigram-operator",
				"aitrigram.io/model-repo":      p.modelRepo.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				p.detectedSCInfo.AccessMode,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(p.storageSize),
				},
			},
		},
	}

	// Use detected storage class name if available
	if p.detectedSCInfo != nil && p.detectedSCInfo.Name != "" {
		pvc.Spec.StorageClassName = &p.detectedSCInfo.Name
	}

	// Add finalizer to protect deletion
	controllerutil.AddFinalizer(pvc, StorageProtectionFinalizer)

	// Create or update PVC
	existing := &corev1.PersistentVolumeClaim{}
	err := p.client.Get(ctx, types.NamespacedName{Name: p.pvcName, Namespace: namespace}, existing)
	if err == nil {
		// PVC already exists
		return nil
	}

	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check PVC: %w", err)
	}

	if err := p.client.Create(ctx, pvc); err != nil {
		return fmt.Errorf("failed to create PVC: %w", err)
	}

	return nil
}

// Cleanup removes the PVC
func (p *StorageClassProvider) Cleanup(ctx context.Context, namespace string) error {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.pvcName,
			Namespace: namespace,
		},
	}

	// Remove finalizer first
	existing := &corev1.PersistentVolumeClaim{}
	if err := p.client.Get(ctx, types.NamespacedName{Name: p.pvcName, Namespace: namespace}, existing); err == nil {
		controllerutil.RemoveFinalizer(existing, StorageProtectionFinalizer)
		if err := p.client.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to remove finalizer: %w", err)
		}
	}

	if err := p.client.Delete(ctx, pvc); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete PVC: %w", err)
	}

	return nil
}

// GetStatus returns current storage status
func (p *StorageClassProvider) GetStatus(ctx context.Context, namespace string) (*StorageStatus, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	err := p.client.Get(ctx, types.NamespacedName{Name: p.pvcName, Namespace: namespace}, pvc)
	if err != nil {
		if errors.IsNotFound(err) {
			return &StorageStatus{
				Ready:   false,
				Message: "PVC not created yet",
			}, nil
		}
		return &StorageStatus{
			Ready:   false,
			Message: fmt.Sprintf("Failed to get PVC: %v", err),
		}, nil
	}

	status := &StorageStatus{
		Ready: pvc.Status.Phase == corev1.ClaimBound,
	}

	if pvc.Status.Capacity != nil {
		if capacity, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
			status.Capacity = capacity.String()
		}
	}

	switch pvc.Status.Phase {
	case corev1.ClaimBound:
		status.Message = "PVC is bound and ready"
	case corev1.ClaimPending:
		status.Message = "PVC is pending, waiting for provisioning"
	default:
		status.Message = fmt.Sprintf("PVC is in %s state", pvc.Status.Phase)
	}

	return status, nil
}

// GetNodeAffinity returns node affinity (only for RWO)
func (p *StorageClassProvider) GetNodeAffinity() *corev1.NodeAffinity {
	// Only RWO needs node affinity
	if p.detectedSCInfo.AccessMode == corev1.ReadWriteOnce {
		if p.modelRepo.Status.Storage != nil && p.modelRepo.Status.Storage.BoundNodeName != "" {
			return &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "kubernetes.io/hostname",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{p.modelRepo.Status.Storage.BoundNodeName},
								},
							},
						},
					},
				},
			}
		}
	}

	return nil
}

// IsShared returns true if storage is accessible from multiple nodes
func (p *StorageClassProvider) IsShared() bool {
	return p.detectedSCInfo.AccessMode == corev1.ReadWriteMany
}

// GetStorageStatus returns detailed storage status for ModelRepository status
// This queries the PVC and its bound PV to extract the ACTUAL underlying storage backend
func (p *StorageClassProvider) GetStorageStatus(ctx context.Context, namespace string) (*aitrigramv1.StorageStatus, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	err := p.client.Get(ctx, types.NamespacedName{Name: p.pvcName, Namespace: namespace}, pvc)
	if err != nil {
		return nil, err
	}

	accessMode := string(p.detectedSCInfo.AccessMode)
	storageStatus := &aitrigramv1.StorageStatus{
		StorageClass: p.storageClass,
		AccessMode:   accessMode,
		VolumeMode:   "Filesystem",
	}

	if pvc.Status.Capacity != nil {
		if capacity, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
			storageStatus.Capacity = capacity.String()
		}
	}

	// For RWO, track bound node
	if p.detectedSCInfo.AccessMode == corev1.ReadWriteOnce && p.modelRepo.Status.Storage != nil {
		storageStatus.BoundNodeName = p.modelRepo.Status.Storage.BoundNodeName
	}

	// Extract the ACTUAL backend from the bound PV
	if pvc.Status.Phase != corev1.ClaimBound || pvc.Spec.VolumeName == "" {
		// PVC not bound yet, return error to trigger retry
		return nil, fmt.Errorf("PVC %s/%s is not bound yet (phase: %s)", namespace, p.pvcName, pvc.Status.Phase)
	}

	backendRef, err := p.extractBackendFromPV(ctx, pvc.Spec.VolumeName, namespace)
	if err != nil {
		// Log and return error instead of masking with "unknown" type
		return nil, fmt.Errorf("failed to extract backend from PV %s: %w", pvc.Spec.VolumeName, err)
	}

	storageStatus.BackendRef = backendRef
	return storageStatus, nil
}

// extractBackendFromPV queries the PersistentVolume and extracts the actual storage backend details
// IMPORTANT: Always includes pvcName in details so LLMEngine can mount the volume via PVC
func (p *StorageClassProvider) extractBackendFromPV(ctx context.Context, pvName string, namespace string) (*aitrigramv1.BackendReference, error) {
	pv := &corev1.PersistentVolume{}
	err := p.client.Get(ctx, types.NamespacedName{Name: pvName}, pv)
	if err != nil {
		return nil, fmt.Errorf("failed to get PV %s: %w", pvName, err)
	}

	// Generate subPath for this ModelRepository
	subPath := fmt.Sprintf("/modelrepositories/%s", p.modelRepo.Name)

	// Extract backend based on PV source type
	// NFS
	if pv.Spec.NFS != nil {
		filesystemID := fmt.Sprintf("nfs://%s%s", pv.Spec.NFS.Server, pv.Spec.NFS.Path)
		return &aitrigramv1.BackendReference{
			Type: "nfs",
			Details: map[string]string{
				"filesystemID": filesystemID,
				"server":       pv.Spec.NFS.Server,
				"exportPath":   pv.Spec.NFS.Path,
				"subPath":      subPath,
				// PVC info needed for mounting
				"pvcName":      p.pvcName,
				"pvcNamespace": namespace,
			},
		}, nil
	}

	// CSI (most modern storage)
	if pv.Spec.CSI != nil {
		details := map[string]string{
			"driver":   pv.Spec.CSI.Driver,
			"volumeID": pv.Spec.CSI.VolumeHandle,
			"subPath":  subPath,
			// PVC info needed for mounting
			"pvcName":      p.pvcName,
			"pvcNamespace": namespace,
		}

		// Add CSI volume attributes
		for k, v := range pv.Spec.CSI.VolumeAttributes {
			details[fmt.Sprintf("csi.%s", k)] = v
		}

		// Determine backend type from driver
		backendType := "csi"
		if pv.Spec.CSI.Driver == "nfs.csi.k8s.io" {
			backendType = "nfs"
			if server, ok := pv.Spec.CSI.VolumeAttributes["server"]; ok {
				if share, ok := pv.Spec.CSI.VolumeAttributes["share"]; ok {
					details["filesystemID"] = fmt.Sprintf("nfs://%s%s", server, share)
					details["server"] = server
					details["exportPath"] = share
				}
			}
		} else if pv.Spec.CSI.Driver == "cephfs.csi.ceph.com" {
			backendType = "cephfs"
			details["filesystemID"] = fmt.Sprintf("ceph://%s", pv.Spec.CSI.VolumeHandle)
		} else if pv.Spec.CSI.Driver == "driver.longhorn.io" {
			backendType = "longhorn"
		}

		return &aitrigramv1.BackendReference{
			Type:    backendType,
			Details: details,
		}, nil
	}

	// AWS EBS
	if pv.Spec.AWSElasticBlockStore != nil {
		return &aitrigramv1.BackendReference{
			Type: "ebs",
			Details: map[string]string{
				"volumeID": pv.Spec.AWSElasticBlockStore.VolumeID,
				"fsType":   pv.Spec.AWSElasticBlockStore.FSType,
				// PVC info needed for mounting
				"pvcName":      p.pvcName,
				"pvcNamespace": namespace,
			},
		}, nil
	}

	// GCE Persistent Disk
	if pv.Spec.GCEPersistentDisk != nil {
		return &aitrigramv1.BackendReference{
			Type: "gce-pd",
			Details: map[string]string{
				"pdName": pv.Spec.GCEPersistentDisk.PDName,
				"fsType": pv.Spec.GCEPersistentDisk.FSType,
				// PVC info needed for mounting
				"pvcName":      p.pvcName,
				"pvcNamespace": namespace,
			},
		}, nil
	}

	// Azure Disk
	if pv.Spec.AzureDisk != nil {
		return &aitrigramv1.BackendReference{
			Type: "azure-disk",
			Details: map[string]string{
				"diskName": pv.Spec.AzureDisk.DiskName,
				"diskURI":  pv.Spec.AzureDisk.DataDiskURI,
				// PVC info needed for mounting
				"pvcName":      p.pvcName,
				"pvcNamespace": namespace,
			},
		}, nil
	}

	// CephFS
	if pv.Spec.CephFS != nil {
		monitors := ""
		if len(pv.Spec.CephFS.Monitors) > 0 {
			monitors = pv.Spec.CephFS.Monitors[0]
		}
		filesystemID := fmt.Sprintf("ceph://%s%s", monitors, pv.Spec.CephFS.Path)
		return &aitrigramv1.BackendReference{
			Type: "cephfs",
			Details: map[string]string{
				"filesystemID": filesystemID,
				"monitors":     monitors,
				"path":         pv.Spec.CephFS.Path,
				"subPath":      subPath,
				// PVC info needed for mounting
				"pvcName":      p.pvcName,
				"pvcNamespace": namespace,
			},
		}, nil
	}

	// GlusterFS
	if pv.Spec.Glusterfs != nil {
		filesystemID := fmt.Sprintf("gluster://%s/%s", pv.Spec.Glusterfs.EndpointsName, pv.Spec.Glusterfs.Path)
		return &aitrigramv1.BackendReference{
			Type: "glusterfs",
			Details: map[string]string{
				"filesystemID": filesystemID,
				"endpoints":    pv.Spec.Glusterfs.EndpointsName,
				"path":         pv.Spec.Glusterfs.Path,
				"subPath":      subPath,
				// PVC info needed for mounting
				"pvcName":      p.pvcName,
				"pvcNamespace": namespace,
			},
		}, nil
	}

	// HostPath (should not happen in PVC, but handle it)
	if pv.Spec.HostPath != nil {
		return &aitrigramv1.BackendReference{
			Type: "hostpath",
			Details: map[string]string{
				"path": pv.Spec.HostPath.Path,
				// Even for hostpath, might be mounted via PVC in some cases
				"pvcName":      p.pvcName,
				"pvcNamespace": namespace,
			},
		}, nil
	}

	// Unknown PV type - return error instead of masking with "unknown"
	return nil, fmt.Errorf("unable to determine backend type for PV %s (no recognized volume source found)", pvName)
}
