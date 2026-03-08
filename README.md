# nats-llm-gateway

A **NATS-native** LLM gateway. Clients connect directly to NATS (TCP or WebSocket) — no HTTP layer in the gateway. A Go SDK provides an OpenAI-compatible interface over NATS.

```
Client (Go SDK) ──► NATS ──► Gateway Service ──► Provider Adapters ──► OpenAI / Anthropic / Ollama / ...
```

## Features

- **NATS-native** — no HTTP in the hot path; clients speak NATS protocol directly (TCP or WebSocket)
- **OpenAI-compatible SDK** — drop-in Go SDK with familiar `ChatCompletion` / `ChatCompletionStream` interface
- **Multi-provider routing** — route to OpenAI, Anthropic, Ollama, and more via pluggable adapters
- **Streaming** — token-by-token streaming over NATS subjects, chunks flow direct from adapter to client
- **Rate limiting** — per-key, per-model, and global limits backed by NATS KV
- **Authentication** — two layers: NATS native auth (NKeys/JWTs) + gateway API key validation
- **Observable** — structured logging, Prometheus metrics

## Quick Start

```bash
# Prerequisites: Go 1.22+, NATS server on localhost:4222

git clone https://github.com/kamalgs/nats-llm-gateway.git
cd nats-llm-gateway

# Start the gateway service
cp configs/gateway.yaml.example configs/gateway.yaml
# Edit configs/gateway.yaml with your provider API keys
go run ./cmd/gateway --config configs/gateway.yaml
```

Use the SDK in your application:

```go
import "github.com/kamalgs/nats-llm-gateway/pkg/client"

llm, _ := client.New(
    client.WithNATSURL("nats://localhost:4222"),
    client.WithAPIKey("sk-my-key"),
)
defer llm.Close()

// Non-streaming
resp, _ := llm.ChatCompletion(ctx, &client.ChatCompletionRequest{
    Model: "gpt-4o",
    Messages: []client.Message{
        {Role: "user", Content: "Hello!"},
    },
})
fmt.Println(resp.Choices[0].Message.Content)

// Streaming
stream, _ := llm.ChatCompletionStream(ctx, &client.ChatCompletionRequest{
    Model: "claude-sonnet",
    Messages: []client.Message{
        {Role: "user", Content: "Write a poem"},
    },
})
for stream.Next() {
    fmt.Print(stream.Current().Choices[0].Delta.Content)
}
```

## Documentation

- [Design & Requirements](docs/DESIGN.md) — architecture, SDK design, NATS subject layout, and milestones

## License

MIT
