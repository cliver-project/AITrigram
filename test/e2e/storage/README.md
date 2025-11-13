# Storage Integration Tests

This directory contains integration tests for AITrigram storage providers.

## Overview

Tests validate all storage types:

- **NFS**: Network File System for shared storage
- **HostPath**: Node-local storage for development/testing
- **PVC (RWX)**: ReadWriteMany persistent volumes
- **PVC (RWO)**: ReadWriteOnce persistent volumes
- **Existing PVC**: User-provided persistent volumes

## Prerequisites

### For kind Cluster

1. **Setup kind cluster** with storage support:

   ```bash
   cd /home/lgao/sources/kube/AITrigram
   ./setup-kind-with-proxy.sh
   ```

2. **Install NFS provisioner** (for RWX tests):

   ```bash
   helm repo add nfs-subdir-external-provisioner \
     https://kubernetes-sigs.github.io/nfs-subdir-external-provisioner/

   helm install nfs-subdir-external-provisioner \
     nfs-subdir-external-provisioner/nfs-subdir-external-provisioner \
     --set nfs.server=<NFS_SERVER_IP> \
     --set nfs.path=/exports/models \
     --set storageClass.name=nfs-client
   ```

3. **Deploy operator**:
   ```bash
   make docker-build IMG=aitrigram-operator:dev
   kind load docker-image aitrigram-operator:dev
   make deploy IMG=aitrigram-operator:dev
   ```

### For Other Clusters

- Ensure you have storage classes that support:
  - **RWX** (ReadWriteMany): NFS, CephFS, GlusterFS, etc.
  - **RWO** (ReadWriteOnce): Default in most clusters
- Adjust `storageClassName` in YAML files to match your cluster

## Test Files

| File                       | Description       | Prerequisites             |
| -------------------------- | ----------------- | ------------------------- |
| `01-nfs-storage.yaml`      | NFS storage test  | NFS server accessible     |
| `02-hostpath-storage.yaml` | HostPath test     | Node with /mnt/models     |
| `03-pvc-rwx-storage.yaml`  | RWX PVC test      | RWX storage class         |
| `04-pvc-rwo-storage.yaml`  | RWO PVC test      | RWO storage class         |
| `05-pvc-existing.yaml`     | Existing PVC test | Manually create PVC first |

## Running Tests

### Run All Tests

```bash
cd test/e2e/storage
./run-storage-tests.sh
```

###Run Individual Tests

```bash
# Apply a specific test
kubectl apply -f 02-hostpath-storage.yaml

# Watch the ModelRepository
kubectl get modelrepository test-hostpath-model -w

# Check status
kubectl get modelrepository test-hostpath-model -o yaml

# View download job logs
kubectl logs -n aitrigram-system job/download-test-hostpath-model

# Cleanup
kubectl delete modelrepository test-hostpath-model
```

## Expected Behavior

### Successful Test

```bash
$ kubectl get modelrepository
NAME                  PHASE        AGE
test-hostpath-model   Downloaded   2m

$ kubectl get modelrepository test-hostpath-model -o jsonpath='{.status}'
{
  "phase": "Downloaded",
  "message": "Model downloaded successfully",
  "boundNodeName": "kind-worker",
  "conditions": [...]
}
```

### Node Affinity Validation

For HostPath and RWO PVC tests, verify node binding:

```bash
# Check bound node
kubectl get modelrepository test-pvc-rwo-model -o jsonpath='{.status.boundNodeName}'

# Verify download job ran on correct node
kubectl get job -n aitrigram-system download-test-pvc-rwo-model -o jsonpath='{.spec.template.spec.nodeSelector}'
```

### PVC Validation

For PVC tests, verify PVC creation:

```bash
# List PVCs created by operator
kubectl get pvc -n aitrigram-system -l app.kubernetes.io/managed-by=aitrigram-operator

# Check PVC status
kubectl get pvc test-pvc-rwx-model-storage -n aitrigram-system
```

## Troubleshooting

### ModelRepository Stuck in Pending

```bash
# Check ModelRepository events
kubectl describe modelrepository test-nfs-model

# Check operator logs
kubectl logs -n aitrigram-system deployment/aitrigram-controller-manager

# Check download job
kubectl get jobs -n aitrigram-system
kubectl logs -n aitrigram-system job/download-test-nfs-model
```

### PVC Not Bound

```bash
# Check PVC events
kubectl describe pvc test-pvc-rwx-model-storage -n aitrigram-system

# Check storage class
kubectl get storageclass

# Check PV availability
kubectl get pv
```

### Node Affinity Issues

For RWO/HostPath tests:

```bash
# Verify node exists
kubectl get nodes

# Check if download job has node selector
kubectl get job download-test-hostpath-model -n aitrigram-system -o yaml | grep -A 5 nodeSelector

# Verify node has required labels
kubectl get node kind-worker --show-labels
```

## Cleanup

### Clean Individual Test

```bash
kubectl delete modelrepository <model-name>
```

### Clean All Tests

```bash
kubectl delete modelrepository -l app.kubernetes.io/created-by=storage-integration-tests
```

### Clean Everything

```bash
# Delete all test ModelRepositories
kubectl delete -f test/e2e/storage/

# Delete auto-created PVCs
kubectl delete pvc -n aitrigram-system -l app.kubernetes.io/managed-by=aitrigram-operator
```

## Coverage

These tests validate:

- ✅ Storage provider factory
- ✅ Configuration validation
- ✅ Volume preparation for downloads
- ✅ Node affinity for RWO/HostPath
- ✅ PVC auto-provisioning
- ✅ Existing PVC references
- ✅ Read-only volume sources for LLMEngine
- ✅ Storage cleanup on deletion

## CI/CD Integration

To run in CI/CD:

```bash
# In GitHub Actions / GitLab CI
- name: Run Storage Integration Tests
  run: |
    ./setup-kind-with-proxy.sh
    make deploy IMG=aitrigram-operator:dev
    ./test/e2e/storage/run-storage-tests.sh
```

## Future Enhancements

- [ ] Add S3 storage tests (requires MinIO or S3-compatible service)
- [ ] Add Image storage tests (requires image registry)
- [ ] Add multi-namespace tests
- [ ] Add concurrent model download tests
- [ ] Add storage migration tests
- [ ] Add performance benchmarks

---

**Last Updated**: 2025-11-09
**Maintainer**: AITrigram Team
