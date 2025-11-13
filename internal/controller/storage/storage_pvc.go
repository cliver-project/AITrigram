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

	aitrigramv1 "github.com/cliver-project/AITrigram/api/v1"
)

// PVCProvider implements storage using PersistentVolumeClaims
type PVCProvider struct {
	client    client.Client
	scheme    *runtime.Scheme
	modelRepo *aitrigramv1.ModelRepository
	spec      *aitrigramv1.PVCStorageSpec
	mountPath string
	pvcName   string
}

// NewPVCProvider creates a new PVC storage provider
func NewPVCProvider(
	c client.Client,
	scheme *runtime.Scheme,
	modelRepo *aitrigramv1.ModelRepository,
	spec *aitrigramv1.PVCStorageSpec,
	mountPath string,
) (*PVCProvider, error) {
	if spec == nil {
		return nil, fmt.Errorf("PVC storage spec is required")
	}

	mountPath = getOrDefaultMountPath(mountPath)

	// PVC name is deterministic based on ModelRepository name
	pvcName := fmt.Sprintf("%s-storage", modelRepo.Name)
	if spec.ExistingClaim != nil {
		pvcName = *spec.ExistingClaim
	}

	return &PVCProvider{
		client:    c,
		scheme:    scheme,
		modelRepo: modelRepo,
		spec:      spec,
		mountPath: mountPath,
		pvcName:   pvcName,
	}, nil
}

// GetType returns the storage type
func (p *PVCProvider) GetType() aitrigramv1.StorageType {
	return aitrigramv1.StorageTypePVC
}

// ValidateConfig validates the PVC configuration
func (p *PVCProvider) ValidateConfig() error {
	// If using existing claim, don't validate size
	if p.spec.ExistingClaim != nil {
		return nil
	}

	if p.spec.Size == "" {
		return fmt.Errorf("storage size is required for PVC")
	}

	if _, err := resource.ParseQuantity(p.spec.Size); err != nil {
		return fmt.Errorf("invalid size %q: %w", p.spec.Size, err)
	}

	// Validate access mode
	accessMode := p.getAccessMode()
	if accessMode != corev1.ReadWriteMany && accessMode != corev1.ReadWriteOnce {
		return fmt.Errorf("access mode must be ReadWriteMany or ReadWriteOnce, got %s", accessMode)
	}

	return nil
}

// PrepareDownloadVolume prepares volume for download job
func (p *PVCProvider) PrepareDownloadVolume() (*corev1.Volume, *corev1.VolumeMount, error) {
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

// GetReadOnlyVolumeSource returns a read-only volume source
func (p *PVCProvider) GetReadOnlyVolumeSource() (*corev1.VolumeSource, error) {
	return &corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
			ClaimName: p.pvcName,
			ReadOnly:  true,
		},
	}, nil
}

// GetMountPath returns the mount path
func (p *PVCProvider) GetMountPath() string {
	return p.mountPath
}

// GetNodeAffinity returns node affinity for RWO volumes
func (p *PVCProvider) GetNodeAffinity() *corev1.NodeAffinity {
	// For RWO, need to bind to the node where the volume is bound
	if p.getAccessMode() == corev1.ReadWriteOnce {
		// Check if we have a bound node in status
		if p.modelRepo.Status.BoundNodeName != "" {
			return &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      "kubernetes.io/hostname",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{p.modelRepo.Status.BoundNodeName},
								},
							},
						},
					},
				},
			}
		}
	}

	return nil // RWX - no node affinity needed
}

// IsShared returns true if the storage is accessible from multiple nodes
func (p *PVCProvider) IsShared() bool {
	return p.getAccessMode() == corev1.ReadWriteMany
}

// CreateStorage provisions the PVC
// Typically the namespace is the one where the controller is running at: aitrigram-system
func (p *PVCProvider) CreateStorage(ctx context.Context, namespace string) error {
	// If using existing claim, just verify it exists
	if p.spec.ExistingClaim != nil {
		return p.validateExistingClaim(ctx, namespace)
	}

	// Create new PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.pvcName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "model-storage",
				"app.kubernetes.io/managed-by": "aitrigram-operator",
				// label it with ModelRepository name for tracking
				"aitrigram.io/model-repo": p.modelRepo.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				p.getAccessMode(),
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(p.spec.Size),
				},
			},
		},
	}

	if p.spec.StorageClassName != nil {
		pvc.Spec.StorageClassName = p.spec.StorageClassName
	}

	if p.spec.VolumeMode != nil {
		pvc.Spec.VolumeMode = p.spec.VolumeMode
	}

	if p.spec.Selector != nil {
		pvc.Spec.Selector = p.spec.Selector
	}

	// Set owner reference to ModelRepository
	// Note: ModelRepository is cluster-scoped, PVC is namespace-scoped
	// We cannot use SetControllerReference for cross-scope, so just add label
	// The PVC will be cleaned up based on retainPolicy

	// Check if PVC already exists
	existing := &corev1.PersistentVolumeClaim{}
	err := p.client.Get(ctx, types.NamespacedName{Name: p.pvcName, Namespace: namespace}, existing)
	if err == nil {
		// PVC already exists, nothing to do
		return nil
	}

	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing PVC: %w", err)
	}

	// Create the PVC
	if err := p.client.Create(ctx, pvc); err != nil {
		return fmt.Errorf("failed to create PVC %s/%s: %w", namespace, p.pvcName, err)
	}

	return nil
}

// validateExistingClaim checks if the referenced PVC exists
func (p *PVCProvider) validateExistingClaim(ctx context.Context, namespace string) error {
	pvc := &corev1.PersistentVolumeClaim{}
	err := p.client.Get(ctx, types.NamespacedName{Name: p.pvcName, Namespace: namespace}, pvc)
	if err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("existing PVC %s/%s not found", namespace, p.pvcName)
		}
		return fmt.Errorf("failed to get existing PVC %s/%s: %w", namespace, p.pvcName, err)
	}

	// Verify the PVC is bound or pending
	if pvc.Status.Phase != corev1.ClaimBound && pvc.Status.Phase != corev1.ClaimPending {
		return fmt.Errorf("existing PVC %s/%s is in invalid state: %s", namespace, p.pvcName, pvc.Status.Phase)
	}

	return nil
}

// GetStatus returns the current status of the PVC
// namespace is the namespace where the controller is running (typically aitrigram-system)
func (p *PVCProvider) GetStatus(ctx context.Context, namespace string) (*StorageStatus, error) {
	// Get the PVC from the cluster
	pvc := &corev1.PersistentVolumeClaim{}
	err := p.client.Get(ctx, types.NamespacedName{Name: p.pvcName, Namespace: namespace}, pvc)
	if err != nil {
		if errors.IsNotFound(err) {
			return &StorageStatus{
				Ready:   false,
				Message: fmt.Sprintf("PVC %s/%s not found", namespace, p.pvcName),
			}, nil
		}
		return nil, fmt.Errorf("failed to get PVC status: %w", err)
	}

	status := &StorageStatus{
		Ready:   pvc.Status.Phase == corev1.ClaimBound,
		Message: string(pvc.Status.Phase),
	}

	if capacity := pvc.Status.Capacity[corev1.ResourceStorage]; !capacity.IsZero() {
		status.Capacity = capacity.String()
	}

	// For RWO volumes, include bound node information
	if p.getAccessMode() == corev1.ReadWriteOnce && p.modelRepo.Status.BoundNodeName != "" {
		status.BoundNodeName = p.modelRepo.Status.BoundNodeName
	}

	return status, nil
}

// Cleanup removes the PVC if retainPolicy is Delete
// namespace is the namespace where the controller is running (typically aitrigram-system)
func (p *PVCProvider) Cleanup(ctx context.Context, namespace string) error {
	// If using existing claim, never delete it
	if p.spec.ExistingClaim != nil {
		return nil
	}

	// Check retain policy
	retainPolicy := p.getRetainPolicy()
	if retainPolicy == aitrigramv1.RetainPolicyRetain {
		return nil
	}

	// Delete the PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.pvcName,
			Namespace: namespace,
		},
	}

	err := p.client.Delete(ctx, pvc)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete PVC %s/%s: %w", namespace, p.pvcName, err)
	}

	return nil
}

// getAccessMode returns the access mode, defaulting to ReadWriteMany
func (p *PVCProvider) getAccessMode() corev1.PersistentVolumeAccessMode {
	if p.spec.AccessMode == "" {
		return corev1.ReadWriteMany
	}
	return p.spec.AccessMode
}

// getRetainPolicy returns the retain policy with appropriate defaults
func (p *PVCProvider) getRetainPolicy() aitrigramv1.RetainPolicy {
	if p.spec.RetainPolicy != nil {
		return *p.spec.RetainPolicy
	}

	// Default: Delete for created PVCs, Retain for existing claims
	if p.spec.ExistingClaim != nil {
		return aitrigramv1.RetainPolicyRetain
	}
	return aitrigramv1.RetainPolicyDelete
}
