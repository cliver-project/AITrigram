# E2E Tests for ModelRepository with Ollama

This directory contains end-to-end test manifests for testing ModelRepository with Ollama models across different storage types.

## Test Files

### Automated E2E Tests (Ginkgo)

- **`e2e_storage_ollama_test.go`**: Automated test suite that runs as part of `make test-e2e`
  - Tests HostPath, PVC (RWX/RWO), and NFS storage
  - Automatically deploys a test NFS server for NFS storage tests
  - Automatically skips tests when prerequisites are not met
  - Verifies download job completion and ModelRepository status

### Manual Test Manifests

1. **`e2e-01-ollama-nfs.yaml`**: NFS storage test
2. **`e2e-02-ollama-hostpath.yaml`**: HostPath storage test
3. **`e2e-03-ollama-pvc-rwx.yaml`**: PVC with ReadWriteMany test
4. **`e2e-04-ollama-pvc-rwo.yaml`**: PVC with ReadWriteOnce test
5. **`nfs-server-deployment.yaml`**: Test NFS server deployment (automatically used by e2e tests)

## Prerequisites

### All Tests
- Kubernetes cluster (minikube or cloud provider)
- AITrigram operator installed and running
- kubectl configured to access the cluster

### Storage-Specific Prerequisites

#### HostPath Storage
- Works out-of-the-box with minikube and single-node clusters
- No additional setup required

#### PVC ReadWriteOnce (RWO)
- Any storage class supporting RWO (available in most clusters)
- Default storage class is usually sufficient

#### PVC ReadWriteMany (RWX)
- Storage class that supports RWX access mode
- Examples:
  - **minikube**: Install `nfs-subdir-external-provisioner` or use hostPath provisioner
  - **AWS**: Use EFS storage class
  - **Azure**: Use Azure Files storage class
  - **GCP**: Use Filestore storage class

#### NFS Storage
- **Automated tests**: The e2e test automatically deploys a lightweight NFS server at `nfs-server.default.svc.cluster.local`
- **Manual tests**: You can either:
  - Deploy the test NFS server: `kubectl apply -f test/e2e/storage/nfs-server-deployment.yaml`
  - Use your own NFS server by updating the `server` and `path` fields in the YAML

## Running Automated E2E Tests

### Run all e2e tests (including storage tests)

**For minikube:**
```bash
USE_MINIKUBE=true make test-e2e
```

**For kind (legacy):**
```bash
make test-e2e
```

### Run only storage tests with verbose output
```bash
cd test/e2e
ginkgo -v --focus="ModelRepository Storage Tests"
```

### Run specific storage type test
```bash
# HostPath only
ginkgo -v --focus="HostPath Storage"

# PVC RWX only
ginkgo -v --focus="PVC ReadWriteMany"

# PVC RWO only
ginkgo -v --focus="PVC ReadWriteOnce"

# NFS only (requires NFS_SERVER env var)
NFS_SERVER=192.168.1.100 ginkgo -v --focus="NFS Storage"
```

## Running Manual Tests

### 1. HostPath Storage Test

This is the simplest test and works well with minikube clusters:

```bash
# Apply the ModelRepository
kubectl apply -f test/e2e/storage/e2e-02-ollama-hostpath.yaml

# Watch the download job
kubectl get jobs -w -l modelrepository.aitrigram.cliver-project.github.io/name=ollama-hostpath-test

# Check the job logs
kubectl logs -f job/ollama-hostpath-test-download

# Verify ModelRepository status
kubectl get modelrepository ollama-hostpath-test -o yaml

# Expected status.phase: "downloaded"
# Expected status.boundNodeName: <node-name>

# Cleanup
kubectl delete modelrepository ollama-hostpath-test
```

### 2. PVC ReadWriteOnce (RWO) Test

```bash
# Create a test namespace
kubectl create namespace test-mr-rwo

# Apply the manifests (creates PVC and ModelRepository)
kubectl apply -f test/e2e/storage/e2e-04-ollama-pvc-rwo.yaml -n test-mr-rwo

# Watch the PVC status
kubectl get pvc -n test-mr-rwo -w

# Watch the download job
kubectl get jobs -n test-mr-rwo -w

# Check download logs
kubectl logs -n test-mr-rwo -f job/ollama-pvc-rwo-test-download

# Verify ModelRepository status
kubectl get modelrepository ollama-pvc-rwo-test -o yaml

# Expected status.phase: "downloaded"
# Expected status.boundNodeName: <node-name> (for RWO)

# Cleanup
kubectl delete modelrepository ollama-pvc-rwo-test
kubectl delete pvc model-storage-rwo-test -n test-mr-rwo
kubectl delete namespace test-mr-rwo
```

### 3. PVC ReadWriteMany (RWX) Test

**Note**: Requires a storage class that supports RWX. Update the `storageClassName` in the YAML file first.

```bash
# Check available storage classes
kubectl get storageclass

# Update the YAML file with your RWX storage class name
# Edit: storageClassName: "your-rwx-storage-class"

# Create a test namespace
kubectl create namespace test-mr-rwx

# Apply the manifests
kubectl apply -f test/e2e/storage/e2e-03-ollama-pvc-rwx.yaml -n test-mr-rwx

# Watch the download job
kubectl get jobs -n test-mr-rwx -w

# Verify ModelRepository status
kubectl get modelrepository ollama-pvc-rwx-test -o yaml

# Expected status.phase: "downloaded"
# Note: status.boundNodeName may be empty for RWX (not node-bound)

# Cleanup
kubectl delete modelrepository ollama-pvc-rwx-test
kubectl delete pvc model-storage-rwx-test -n test-mr-rwx
kubectl delete namespace test-mr-rwx
```

### 4. NFS Storage Test

**Option A: Using the test NFS server** (Recommended for testing)

```bash
# Deploy the test NFS server
kubectl apply -f test/e2e/storage/nfs-server-deployment.yaml

# Wait for NFS server to be ready
kubectl wait --for=condition=available --timeout=3m deployment/nfs-server -n default

# Verify NFS server is running
kubectl get pods -n default -l app=nfs-server

# Apply the ModelRepository
kubectl apply -f test/e2e/storage/e2e-01-ollama-nfs.yaml

# Watch the download job
kubectl get jobs -w -l modelrepository.aitrigram.cliver-project.github.io/name=ollama-nfs-test

# Verify ModelRepository status
kubectl get modelrepository ollama-nfs-test -o yaml

# Expected status.phase: "downloaded"

# Cleanup
kubectl delete modelrepository ollama-nfs-test
kubectl delete -f test/e2e/storage/nfs-server-deployment.yaml
```

**Option B: Using your own NFS server**

```bash
# Update the YAML file with your NFS server details:
# Edit e2e-01-ollama-nfs.yaml:
# - server: <your-nfs-server-ip>
# - path: <your-nfs-export-path>

# Apply the ModelRepository
kubectl apply -f test/e2e/storage/e2e-01-ollama-nfs.yaml

# Watch the download job
kubectl get jobs -w -l modelrepository.aitrigram.cliver-project.github.io/name=ollama-nfs-test

# Verify ModelRepository status
kubectl get modelrepository ollama-nfs-test -o yaml

# Cleanup
kubectl delete modelrepository ollama-nfs-test
```

## Verifying Test Success

For all tests, successful completion means:

1. **Download Job Completes**:
   ```bash
   kubectl get job <job-name> -o jsonpath='{.status.succeeded}'
   # Output: 1
   ```

2. **ModelRepository Phase is "downloaded"**:
   ```bash
   kubectl get modelrepository <name> -o jsonpath='{.status.phase}'
   # Output: downloaded
   ```

3. **For HostPath and RWO: boundNodeName is set**:
   ```bash
   kubectl get modelrepository <name> -o jsonpath='{.status.boundNodeName}'
   # Output: <node-name>
   ```

4. **No errors in controller logs**:
   ```bash
   kubectl logs -n aitrigram-system deployment/aitrigram-controller-manager
   ```

## Troubleshooting

### Download Job Fails

```bash
# Check job status
kubectl describe job <job-name>

# Check pod logs
kubectl get pods -l job-name=<job-name>
kubectl logs <pod-name>

# Common issues:
# - Network connectivity to download source
# - Insufficient storage space
# - Permission issues with storage
```

### PVC Stuck in Pending

```bash
# Check PVC events
kubectl describe pvc <pvc-name>

# Common issues:
# - No storage class available
# - Storage class doesn't support requested access mode
# - No available storage in the cluster
```

### ModelRepository Status Stuck

```bash
# Check ModelRepository events
kubectl describe modelrepository <name>

# Check controller logs
kubectl logs -n aitrigram-system -l control-plane=controller-manager --tail=100

# Check if job exists
kubectl get jobs -l modelrepository.aitrigram.cliver-project.github.io/name=<name>
```

## Testing with Different Models

The test manifests use `qwen2.5:0.5b` which is a small model (~0.5GB) for fast testing. You can test with other models by changing the `modelId`:

```yaml
spec:
  source:
    origin: ollama
    modelId: llama3.2:1b  # ~1.3GB
    # or
    modelId: phi3:mini     # ~2.3GB
```

**Note**: Larger models will take longer to download and require more storage space.

## CI/CD Integration

The automated e2e tests are designed to run in CI environments:

```bash
# GitHub Actions example
- name: Run E2E Storage Tests
  run: |
    make test-e2e
  env:
    # Optional: Set NFS server if available
    NFS_SERVER: ${{ secrets.NFS_SERVER }}
```

Tests automatically skip when prerequisites are not met, so they won't fail in environments without specific storage types.

## Model Storage Locations

After successful download, models are stored at:

- **HostPath**: `/mnt/aitrigram-models/e2e-test/<model-id>` on the node
- **PVC**: `/data/models/<model-id>` in the PVC
- **NFS**: `<nfs-path>/<model-id>` on the NFS server

You can verify the model files exist by inspecting the download job pod or mounting the storage.
