# E2E Tests for AITrigram

This directory contains end-to-end tests for the AITrigram operator.

## Test Categories

### 1. Controller Manager Tests (`e2e_test.go`)
Tests basic controller deployment and health:
- Verifies controller-manager pod is running
- Checks pod status and readiness

### 2. Storage Tests (`e2e_storage_ollama_test.go`)
Tests ModelRepository with different storage backends:
- **HostPath Storage**: Tests model download to node-local filesystem
- **PVC RWX Storage**: Tests with ReadWriteMany persistent volumes (if supported by storage class)
- **PVC RWO Storage**: Tests with ReadWriteOnce persistent volumes

### 3. Inference Tests (`e2e_inference_test.go`) ⭐ NEW
Full end-to-end tests including ModelRepository, LLMEngine deployment, and actual inference:

#### Ollama + HostPath Inference Test
- **Model**: `qwen2.5:0.5b` (~395MB, fast for CI)
- **Storage**: HostPath
- **Tests**:
  - ModelRepository deployment and download
  - LLMEngine (Ollama) deployment
  - Service creation and accessibility
  - Actual inference request to `/api/generate` endpoint
  - Response validation (checks for "hello" in response)

#### HuggingFace + HostPath Inference Test
- **Model**: `hf-internal-testing/tiny-random-gpt2` (~20MB)
- **Storage**: HostPath
- **Engine**: vLLM
- **Tests**:
  - ModelRepository deployment and download
  - LLMEngine (vLLM) deployment
  - Service creation and accessibility
  - Actual inference request to OpenAI-compatible API
  - Response validation (valid JSON response)
- **Note**: Skipped if no GPU detected (vLLM typically needs GPU)

## Running Tests

### Run All E2E Tests
```bash
make test-e2e
```

### Run Specific Test Categories

```bash
# Controller manager tests only
make test-e2e-manager

# All storage tests
make test-e2e-storage

# Only HostPath storage tests
make test-e2e-storage-hostpath

# Only PVC storage tests
make test-e2e-storage-pvc
```

### Run Inference Tests Specifically
```bash
# Run only inference tests
go test ./test/e2e/ -v -ginkgo.v -ginkgo.focus "Inference"

# Run only Ollama inference test
go test ./test/e2e/ -v -ginkgo.v -ginkgo.focus "Ollama.*Inference"

# Run only HuggingFace inference test
go test ./test/e2e/ -v -ginkgo.v -ginkgo.focus "HuggingFace.*Inference"
```

## Test Manifests

Test YAML manifests are located in `test/e2e/storage/`:

### Storage-Only Tests (No Inference)
- `e2e-02-ollama-hostpath.yaml` - Ollama ModelRepository with HostPath
- `e2e-03-ollama-pvc-rwx.yaml` - Ollama ModelRepository with RWX PVC
- `e2e-04-ollama-pvc-rwo.yaml` - Ollama ModelRepository with RWO PVC

### Full Inference Tests (ModelRepository + LLMEngine + Inference)
- `e2e-06-ollama-hostpath-inference.yaml` - Complete Ollama stack with inference testing
- `e2e-07-huggingface-hostpath-inference.yaml` - Complete HuggingFace/vLLM stack with inference testing

## Prerequisites

### For Local Testing
1. **Kubernetes Cluster**: Minikube, kind, or any K8s cluster
2. **kubectl**: Configured to access the cluster
3. **Deployed Operator**: Controller should be running in `aitrigram-system` namespace

### For Minikube
```bash
# Start minikube
minikube start --driver=docker

# Deploy the operator
make deploy

# Run tests
make test-e2e
```

### For GitHub Actions CI
Tests run automatically on:
- Push to any branch
- Pull requests
- Tag pushes

The CI workflow:
1. Sets up minikube with Docker driver
2. Deploys the operator
3. Runs all e2e tests including inference tests
4. Collects logs on failure

## Test Timeouts

- **Storage tests**: 30 minutes (model downloads can be slow)
- **Inference tests**: 40 minutes (includes deployment + inference)
- **Polling interval**: 30 seconds

## Inference Test Flow

### Ollama Inference Test
```
1. Apply ModelRepository (qwen2.5:0.5b with HostPath)
2. Wait for download job to complete
3. Verify ModelRepository status = "downloaded"
4. Verify BoundNodeName is set
5. Apply LLMEngine (Ollama)
6. Wait for deployment to be ready
7. Wait for service to be created
8. Create test client pod (curl)
9. Send POST to /api/generate endpoint
10. Parse JSON response
11. Verify response contains "hello"
12. Cleanup resources
```

### HuggingFace Inference Test
```
1. Check for GPU (skip if none)
2. Apply ModelRepository (tiny-random-gpt2 with HostPath)
3. Wait for download to complete
4. Apply LLMEngine (vLLM)
5. Wait for deployment to be ready
6. Create test client pod (curl)
7. Send POST to /v1/completions (OpenAI-compatible API)
8. Parse JSON response
9. Verify valid JSON returned
10. Cleanup resources
```

## Debugging Failed Tests

### View Logs
```bash
# Controller logs
kubectl logs -n aitrigram-system -l control-plane=controller-manager

# ModelRepository status
kubectl get modelrepository <name> -o yaml

# LLMEngine status
kubectl get llmengine <name> -n aitrigram-system -o yaml

# Pod logs
kubectl logs -n aitrigram-system <pod-name>
```

### Common Issues

1. **Download timeout**: Model download takes too long
   - Use smaller models for testing
   - Check network connectivity
   - Increase timeout values

2. **BoundNodeName not set**: HostPath storage issue
   - Check download job logs
   - Verify nodeSelector configuration
   - Check if job pod was scheduled

3. **Inference timeout**: LLM not responding
   - Check LLMEngine pod logs
   - Verify model loaded successfully
   - Check resource limits (CPU/memory)
   - For vLLM: verify GPU availability

4. **Service not accessible**: Networking issue
   - Check service exists: `kubectl get svc -n aitrigram-system`
   - Verify ClusterIP is assigned
   - Check pod is running and ready

## Environment Variables

- `E2E_DUMP_LOGS=true`: Dump controller logs on test failure (used in CI)

## CI/CD Integration

Tests are integrated into `.github/workflows/ci.yml`:

```yaml
- name: Running Test e2e
  env:
    E2E_DUMP_LOGS: "true"
  run: |
    go mod tidy
    make test-e2e
```

The workflow uses:
- Ubuntu latest runner
- Minikube with Docker driver
- Kubernetes stable version

## Future Improvements

- [ ] Add performance benchmarking for inference
- [ ] Test with larger models (optional, controlled by env var)
- [ ] Add multi-replica LLMEngine tests
- [ ] Test model updates and revisions
- [ ] Add tests for NFS storage with inference
- [ ] Add tests for GPU-enabled models
