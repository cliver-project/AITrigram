#!/bin/bash
set -euo pipefail

# Environment variables:
#   - OLLAMA_HOME: Directory where models are stored (set by operator)
#   - OLLAMA_MODELS: Directory where models are stored (set by operator)
#   - MODEL_NAME: Ollama model name (e.g., "llama2:7b")

echo "========================================"
echo "Ollama Model Cleanup Script"
echo "========================================"

if [ -z "${MODEL_NAME:-}" ]; then
    echo "ERROR: MODEL_NAME environment variable is required" >&2
    exit 1
fi

OLLAMA_HOME=${OLLAMA_HOME:-/data/models}
export OLLAMA_MODELS="$OLLAMA_HOME"

echo "Model Name: $MODEL_NAME"
echo "Model directory: $OLLAMA_HOME"
echo "----------------------------------------"

# Start ollama serve in background
echo "Starting Ollama service..."
ollama serve &
OLLAMA_PID=$!

# Wait for ollama to be ready
echo "Waiting for Ollama service to be ready..."
for i in {1..30}; do
    if curl -s http://localhost:11434/api/tags > /dev/null 2>&1; then
        echo "Ollama service is ready"
        break
    fi
    if [ $i -eq 30 ]; then
        echo "WARNING: Ollama service failed to start, will try to delete directly" >&2
        # Don't fail cleanup - try to clean up manually
        break
    fi
    echo "Waiting... ($i/30)"
    sleep 2
done

# Try to remove the model using ollama
echo "Removing model: $MODEL_NAME"
if ollama rm "$MODEL_NAME" 2>/dev/null; then
    echo "Successfully removed $MODEL_NAME"
elif [ -d "$OLLAMA_HOME/manifests" ]; then
    # If ollama rm fails, try to clean up manually
    echo "WARNING: ollama rm failed, attempting manual cleanup"

    # Remove model manifests
    find "$OLLAMA_HOME/manifests" -type f -name "*${MODEL_NAME}*" -delete 2>/dev/null || true

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
