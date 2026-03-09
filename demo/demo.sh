#!/usr/bin/env bash
set -euo pipefail

NATS_URL="${NATS_URL:-nats://localhost:14225}"
MODEL="${MODEL:-ollama.qwen2.5:0.5b}"

echo "=== InferMesh End-to-End Demo (SDK → NATS → Ollama) ==="
echo "NATS:   $NATS_URL"
echo "Model:  $MODEL"
echo ""

# Wait for NATS leaf to be reachable (check TCP port)
NATS_PORT="${NATS_URL##*:}"
echo "Waiting for NATS on port $NATS_PORT..."
for i in $(seq 1 30); do
  if nc -z localhost "$NATS_PORT" 2>/dev/null; then
    echo "NATS is ready!"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "ERROR: NATS not reachable after 30 seconds"
    exit 1
  fi
  sleep 1
done

echo ""
echo "--- Sending chat completion via InferMeshClient SDK ---"
echo ""

# Run a one-shot SDK request
NATS_URL="$NATS_URL" MODEL="$MODEL" npx tsx demo/demo-request.ts

echo ""
echo "=== Demo complete! SDK → NATS → Ollama provider → real LLM. ==="
