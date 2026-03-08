# nats-llm-gateway

A **NATS-native** LLM gateway. Clients connect directly to NATS (TCP or WebSocket) — no HTTP layer in the gateway. A JavaScript/TypeScript SDK provides an OpenAI-compatible interface over NATS.

```
Client (JS SDK) ──► NATS (TCP/WS) ──► Gateway Service ──► Provider Adapters ──► OpenAI / Anthropic / Ollama / ...
```

## Features

- **NATS-native** — no HTTP in the hot path; clients speak NATS protocol directly
- **OpenAI-compatible JS SDK** — drop-in replacement for the `openai` npm package
- **Multi-runtime** — works in Node.js, Deno, Bun (TCP) and browsers (WebSocket)
- **Multi-provider routing** — route to OpenAI, Anthropic, Ollama, and more
- **Streaming** — `async iterable` streaming over NATS, chunks flow direct from adapter to client
- **Rate limiting** — per-key, per-model, and global limits backed by NATS KV
- **Authentication** — NATS native auth (NKeys/JWTs) + gateway API key validation

## Quick Start

```bash
# Prerequisites: Node.js 18+, NATS server on localhost:4222

git clone https://github.com/kamalgs/nats-llm-gateway.git
cd nats-llm-gateway

# Start the gateway service
cp configs/gateway.yaml.example configs/gateway.yaml
# Edit configs/gateway.yaml with your provider API keys
go run ./cmd/gateway --config configs/gateway.yaml
```

Use the SDK in your application:

```typescript
import { NATSLLMClient } from 'nats-llm-client';

const client = new NATSLLMClient({
  natsUrl: 'nats://localhost:4222',
  apiKey: 'sk-my-key',
});

// Non-streaming — same interface as OpenAI SDK
const response = await client.chat.completions.create({
  model: 'gpt-4o',
  messages: [{ role: 'user', content: 'Hello!' }],
});
console.log(response.choices[0].message.content);

// Streaming — async iterable
const stream = await client.chat.completions.create({
  model: 'claude-sonnet',
  messages: [{ role: 'user', content: 'Write a poem' }],
  stream: true,
});
for await (const chunk of stream) {
  process.stdout.write(chunk.choices[0]?.delta?.content || '');
}

await client.close();
```

**Migrating from OpenAI SDK?** Just change the import and constructor — everything else stays the same.

## Documentation

- [Design & Requirements](docs/DESIGN.md) — architecture, SDK design, NATS subject layout, and milestones

## License

MIT
