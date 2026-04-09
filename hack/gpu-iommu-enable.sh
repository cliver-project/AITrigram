#!/usr/bin/env bash
# gpu-iommu-enable.sh — Ensure IOMMU is enabled (one-time setup, requires reboot)
#
# Checks if IOMMU is active. If not, adds intel_iommu=on to GRUB and prompts
# for reboot. Safe to run multiple times — skips if already enabled.
#
# This does NOT affect GPU access. The GPU remains usable on the host.
#
# Usage:
#   sudo ./hack/gpu-iommu-enable.sh
#
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "ERROR: Must run as root" >&2
    exit 1
fi

echo "=== Checking IOMMU status ==="

# Check if IOMMU is active in kernel
if grep -q "intel_iommu=on" /proc/cmdline; then
    echo "IOMMU is enabled (intel_iommu=on in kernel cmdline)"
    if dmesg 2>/dev/null | grep -qi "DMAR:.*IOMMU\|IOMMU enabled"; then
        echo "IOMMU is active in hardware"
    fi
    echo "No action needed."
    exit 0
fi

echo "IOMMU is NOT enabled"

# Also blacklist nouveau while we're at it
BLACKLIST_FILE="/etc/modprobe.d/blacklist-nouveau.conf"
if [[ ! -f "$BLACKLIST_FILE" ]]; then
    cat > "$BLACKLIST_FILE" <<EOF
blacklist nouveau
options nouveau modeset=0
EOF
    echo "Blacklisted nouveau driver"
fi

# Update GRUB
GRUB_FILE="/etc/default/grub"
if [[ ! -f "$GRUB_FILE" ]]; then
    echo "ERROR: Cannot find $GRUB_FILE. Enable IOMMU manually." >&2
    exit 1
fi

if grep -q "intel_iommu=on" "$GRUB_FILE"; then
    echo "intel_iommu=on already in GRUB config but not active — reboot required"
else
    cp "$GRUB_FILE" "${GRUB_FILE}.bak.$(date +%s)"
    sed -i 's/\(GRUB_CMDLINE_LINUX="[^"]*\)/\1 intel_iommu=on iommu=pt/' "$GRUB_FILE"
    echo "Added intel_iommu=on iommu=pt to GRUB"

    echo "Regenerating GRUB config..."
    if command -v grub2-mkconfig &>/dev/null; then
        grub2-mkconfig -o /boot/grub2/grub.cfg
    elif command -v update-grub &>/dev/null; then
        update-grub
    fi
fi

echo ""
echo "=== Reboot required ==="
echo "Run: reboot"
echo "After reboot, re-run this script to verify IOMMU is active."
