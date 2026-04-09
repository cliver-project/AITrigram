#!/usr/bin/env bash
# setup-gpu.sh — Prepare a Kubernetes cluster for GPU workloads
#
# This script installs the NVIDIA GPU Operator which handles:
#   - NVIDIA driver installation (optional, can use pre-installed)
#   - NVIDIA Container Toolkit
#   - NVIDIA Device Plugin (exposes nvidia.com/gpu resources)
#   - GPU Feature Discovery (labels nodes with GPU info)
#   - DCGM Exporter (GPU metrics for Prometheus)
#
# Prerequisites:
#   - kubectl access to the cluster
#   - Helm 3 installed
#   - GPU nodes with NVIDIA GPUs (bare metal or VM with GPU passthrough)
#
# Usage:
#   ./hack/setup-gpu.sh                    # Install with auto driver management
#   ./hack/setup-gpu.sh --pre-installed    # Skip driver install (driver already on nodes)
#   ./hack/setup-gpu.sh --uninstall        # Remove GPU operator
#
# GPU Passthrough (VM environments):
#   Before running this script, GPU must be passed through to VMs.
#   See comments at the bottom of this file for passthrough instructions.
#
set -euo pipefail

NAMESPACE="gpu-operator"
HELM_RELEASE="gpu-operator"

usage() {
    echo "Usage: $0 [--pre-installed|--uninstall|--status]"
    echo ""
    echo "Options:"
    echo "  --pre-installed   Skip NVIDIA driver install (use pre-installed drivers)"
    echo "  --uninstall       Remove the GPU operator"
    echo "  --status          Show GPU node status and labels"
    echo ""
    exit 1
}

install_gpu_operator() {
    local driver_enabled="${1:-true}"

    echo "=== Installing NVIDIA GPU Operator ==="
    echo "Driver management: $([ "$driver_enabled" = "true" ] && echo "operator-managed" || echo "pre-installed")"

    # Add NVIDIA Helm repo
    helm repo add nvidia https://helm.ngc.nvidia.com/nvidia 2>/dev/null || true
    helm repo update nvidia

    # Install GPU Operator
    helm upgrade --install "$HELM_RELEASE" nvidia/gpu-operator \
        --namespace "$NAMESPACE" --create-namespace \
        --set driver.enabled="$driver_enabled" \
        --set toolkit.enabled=true \
        --set devicePlugin.enabled=true \
        --set gfd.enabled=true \
        --set dcgmExporter.enabled=true \
        --wait --timeout 10m

    echo ""
    echo "=== GPU Operator installed ==="
    echo "Waiting for device plugin pods to be ready..."
    kubectl rollout status daemonset/nvidia-device-plugin-daemonset \
        -n "$NAMESPACE" --timeout=5m 2>/dev/null || \
        echo "Note: Device plugin may take a few minutes to initialize on all nodes"
}

uninstall_gpu_operator() {
    echo "=== Uninstalling NVIDIA GPU Operator ==="
    helm uninstall "$HELM_RELEASE" -n "$NAMESPACE" 2>/dev/null || echo "Release not found"
    kubectl delete namespace "$NAMESPACE" --ignore-not-found
    echo "GPU Operator removed"
}

show_status() {
    echo "=== GPU Node Status ==="
    echo ""
    echo "Nodes with GPU resources:"
    kubectl get nodes -o custom-columns=\
'NAME:.metadata.name,GPU_PRESENT:.metadata.labels.nvidia\.com/gpu\.present,GPU_PRODUCT:.metadata.labels.nvidia\.com/gpu\.product,GPU_MEMORY:.metadata.labels.nvidia\.com/gpu\.memory,GPU_COUNT:.status.allocatable.nvidia\.com/gpu' \
        2>/dev/null || echo "No GPU labels found"

    echo ""
    echo "GPU Operator pods:"
    kubectl get pods -n "$NAMESPACE" -o wide 2>/dev/null || echo "GPU operator namespace not found"

    echo ""
    echo "Allocatable GPU resources across cluster:"
    kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatable.nvidia\.com/gpu}{"\n"}{end}' 2>/dev/null
}

# Parse arguments
case "${1:-}" in
    --pre-installed)
        install_gpu_operator "false"
        ;;
    --uninstall)
        uninstall_gpu_operator
        ;;
    --status)
        show_status
        ;;
    --help|-h)
        usage
        ;;
    "")
        install_gpu_operator "true"
        ;;
    *)
        echo "Unknown option: $1"
        usage
        ;;
esac

# =============================================================================
# GPU PASSTHROUGH GUIDE (Physical Host → VM)
# =============================================================================
#
# If your GPUs are on a physical host but your K8s nodes run in VMs,
# you need PCI passthrough before running this script.
#
# --- Step 1: Enable IOMMU on the host ---
#
#   For Intel CPUs, add to host's GRUB (/etc/default/grub):
#     GRUB_CMDLINE_LINUX="intel_iommu=on iommu=pt"
#
#   For AMD CPUs:
#     GRUB_CMDLINE_LINUX="amd_iommu=on iommu=pt"
#
#   Then: grub2-mkconfig -o /boot/grub2/grub.cfg && reboot
#
#   Verify: dmesg | grep -i iommu
#
# --- Step 2: Identify GPU PCI address ---
#
#   lspci -nn | grep -i nvidia
#   # Example output:
#   # 41:00.0 3D controller [0302]: NVIDIA Corporation A100 [10de:20b2] (rev a1)
#   # 41:00.1 Audio device [0403]: NVIDIA Corporation [10de:1aef] (rev a1)
#   #
#   # Note: Pass through BOTH the GPU and its audio device (same IOMMU group)
#
# --- Step 3: Bind GPU to vfio-pci driver on host ---
#
#   # Load vfio modules
#   modprobe vfio-pci
#
#   # Create /etc/modprobe.d/vfio.conf:
#   options vfio-pci ids=10de:20b2,10de:1aef
#
#   # Blacklist nouveau driver:
#   echo "blacklist nouveau" > /etc/modprobe.d/blacklist-nouveau.conf
#   dracut --force && reboot
#
#   # Verify GPU is bound to vfio-pci:
#   lspci -nnk -s 41:00.0 | grep "Kernel driver"
#   # Should show: Kernel driver in use: vfio-pci
#
# --- Step 4: Add GPU to VM (libvirt/KVM) ---
#
#   # Option A: virsh (runtime)
#   virsh nodedev-list | grep pci | grep 41_00
#   virsh attach-device <vm-name> gpu-passthrough.xml --config
#
#   # gpu-passthrough.xml:
#   # <hostdev mode='subsystem' type='pci' managed='yes'>
#   #   <source>
#   #     <address domain='0x0000' bus='0x41' slot='0x00' function='0x0'/>
#   #   </source>
#   # </hostdev>
#
#   # Option B: VM XML definition
#   virsh edit <vm-name>
#   # Add <hostdev> block under <devices>
#
#   # Reboot VM after attaching
#   virsh reboot <vm-name>
#
# --- Step 5: Verify GPU inside VM ---
#
#   # SSH into the VM and check:
#   lspci | grep -i nvidia
#   # Should show the GPU device
#
#   # Install NVIDIA driver inside VM (if not using GPU Operator driver management):
#   # dnf install -y nvidia-driver nvidia-driver-cuda
#   # or let GPU Operator handle it (default mode of this script)
#
# --- Step 6: Run this script ---
#
#   # If GPU Operator manages drivers (recommended):
#   ./hack/setup-gpu.sh
#
#   # If you pre-installed drivers in the VM:
#   ./hack/setup-gpu.sh --pre-installed
#
# =============================================================================
