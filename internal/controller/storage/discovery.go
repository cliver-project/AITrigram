package storage

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// StorageClassInfo contains information about a storage class
type StorageClassInfo struct {
	Name        string                            // Kubernetes StorageClass name
	Provisioner string                            // Provisioner name
	Type        string                            // User-friendly type (nfs, cephfs, longhorn, local, block, etc.)
	AccessMode  corev1.PersistentVolumeAccessMode // PVC access mode (ReadWriteMany or ReadWriteOnce)
	IsDefault   bool                              // Is this the default storage class
}

// StorageClassDiscovery discovers available storage options
type StorageClassDiscovery struct {
	client client.Client
}

// NewStorageClassDiscovery creates a new storage class discovery service
func NewStorageClassDiscovery(c client.Client) *StorageClassDiscovery {
	return &StorageClassDiscovery{
		client: c,
	}
}

// DiscoverStorageClasses discovers all available storage classes
func (d *StorageClassDiscovery) DiscoverStorageClasses(ctx context.Context) ([]StorageClassInfo, error) {
	scList := &storagev1.StorageClassList{}
	if err := d.client.List(ctx, scList); err != nil {
		return nil, fmt.Errorf("failed to list storage classes: %w", err)
	}

	result := make([]StorageClassInfo, 0, len(scList.Items))
	for _, sc := range scList.Items {
		info := d.categorizeStorageClass(&sc)
		result = append(result, info)
	}

	return result, nil
}

// SelectBestStorageClass selects the best storage class based on user preference
func (d *StorageClassDiscovery) SelectBestStorageClass(
	ctx context.Context,
	preference string,
) (*StorageClassInfo, error) {
	available, err := d.DiscoverStorageClasses(ctx)
	if err != nil {
		return nil, err
	}

	if len(available) == 0 {
		return nil, fmt.Errorf("no StorageClass found in the cluster; at least one StorageClass is required")
	}

	switch preference {
	case "auto", "":
		// Priority: 1. RWX shared, 2. Default SC, 3. Any RWO
		return d.selectAuto(available)

	default:
		// Treat as specific SC name
		sc := d.selectByName(available, preference)
		if sc != nil {
			return sc, nil
		}
		return nil, fmt.Errorf("storage class %q not found", preference)
	}
}

// categorizeStorageClass categorizes a storage class by its provisioner
func (d *StorageClassDiscovery) categorizeStorageClass(sc *storagev1.StorageClass) StorageClassInfo {
	info := StorageClassInfo{
		Name:        sc.Name,
		Provisioner: sc.Provisioner,
		IsDefault:   sc.Annotations != nil && sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true",
	}

	provisioner := strings.ToLower(sc.Provisioner)

	// Detect type and access mode from provisioner
	switch {
	case strings.Contains(provisioner, "nfs"):
		info.Type = "nfs"
		info.AccessMode = corev1.ReadWriteMany
	case strings.Contains(provisioner, "cephfs") || strings.Contains(provisioner, "ceph.rbd.csi"):
		info.Type = "cephfs"
		info.AccessMode = corev1.ReadWriteMany
	case strings.Contains(provisioner, "glusterfs") || strings.Contains(provisioner, "gluster"):
		info.Type = "glusterfs"
		info.AccessMode = corev1.ReadWriteMany
	case provisioner == "driver.longhorn.io":
		info.Type = "longhorn"
		info.AccessMode = corev1.ReadWriteMany
	case strings.Contains(provisioner, "hostpath") || strings.Contains(provisioner, "local"):
		info.Type = "local"
		info.AccessMode = corev1.ReadWriteOnce
	case strings.Contains(provisioner, "ebs") || strings.Contains(provisioner, "gce-pd") ||
		strings.Contains(provisioner, "azure-disk") || strings.Contains(provisioner, "cinder"):
		info.Type = "block"
		info.AccessMode = corev1.ReadWriteOnce
	default:
		// Unknown provisioner, assume RWO as most conservative
		info.Type = "unknown"
		info.AccessMode = corev1.ReadWriteOnce
	}

	return info
}

// selectAuto selects the best storage class automatically
// Priority: 1. RWX shared, 2. Default SC, 3. Any RWO
func (d *StorageClassDiscovery) selectAuto(available []StorageClassInfo) (*StorageClassInfo, error) {
	// 1. Prefer RWX shared storage
	for i := range available {
		if available[i].AccessMode == corev1.ReadWriteMany {
			return &available[i], nil
		}
	}

	// 2. Use default storage class
	for i := range available {
		if available[i].IsDefault {
			return &available[i], nil
		}
	}

	// 3. Any RWO storage
	for i := range available {
		if available[i].AccessMode == corev1.ReadWriteOnce {
			return &available[i], nil
		}
	}

	return nil, fmt.Errorf("no suitable StorageClass found among %d available", len(available))
}

// selectByName finds a storage class by exact name
func (d *StorageClassDiscovery) selectByName(available []StorageClassInfo, name string) *StorageClassInfo {
	for i := range available {
		if available[i].Name == name {
			return &available[i]
		}
	}
	return nil
}
