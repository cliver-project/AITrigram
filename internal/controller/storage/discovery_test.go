package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestStorageClassDiscovery_DiscoverStorageClasses(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = storagev1.AddToScheme(scheme)

	tests := []struct {
		name             string
		existingClasses  []storagev1.StorageClass
		expectedCount    int
		expectedContains []string // Types we expect to find
	}{
		{
			name: "discover NFS storage class",
			existingClasses: []storagev1.StorageClass{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "nfs-client",
					},
					Provisioner: "nfs.csi.k8s.io",
				},
			},
			expectedCount:    1,
			expectedContains: []string{"nfs"},
		},
		{
			name: "discover multiple storage classes",
			existingClasses: []storagev1.StorageClass{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "nfs-client",
						Annotations: map[string]string{
							"storageclass.kubernetes.io/is-default-class": "true",
						},
					},
					Provisioner: "nfs.csi.k8s.io",
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "gp2",
					},
					Provisioner: "ebs.csi.aws.com",
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "cephfs",
					},
					Provisioner: "cephfs.csi.ceph.com",
				},
			},
			expectedCount:    3,
			expectedContains: []string{"nfs", "block", "cephfs"},
		},
		{
			name:             "no storage classes",
			existingClasses:  []storagev1.StorageClass{},
			expectedCount:    0,
			expectedContains: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(toObjects(tt.existingClasses)...).Build()
			discovery := NewStorageClassDiscovery(client)

			result, err := discovery.DiscoverStorageClasses(context.Background())
			require.NoError(t, err)
			assert.Len(t, result, tt.expectedCount)

			// Verify expected types are found
			foundTypes := make(map[string]bool)
			for _, info := range result {
				foundTypes[info.Type] = true
			}

			for _, expectedType := range tt.expectedContains {
				assert.True(t, foundTypes[expectedType], "expected to find type %s", expectedType)
			}
		})
	}
}

func TestStorageClassDiscovery_CategorizeStorageClass(t *testing.T) {
	discovery := &StorageClassDiscovery{}

	tests := []struct {
		name               string
		sc                 *storagev1.StorageClass
		expectedType       string
		expectedAccessMode corev1.PersistentVolumeAccessMode
		expectedIsDefault  bool
	}{
		{
			name: "NFS storage class",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "nfs-client"},
				Provisioner: "nfs.csi.k8s.io",
			},
			expectedType:       "nfs",
			expectedAccessMode: corev1.ReadWriteMany,
			expectedIsDefault:  false,
		},
		{
			name: "NFS subdir provisioner",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "nfs-subdir"},
				Provisioner: "cluster.local/nfs-subdir-external-provisioner",
			},
			expectedType:       "nfs",
			expectedAccessMode: corev1.ReadWriteMany,
		},
		{
			name: "CephFS storage class",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "cephfs"},
				Provisioner: "cephfs.csi.ceph.com",
			},
			expectedType:       "cephfs",
			expectedAccessMode: corev1.ReadWriteMany,
		},
		{
			name: "GlusterFS storage class",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "glusterfs"},
				Provisioner: "kubernetes.io/glusterfs",
			},
			expectedType:       "glusterfs",
			expectedAccessMode: corev1.ReadWriteMany,
		},
		{
			name: "AWS EBS storage class",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "gp2"},
				Provisioner: "ebs.csi.aws.com",
			},
			expectedType:       "block",
			expectedAccessMode: corev1.ReadWriteOnce,
		},
		{
			name: "GCE PD storage class",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "standard"},
				Provisioner: "kubernetes.io/gce-pd",
			},
			expectedType:       "block",
			expectedAccessMode: corev1.ReadWriteOnce,
		},
		{
			name: "Azure Disk storage class",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "managed-premium"},
				Provisioner: "kubernetes.io/azure-disk",
			},
			expectedType:       "block",
			expectedAccessMode: corev1.ReadWriteOnce,
		},
		{
			name: "Longhorn storage class",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "longhorn"},
				Provisioner: "driver.longhorn.io",
			},
			expectedType:       "longhorn",
			expectedAccessMode: corev1.ReadWriteMany,
		},
		{
			name: "local-path storage class (kind/k3s)",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "standard"},
				Provisioner: "rancher.io/local-path",
			},
			expectedType:       "local",
			expectedAccessMode: corev1.ReadWriteOnce,
		},
		{
			name: "minikube hostpath storage class",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "standard"},
				Provisioner: "k8s.io/minikube-hostpath",
			},
			expectedType:       "local",
			expectedAccessMode: corev1.ReadWriteOnce,
		},
		{
			name: "Unknown provisioner defaults to block RWO",
			sc: &storagev1.StorageClass{
				ObjectMeta:  metav1.ObjectMeta{Name: "custom"},
				Provisioner: "custom.example.com/storage",
			},
			expectedType:       "unknown",
			expectedAccessMode: corev1.ReadWriteOnce,
		},
		{
			name: "Default storage class annotation",
			sc: &storagev1.StorageClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default-sc",
					Annotations: map[string]string{
						"storageclass.kubernetes.io/is-default-class": "true",
					},
				},
				Provisioner: "ebs.csi.aws.com",
			},
			expectedType:       "block",
			expectedAccessMode: corev1.ReadWriteOnce,
			expectedIsDefault:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := discovery.categorizeStorageClass(tt.sc)
			assert.Equal(t, tt.expectedType, info.Type)
			assert.Equal(t, tt.expectedAccessMode, info.AccessMode)
			assert.Equal(t, tt.sc.Name, info.Name)
			assert.Equal(t, tt.sc.Provisioner, info.Provisioner)
			if tt.expectedIsDefault {
				assert.True(t, info.IsDefault)
			}
		})
	}
}

func TestStorageClassDiscovery_SelectBestStorageClass(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = storagev1.AddToScheme(scheme)

	tests := []struct {
		name            string
		existingClasses []storagev1.StorageClass
		preference      string
		expectedType    string
		expectedName    string
		expectError     bool
	}{
		{
			name: "auto preference selects RWX over RWO",
			existingClasses: []storagev1.StorageClass{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "gp2"},
					Provisioner: "ebs.csi.aws.com",
				},
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "nfs-client"},
					Provisioner: "nfs.csi.k8s.io",
				},
			},
			preference:   "auto",
			expectedType: "nfs",
			expectedName: "nfs-client",
		},
		{
			name: "auto preference selects default SC when no RWX available",
			existingClasses: []storagev1.StorageClass{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "gp2"},
					Provisioner: "ebs.csi.aws.com",
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "gp3",
						Annotations: map[string]string{
							"storageclass.kubernetes.io/is-default-class": "true",
						},
					},
					Provisioner: "ebs.csi.aws.com",
				},
			},
			preference:   "auto",
			expectedType: "block",
			expectedName: "gp3",
		},
		{
			name: "specific SC name is selected",
			existingClasses: []storagev1.StorageClass{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "gp2"},
					Provisioner: "ebs.csi.aws.com",
				},
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "custom-sc"},
					Provisioner: "custom.example.com/storage",
				},
			},
			preference:   "custom-sc",
			expectedType: "unknown",
			expectedName: "custom-sc",
		},
		{
			name:        "no storage classes returns error",
			preference:  "auto",
			expectError: true,
		},
		{
			name: "non-existent SC name returns error",
			existingClasses: []storagev1.StorageClass{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "gp2"},
					Provisioner: "ebs.csi.aws.com",
				},
			},
			preference:  "does-not-exist",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(toObjects(tt.existingClasses)...).Build()
			discovery := NewStorageClassDiscovery(client)

			result, err := discovery.SelectBestStorageClass(context.Background(), tt.preference)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedType, result.Type)
			if tt.expectedName != "" {
				assert.Equal(t, tt.expectedName, result.Name)
			}
		})
	}
}

func TestStorageClassDiscovery_SelectAuto(t *testing.T) {
	discovery := &StorageClassDiscovery{}

	tests := []struct {
		name         string
		available    []StorageClassInfo
		expectedType string
		expectedName string
		expectError  bool
	}{
		{
			name: "prefers RWX over default",
			available: []StorageClassInfo{
				{Name: "gp2", Type: "block", AccessMode: corev1.ReadWriteOnce, IsDefault: true},
				{Name: "nfs", Type: "nfs", AccessMode: corev1.ReadWriteMany, IsDefault: false},
			},
			expectedType: "nfs",
			expectedName: "nfs",
		},
		{
			name: "prefers default when no RWX",
			available: []StorageClassInfo{
				{Name: "gp2", Type: "block", AccessMode: corev1.ReadWriteOnce, IsDefault: false},
				{Name: "gp3", Type: "block", AccessMode: corev1.ReadWriteOnce, IsDefault: true},
			},
			expectedType: "block",
			expectedName: "gp3",
		},
		{
			name: "uses any RWO when no RWX or default",
			available: []StorageClassInfo{
				{Name: "gp2", Type: "block", AccessMode: corev1.ReadWriteOnce, IsDefault: false},
			},
			expectedType: "block",
			expectedName: "gp2",
		},
		{
			name:        "error when no storage classes available",
			available:   []StorageClassInfo{},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := discovery.selectAuto(tt.available)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedType, result.Type)
			if tt.expectedName != "" {
				assert.Equal(t, tt.expectedName, result.Name)
			}
		})
	}
}

// Helper function to convert StorageClass slice to client.Object slice
func toObjects(classes []storagev1.StorageClass) []client.Object {
	objects := make([]client.Object, len(classes))
	for i := range classes {
		objects[i] = &classes[i]
	}
	return objects
}
