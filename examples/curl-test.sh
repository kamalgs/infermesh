#!/usr/bin/env bash
# M0 tracer bullet — test the HTTP proxy end-to-end
# Prerequisites: NATS running, gateway running, proxy running
set -euo pipefail

PROXY="${PROXY_URL:-http://localhost:8080}"

echo "=== Health check ==="
curl -s "${PROXY}/health" | jq .
echo

echo "=== Chat completion (via HTTP proxy → NATS → gateway → OpenAI) ==="
curl -s "${PROXY}/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Say hello in one sentence."}]
  }' | jq .
