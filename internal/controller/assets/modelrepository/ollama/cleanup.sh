#!/bin/bash
set -euo pipefail

# Environment variables:
#   - OLLAMA_HOME: Directory where models are stored (set by operator)
#   - OLLAMA_MODELS: Directory where models are stored (set by operator)
#   - MODEL_ID: Ollama model name (e.g., "llama2:7b")

echo "========================================"
echo "Ollama Model Cleanup Script"
echo "========================================"

if [ -z "${MODEL_ID:-}" ]; then
    echo "ERROR: MODEL_ID environment variable is required" >&2
    exit 1
fi

OLLAMA_HOME=${OLLAMA_HOME:-/data/models}
export OLLAMA_MODELS="$OLLAMA_HOME"

echo "Model ID: $MODEL_ID"
echo "Model directory: $OLLAMA_HOME"
echo "----------------------------------------"

# Start ollama serve in background and capture logs
echo "Starting Ollama service..."
OLLAMA_LOG=$(mktemp)
ollama serve > "$OLLAMA_LOG" 2>&1 &
OLLAMA_PID=$!

# Wait for ollama to be ready by checking logs for "Listening on"
echo "Waiting for Ollama service to be ready..."
for i in {1..30}; do
    if grep -q "Listening on" "$OLLAMA_LOG" 2>/dev/null; then
        echo "Ollama service is ready"
        rm -f "$OLLAMA_LOG"
        break
    fi
    if [ $i -eq 30 ]; then
        echo "WARNING: Ollama service failed to start, will try to delete directly" >&2
        echo "--- Ollama logs ---"
        cat "$OLLAMA_LOG"
        rm -f "$OLLAMA_LOG"
        # Don't fail cleanup - try to clean up manually
        break
    fi
    echo "Waiting... ($i/30)"
    sleep 2
done

# Try to remove the model using ollama
echo "Removing model: $MODEL_ID"
if ollama rm "$MODEL_ID" 2>/dev/null; then
    echo "Successfully removed $MODEL_ID"
elif [ -d "$OLLAMA_HOME/manifests" ]; then
    # If ollama rm fails, try to clean up manually
    echo "WARNING: ollama rm failed, attempting manual cleanup"

    # Remove model manifests
    find "$OLLAMA_HOME/manifests" -type f -name "*${MODEL_ID}*" -delete 2>/dev/null || true

    # Note: We don't remove blobs as they might be shared by other models
    echo "Manual cleanup completed (kept shared blobs)"
else
    echo "Model directory not found, nothing to cleanup"
fi

# Stop ollama service
echo "Stopping Ollama service..."
kill $OLLAMA_PID 2>/dev/null || true
wait $OLLAMA_PID 2>/dev/null || true

echo "========================================"
echo "Cleanup completed"

# Always exit successfully to allow deletion to proceed
exit 0
