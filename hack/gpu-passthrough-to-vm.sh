#!/usr/bin/env bash
# gpu-passthrough-to-vm.sh — Bind GPU to vfio-pci and attach to a VM
#
# Performs runtime vfio-pci bind + libvirt attach in one step.
# After this, the host loses GPU access and the VM gets it.
#
# Prerequisites:
#   - IOMMU enabled (run gpu-iommu-enable.sh first)
#   - VM defined in libvirt
#
# Usage:
#   sudo ./hack/gpu-passthrough-to-vm.sh <vm-domain-name>
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

# Verify VM exists
if ! virsh dominfo "$VM_NAME" &>/dev/null; then
    echo "ERROR: VM '$VM_NAME' not found" >&2
    exit 1
fi

# Verify IOMMU
if ! grep -q "intel_iommu=on" /proc/cmdline; then
    echo "ERROR: IOMMU not enabled. Run ./hack/gpu-iommu-enable.sh first" >&2
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
GPU_IDS=$(lspci -nn -s "$GPU_PCI" | grep -oP '\[\K[0-9a-f]{4}:[0-9a-f]{4}' | head -1)
GPU_VENDOR=$(echo "$GPU_IDS" | cut -d: -f1)
GPU_DEVICE=$(echo "$GPU_IDS" | cut -d: -f2)
GPU_BUS=$(echo "$GPU_PCI" | cut -d: -f1)
GPU_SLOT=$(echo "$GPU_PCI" | cut -d: -f2 | cut -d. -f1)
GPU_FUNC=$(echo "$GPU_PCI" | cut -d. -f2)

AUDIO_PCI="${GPU_PCI%.*}.1"
HAS_AUDIO=false
if lspci -s "$AUDIO_PCI" 2>/dev/null | grep -qi "audio"; then
    HAS_AUDIO=true
fi

echo "GPU: $GPU_ADDR [$GPU_IDS]  $(lspci -s "$GPU_PCI" | cut -d: -f3-)"
if [[ "$HAS_AUDIO" == true ]]; then
    echo "Audio: 0000:${AUDIO_PCI}  $(lspci -s "$AUDIO_PCI" | cut -d: -f3-)"
fi

CURRENT_DRIVER=$(lspci -nnk -s "$GPU_PCI" | grep "Kernel driver" | awk '{print $NF}' || echo "none")
echo "Current driver: $CURRENT_DRIVER"

# Check if already attached to another VM
for vm in $(virsh list --all --name 2>/dev/null | grep -v "^$"); do
    if virsh dumpxml "$vm" 2>/dev/null | grep -q "bus='0x${GPU_BUS}'.*slot='0x${GPU_SLOT}'"; then
        if [[ "$vm" == "$VM_NAME" ]]; then
            echo "GPU is already attached to VM '$VM_NAME'. Nothing to do."
            exit 0
        else
            echo "ERROR: GPU is attached to VM '$vm'. Detach first:" >&2
            echo "  sudo ./hack/gpu-reclaim-from-vm.sh $vm" >&2
            exit 1
        fi
    fi
done

# --- Bind to vfio-pci ---
if [[ "$CURRENT_DRIVER" != "vfio-pci" ]]; then
    echo ""
    echo "=== Binding GPU to vfio-pci ==="
    modprobe vfio-pci

    # Unbind from current driver
    if [[ "$CURRENT_DRIVER" != "none" ]]; then
        echo "$GPU_ADDR" > "/sys/bus/pci/drivers/$CURRENT_DRIVER/unbind" 2>/dev/null || true
    fi
    if [[ "$HAS_AUDIO" == true ]]; then
        AUDIO_DRIVER=$(lspci -nnk -s "$AUDIO_PCI" | grep "Kernel driver" | awk '{print $NF}' || echo "none")
        if [[ "$AUDIO_DRIVER" != "none" ]]; then
            echo "0000:${AUDIO_PCI}" > "/sys/bus/pci/drivers/$AUDIO_DRIVER/unbind" 2>/dev/null || true
        fi
    fi

    # Bind GPU to vfio-pci
    echo "$GPU_VENDOR $GPU_DEVICE" > /sys/bus/pci/drivers/vfio-pci/new_id 2>/dev/null || true
    echo "$GPU_ADDR" > /sys/bus/pci/drivers/vfio-pci/bind 2>/dev/null || true

    # Bind audio to vfio-pci
    if [[ "$HAS_AUDIO" == true ]]; then
        AUDIO_IDS=$(lspci -nn -s "$AUDIO_PCI" | grep -oP '\[\K[0-9a-f]{4}:[0-9a-f]{4}' | head -1)
        AUDIO_VENDOR=$(echo "$AUDIO_IDS" | cut -d: -f1)
        AUDIO_DEVICE=$(echo "$AUDIO_IDS" | cut -d: -f2)
        echo "$AUDIO_VENDOR $AUDIO_DEVICE" > /sys/bus/pci/drivers/vfio-pci/new_id 2>/dev/null || true
        echo "0000:${AUDIO_PCI}" > /sys/bus/pci/drivers/vfio-pci/bind 2>/dev/null || true
    fi

    # Verify
    NEW_DRIVER=$(lspci -nnk -s "$GPU_PCI" | grep "Kernel driver" | awk '{print $NF}' || echo "unknown")
    if [[ "$NEW_DRIVER" != "vfio-pci" ]]; then
        echo "ERROR: Failed to bind to vfio-pci (driver: $NEW_DRIVER)" >&2
        exit 1
    fi
    echo "GPU bound to vfio-pci"
fi

# --- Attach to VM ---
echo ""
echo "=== Attaching GPU to VM '$VM_NAME' ==="

GPU_XML=$(mktemp /tmp/gpu-passthrough-XXXXXX.xml)
cat > "$GPU_XML" <<EOF
<hostdev mode='subsystem' type='pci' managed='yes'>
  <source>
    <address domain='0x0000' bus='0x${GPU_BUS}' slot='0x${GPU_SLOT}' function='0x${GPU_FUNC}'/>
  </source>
</hostdev>
EOF
virsh attach-device "$VM_NAME" "$GPU_XML" --config
echo "GPU attached"
rm -f "$GPU_XML"

if [[ "$HAS_AUDIO" == true ]]; then
    AUDIO_XML=$(mktemp /tmp/gpu-audio-passthrough-XXXXXX.xml)
    cat > "$AUDIO_XML" <<EOF
<hostdev mode='subsystem' type='pci' managed='yes'>
  <source>
    <address domain='0x0000' bus='0x${GPU_BUS}' slot='0x${GPU_SLOT}' function='0x1'/>
  </source>
</hostdev>
EOF
    virsh attach-device "$VM_NAME" "$AUDIO_XML" --config
    echo "Audio device attached"
    rm -f "$AUDIO_XML"
fi

# Restart VM if running
VM_STATE=$(virsh domstate "$VM_NAME" 2>/dev/null || echo "unknown")
if [[ "$VM_STATE" == "running" ]]; then
    echo ""
    echo "Restarting VM to apply GPU passthrough..."
    virsh reboot "$VM_NAME"
    echo "Waiting for VM..."
    for i in $(seq 1 30); do
        sleep 5
        if virsh domstate "$VM_NAME" 2>/dev/null | grep -q "running"; then
            echo "VM is running"
            break
        fi
    done
elif [[ "$VM_STATE" == "shut off" ]]; then
    echo ""
    echo "Start VM to use the GPU: virsh start $VM_NAME"
fi

echo ""
echo "=== Done ==="
echo "Verify: ssh <vm> lspci | grep -i nvidia"
