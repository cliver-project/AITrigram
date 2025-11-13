#!/bin/bash
# Integration Test Runner for Storage Providers
# This script applies test YAMLs and validates the results
# DO NOT RUN without proper cluster setup

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TEST_NS="aitrigram-system"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

wait_for_model_ready() {
    local model_name=$1
    local timeout=${2:-300}  # 5 minutes default
    local elapsed=0

    log_info "Waiting for ModelRepository '$model_name' to be ready..."

    while [ $elapsed -lt $timeout ]; do
        phase=$(kubectl get modelrepository "$model_name" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")

        if [ "$phase" == "Downloaded" ] || [ "$phase" == "Ready" ]; then
            log_info "ModelRepository '$model_name' is ready (phase: $phase)"
            return 0
        elif [ "$phase" == "Failed" ]; then
            log_error "ModelRepository '$model_name' failed"
            kubectl get modelrepository "$model_name" -o yaml
            return 1
        fi

        sleep 5
        elapsed=$((elapsed + 5))
    done

    log_error "Timeout waiting for ModelRepository '$model_name'"
    return 1
}

check_pvc_exists() {
    local pvc_name=$1
    local namespace=$2

    if kubectl get pvc "$pvc_name" -n "$namespace" &>/dev/null; then
        log_info "PVC '$pvc_name' exists in namespace '$namespace'"
        return 0
    else
        log_warn "PVC '$pvc_name' does not exist in namespace '$namespace'"
        return 1
    fi
}

check_node_affinity() {
    local model_name=$1
    local expected_node=$2

    bound_node=$(kubectl get modelrepository "$model_name" -o jsonpath='{.status.boundNodeName}' 2>/dev/null || echo "")

    if [ -n "$expected_node" ]; then
        if [ "$bound_node" == "$expected_node" ]; then
            log_info "ModelRepository '$model_name' correctly bound to node '$bound_node'"
            return 0
        else
            log_error "ModelRepository '$model_name' bound to '$bound_node', expected '$expected_node'"
            return 1
        fi
    elif [ -n "$bound_node" ]; then
        log_info "ModelRepository '$model_name' bound to node '$bound_node'"
        return 0
    else
        log_info "ModelRepository '$model_name' not bound to specific node (shared storage)"
        return 0
    fi
}

cleanup_model() {
    local model_name=$1
    log_info "Cleaning up ModelRepository '$model_name'..."
    kubectl delete modelrepository "$model_name" --ignore-not-found=true
    # Wait for deletion
    kubectl wait --for=delete modelrepository/"$model_name" --timeout=60s 2>/dev/null || true
}

test_nfs_storage() {
    log_info "===== Testing NFS Storage ====="

    kubectl apply -f "$SCRIPT_DIR/01-nfs-storage.yaml"
    wait_for_model_ready "test-nfs-model" || return 1
    check_node_affinity "test-nfs-model" "" || return 1

    log_info "NFS storage test PASSED"
    cleanup_model "test-nfs-model"
}

test_hostpath_storage() {
    log_info "===== Testing HostPath Storage ====="

    kubectl apply -f "$SCRIPT_DIR/02-hostpath-storage.yaml"

    # Test default HostPath
    wait_for_model_ready "test-hostpath-model" || return 1
    check_node_affinity "test-hostpath-model" "" || return 1

    # Test node-specific HostPath
    wait_for_model_ready "test-hostpath-node-specific" || return 1
    check_node_affinity "test-hostpath-node-specific" "kind-worker" || return 1

    log_info "HostPath storage test PASSED"
    cleanup_model "test-hostpath-model"
    cleanup_model "test-hostpath-node-specific"
}

test_pvc_rwx_storage() {
    log_info "===== Testing PVC RWX Storage ====="

    kubectl apply -f "$SCRIPT_DIR/03-pvc-rwx-storage.yaml"
    wait_for_model_ready "test-pvc-rwx-model" || return 1
    check_pvc_exists "test-pvc-rwx-model-storage" "$TEST_NS" || return 1
    check_node_affinity "test-pvc-rwx-model" "" || return 1

    log_info "PVC RWX storage test PASSED"
    cleanup_model "test-pvc-rwx-model"
}

test_pvc_rwo_storage() {
    log_info "===== Testing PVC RWO Storage ====="

    kubectl apply -f "$SCRIPT_DIR/04-pvc-rwo-storage.yaml"
    wait_for_model_ready "test-pvc-rwo-model" || return 1
    check_pvc_exists "test-pvc-rwo-model-storage" "$TEST_NS" || return 1

    # RWO should bind to a specific node
    bound_node=$(kubectl get modelrepository "test-pvc-rwo-model" -o jsonpath='{.status.boundNodeName}')
    if [ -z "$bound_node" ]; then
        log_error "RWO PVC should be bound to a specific node"
        return 1
    fi
    log_info "PVC RWO correctly bound to node: $bound_node"

    log_info "PVC RWO storage test PASSED"
    cleanup_model "test-pvc-rwo-model"
}

test_existing_pvc() {
    log_info "===== Testing Existing PVC ====="

    # Create the PVC first
    kubectl apply -f "$SCRIPT_DIR/05-pvc-existing.yaml"

    # Wait for PVC to be bound
    kubectl wait --for=jsonpath='{.status.phase}'=Bound pvc/my-existing-model-pvc -n "$TEST_NS" --timeout=60s || {
        log_warn "PVC may not be bound yet, continuing anyway"
    }

    # Apply ModelRepository
    wait_for_model_ready "test-existing-pvc-model" || return 1

    log_info "Existing PVC storage test PASSED"
    cleanup_model "test-existing-pvc-model"
    kubectl delete pvc my-existing-model-pvc -n "$TEST_NS" --ignore-not-found=true
}

main() {
    log_info "Starting storage integration tests"
    log_warn "Ensure operator is running and cluster has required storage classes"

    local failed=0

    # Run tests
    test_nfs_storage || failed=$((failed + 1))
    test_hostpath_storage || failed=$((failed + 1))
    test_pvc_rwx_storage || failed=$((failed + 1))
    test_pvc_rwo_storage || failed=$((failed + 1))
    test_existing_pvc || failed=$((failed + 1))

    # Summary
    echo ""
    if [ $failed -eq 0 ]; then
        log_info "===== ALL TESTS PASSED ====="
        exit 0
    else
        log_error "===== $failed TESTS FAILED ====="
        exit 1
    fi
}

# Only run main if script is executed directly
if [ "${BASH_SOURCE[0]}" == "${0}" ]; then
    main "$@"
fi
