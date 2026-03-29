#!/bin/bash
set -euo pipefail

# Environment variables:
#   - OLLAMA_HOME: Directory to store models (set by operator)
#   - OLLAMA_MODELS: Directory to store models (set by operator)
#   - MODEL_ID: Ollama model name (e.g., "llama3.2")
#   - REVISION_ID: Optional revision/tag (e.g., "3b", "latest")
#                  If provided, will be used to compose final model like "llama3.2:3b"

echo "========================================"
echo "Ollama Model Download Script"
echo "========================================"

if [ -z "${MODEL_ID:-}" ]; then
    echo "ERROR: MODEL_ID environment variable is required" >&2
    exit 1
fi

OLLAMA_HOME=${OLLAMA_HOME:-/data/models}
export OLLAMA_MODELS="$OLLAMA_HOME"

# Compose full model identifier with revision tag if provided
if [ -n "${REVISION_ID:-}" ]; then
    FULL_MODEL_ID="${MODEL_ID}:${REVISION_ID}"
else
    FULL_MODEL_ID="${MODEL_ID}"
fi

echo "Base Model ID: $MODEL_ID"
echo "Revision: ${REVISION_ID:-<default>}"
echo "Full Model ID: $FULL_MODEL_ID"
echo "Target directory: $OLLAMA_HOME"
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
        break
    fi
    if [ $i -eq 30 ]; then
        echo "ERROR: Ollama service failed to start" >&2
        echo "--- Ollama logs ---"
        cat "$OLLAMA_LOG"
        rm -f "$OLLAMA_LOG"
        kill $OLLAMA_PID || true
        exit 1
    fi
    echo "Waiting... ($i/30)"
    sleep 2
done
rm -f "$OLLAMA_LOG"

# Pull the model
echo "Downloading model: $FULL_MODEL_ID"
if ollama pull "$FULL_MODEL_ID"; then
    echo "Successfully downloaded $FULL_MODEL_ID"
    EXIT_CODE=0
else
    echo "ERROR: Failed to download $FULL_MODEL_ID" >&2
    EXIT_CODE=1
fi

# Stop ollama service
echo "Stopping Ollama service..."
kill $OLLAMA_PID || true
wait $OLLAMA_PID 2>/dev/null || true

echo "========================================"
exit $EXIT_CODE
