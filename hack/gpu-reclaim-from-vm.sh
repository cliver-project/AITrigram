#!/usr/bin/env bash
# gpu-reclaim-from-vm.sh — Detach GPU from VM and restore host access
#
# Performs VM detach + vfio-pci unbind + nvidia rebind in one step.
# After this, the host has GPU access again.
#
# Usage:
#   sudo ./hack/gpu-reclaim-from-vm.sh <vm-domain-name>
#
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "ERROR: Must run as root" >&2
    exit 1
fi

VM_NAME="${1:-}"
if [[ -z "$VM_NAME" ]]; then
    echo "Usage: $0 <vm-domain-name>"
    echo ""
    echo "Available VMs:"
    virsh list --all --name 2>/dev/null | grep -v "^$" | sed 's/^/  /'
    exit 1
fi

if ! virsh dominfo "$VM_NAME" &>/dev/null; then
    echo "ERROR: VM '$VM_NAME' not found" >&2
    exit 1
fi

# --- Detect GPU ---
echo "=== Detecting NVIDIA GPU ==="
GPU_PCI=$(lspci -nn | grep -i "nvidia" | grep -i "3d\|vga" | head -1 | awk '{print $1}')
if [[ -z "$GPU_PCI" ]]; then
    echo "ERROR: No NVIDIA GPU found" >&2
    exit 1
fi

GPU_ADDR="0000:${GPU_PCI}"
GPU_BUS=$(echo "$GPU_PCI" | cut -d: -f1)
GPU_SLOT=$(echo "$GPU_PCI" | cut -d: -f2 | cut -d. -f1)
GPU_FUNC=$(echo "$GPU_PCI" | cut -d. -f2)
AUDIO_PCI="${GPU_PCI%.*}.1"

echo "GPU: $GPU_ADDR  $(lspci -s "$GPU_PCI" | cut -d: -f3-)"

# --- Detach from VM ---
if virsh dumpxml "$VM_NAME" 2>/dev/null | grep -q "bus='0x${GPU_BUS}'.*slot='0x${GPU_SLOT}'"; then
    echo ""
    echo "=== Detaching GPU from VM '$VM_NAME' ==="

    # Shut down VM if running
    VM_STATE=$(virsh domstate "$VM_NAME" 2>/dev/null || echo "unknown")
    if [[ "$VM_STATE" == "running" ]]; then
        echo "Shutting down VM..."
        virsh shutdown "$VM_NAME"
        for i in $(seq 1 30); do
            if virsh domstate "$VM_NAME" 2>/dev/null | grep -q "shut off"; then
                echo "VM shut down"
                break
            fi
            if [[ $i -eq 30 ]]; then
                echo "Forcing shutdown..."
                virsh destroy "$VM_NAME" 2>/dev/null || true
            fi
            sleep 5
        done
    fi

    # Detach GPU
    GPU_XML=$(mktemp /tmp/gpu-detach-XXXXXX.xml)
    cat > "$GPU_XML" <<EOF
<hostdev mode='subsystem' type='pci' managed='yes'>
  <source>
    <address domain='0x0000' bus='0x${GPU_BUS}' slot='0x${GPU_SLOT}' function='0x${GPU_FUNC}'/>
  </source>
</hostdev>
EOF
    virsh detach-device "$VM_NAME" "$GPU_XML" --config
    echo "GPU detached"
    rm -f "$GPU_XML"

    # Detach audio
    if lspci -s "$AUDIO_PCI" 2>/dev/null | grep -qi "audio"; then
        AUDIO_XML=$(mktemp /tmp/gpu-audio-detach-XXXXXX.xml)
        cat > "$AUDIO_XML" <<EOF
<hostdev mode='subsystem' type='pci' managed='yes'>
  <source>
    <address domain='0x0000' bus='0x${GPU_BUS}' slot='0x${GPU_SLOT}' function='0x1'/>
  </source>
</hostdev>
EOF
        virsh detach-device "$VM_NAME" "$AUDIO_XML" --config 2>/dev/null || true
        echo "Audio device detached"
        rm -f "$AUDIO_XML"
    fi

    # Start VM back without GPU
    echo "Starting VM without GPU..."
    virsh start "$VM_NAME" 2>/dev/null || true
else
    echo "GPU is not attached to VM '$VM_NAME'"
fi

# --- Unbind from vfio-pci ---
CURRENT_DRIVER=$(lspci -nnk -s "$GPU_PCI" | grep "Kernel driver" | awk '{print $NF}' || echo "none")
if [[ "$CURRENT_DRIVER" == "vfio-pci" ]]; then
    echo ""
    echo "=== Restoring host GPU access ==="

    # Unbind GPU from vfio-pci
    echo "$GPU_ADDR" > /sys/bus/pci/drivers/vfio-pci/unbind 2>/dev/null || true

    # Unbind audio from vfio-pci
    if lspci -s "$AUDIO_PCI" &>/dev/null; then
        echo "0000:${AUDIO_PCI}" > /sys/bus/pci/drivers/vfio-pci/unbind 2>/dev/null || true
    fi

    # Rebind to nvidia
    TARGET_DRIVER="nvidia"
    if ! modinfo nvidia &>/dev/null; then
        TARGET_DRIVER="nouveau"
    fi

    modprobe "$TARGET_DRIVER" 2>/dev/null || true
    echo 1 > /sys/bus/pci/rescan
    sleep 2

    NEW_DRIVER=$(lspci -nnk -s "$GPU_PCI" | grep "Kernel driver" | awk '{print $NF}' || echo "none")
    if [[ "$NEW_DRIVER" == "$TARGET_DRIVER" ]]; then
        echo "GPU rebound to $TARGET_DRIVER"
    else
        echo "WARNING: GPU driver is '$NEW_DRIVER', expected '$TARGET_DRIVER'"
        echo "Try: modprobe $TARGET_DRIVER && echo 1 > /sys/bus/pci/rescan"
    fi
else
    echo "GPU is not on vfio-pci (driver: $CURRENT_DRIVER), skipping unbind"
fi

echo ""
echo "=== Done ==="
echo "GPU is available to the host. Verify: nvidia-smi"
