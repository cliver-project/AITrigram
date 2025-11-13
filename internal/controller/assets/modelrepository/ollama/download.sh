#!/bin/bash
set -euo pipefail

# Environment variables:
#   - OLLAMA_HOME: Directory to store models (set by operator)
#   - OLLAMA_MODELS: Directory to store models (set by operator)
#   - MODEL_NAME: Ollama model name (e.g., "llama2:7b")

echo "========================================"
echo "Ollama Model Download Script"
echo "========================================"

if [ -z "${MODEL_NAME:-}" ]; then
    echo "ERROR: MODEL_NAME environment variable is required" >&2
    exit 1
fi

OLLAMA_HOME=${OLLAMA_HOME:-/data/models}
export OLLAMA_MODELS="$OLLAMA_HOME"

echo "Model Name: $MODEL_NAME"
echo "Target directory: $OLLAMA_HOME"
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
        echo "ERROR: Ollama service failed to start" >&2
        kill $OLLAMA_PID || true
        exit 1
    fi
    echo "Waiting... ($i/30)"
    sleep 5
done

# Pull the model
echo "Downloading model: $MODEL_NAME"
if ollama pull "$MODEL_NAME"; then
    echo "Successfully downloaded $MODEL_NAME"
    EXIT_CODE=0
else
    echo "ERROR: Failed to download $MODEL_NAME" >&2
    EXIT_CODE=1
fi

# Stop ollama service
echo "Stopping Ollama service..."
kill $OLLAMA_PID || true
wait $OLLAMA_PID 2>/dev/null || true

echo "========================================"
exit $EXIT_CODE
