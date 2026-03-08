# NATS LLM Gateway — Requirements & Design

## 1. Overview

NATS LLM Gateway is a **NATS-native** LLM gateway. There is no HTTP layer in
the gateway itself — clients connect directly to NATS (TCP or WebSocket) and
interact with LLM providers through NATS request/reply and streaming subjects.

A **Go SDK** provides an OpenAI-compatible programmatic interface over NATS,
making it trivial to migrate existing applications. A **client-side HTTP proxy**
can be added later for legacy HTTP clients.

```
                          NATS Server
                       (TCP + WebSocket)
                              │
          ┌───────────────────┼───────────────────┐
          │                   │                   │
   ┌──────┴──────┐    ┌──────┴──────┐    ┌──────┴──────┐
   │   Client    │    │   Gateway   │    │   Client    │
   │  (Go SDK)   │    │   Service   │    │  (any NATS  │
   │             │    │             │    │   client)   │
   └─────────────┘    └──────┬──────┘    └─────────────┘
                             │
          ┌──────────────────┼──────────────────┐
          ▼                  ▼                  ▼
   ┌────────────┐    ┌────────────┐     ┌────────────┐
   │  Provider  │    │  Provider  │     │  Provider  │
   │  Adapter:  │    │  Adapter:  │     │  Adapter:  │
   │  OpenAI    │    │  Anthropic │     │  Ollama    │
   └────────────┘    └────────────┘     └────────────┘
```

### Why NATS-native (no HTTP in the gateway)?

| Benefit | Detail |
|---|---|
| **Lower latency** | No HTTP parse/serialize overhead; NATS binary protocol is faster |
| **Built-in auth** | NATS has native user/token/NKey/JWT authentication — no custom auth middleware needed |
| **Built-in streaming** | NATS subjects are natural streaming channels — no SSE/chunked-encoding complexity |
| **WebSocket support** | NATS server natively exposes WebSocket endpoints — browser clients connect directly |
| **Simpler gateway** | The gateway is just a NATS service — no HTTP framework, no middleware stack |
| **Scalability** | Clients, gateway services, and adapters are all equal NATS participants; scale any independently |

---

## 2. Goals

1. **NATS-native protocol** — clients and gateway communicate purely over NATS (TCP or WebSocket).
2. **SDK with OpenAI-compatible interface** — Go SDK that mirrors the OpenAI chat completion API types and calling conventions, making migration from `openai-go` trivial.
3. **Multi-provider routing** — route to OpenAI, Anthropic, Ollama, vLLM, or any provider via pluggable adapters.
4. **Model aliasing & mapping** — expose virtual model names that map to real provider:model pairs.
5. **Streaming first** — token-by-token streaming over NATS subjects.
6. **Rate limiting** — per-user, per-model, and global rate limits enforced at the gateway service.
7. **Authentication** — leverage NATS native auth (NKeys, JWTs, tokens) + gateway-level API key validation.
8. **Observability** — structured logging, Prometheus metrics, OpenTelemetry traces.
9. **Future HTTP proxy** — a thin client-side HTTP→NATS proxy can be layered on later.

---

## 3. High-Level Requirements

### 3.1 Functional Requirements

| ID | Requirement | Priority |
|----|-------------|----------|
| FR-1 | Go SDK: `ChatCompletion(ctx, req)` with OpenAI-compatible request/response types | P0 |
| FR-2 | Go SDK: `ChatCompletionStream(ctx, req)` returning a token stream iterator | P0 |
| FR-3 | Gateway service: accept requests on NATS subjects, route by model | P0 |
| FR-4 | Provider adapters for OpenAI, Anthropic, Ollama | P0 |
| FR-5 | Model aliasing — map virtual model names to provider:model pairs | P1 |
| FR-6 | Authentication via NATS native auth (NKeys/JWTs) + gateway-level key check | P0 |
| FR-7 | Per-user and per-model rate limiting at the gateway | P0 |
| FR-8 | Return OpenAI-compatible response and error structures | P0 |
| FR-9 | List available models via NATS request (`llm.models`) | P1 |
| FR-10 | Request/response logging with redaction of sensitive fields | P1 |
| FR-11 | Graceful shutdown with in-flight request draining | P1 |
| FR-12 | Tool/function calling pass-through | P2 |
| FR-13 | Provider failover — retry on a secondary provider if primary fails | P2 |
| FR-14 | Client-side HTTP proxy (translates `POST /v1/chat/completions` ↔ NATS) | P2 |
| FR-15 | Python SDK wrapper | P2 |

### 3.2 Non-Functional Requirements

| ID | Requirement | Target |
|----|-------------|--------|
| NFR-1 | P99 gateway-added latency (excluding LLM time) | < 2 ms |
| NFR-2 | Concurrent request capacity | 10 000+ |
| NFR-3 | Configuration hot-reload without restart | Yes |
| NFR-4 | Single statically-linked binary (gateway) | Yes |
| NFR-5 | Container image size | < 30 MB |

---

## 4. Architecture

### 4.1 Components

```
nats-llm-gateway/
├── cmd/
│   └── gateway/              # Gateway service binary
├── pkg/
│   └── client/               # Go SDK (public API for consumers)
│       ├── client.go         # NATSChatClient — main entry point
│       ├── types.go          # OpenAI-compatible request/response types
│       └── stream.go         # Streaming iterator
├── internal/
│   ├── auth/                 # API-key / NATS credential validation
│   ├── ratelimit/            # Sliding-window limiter (NATS KV backed)
│   ├── router/               # Model → provider subject resolver
│   ├── provider/             # Provider adapter interface + implementations
│   │   ├── provider.go       # Interface
│   │   ├── openai/
│   │   ├── anthropic/
│   │   └── ollama/
│   ├── config/               # Config loading, validation, hot-reload
│   └── middleware/            # Gateway middleware chain (auth, ratelimit, logging)
├── configs/
│   └── gateway.yaml          # Reference configuration
├── docs/
│   └── DESIGN.md             # This document
├── Dockerfile
├── go.mod
└── go.sum
```

### 4.2 SDK — Client Interface

The SDK is the primary way applications interact with the gateway. It mirrors
the OpenAI Go SDK calling conventions:

```go
import "github.com/kamalgs/nats-llm-gateway/pkg/client"

// Connect to NATS (supports TCP, WebSocket, TLS, NKey auth, etc.)
llm, err := client.New(
    client.WithNATSURL("wss://nats.example.com:443"),
    client.WithAPIKey("sk-my-key"),
)
defer llm.Close()

// Non-streaming — identical shape to OpenAI's API
resp, err := llm.ChatCompletion(ctx, &client.ChatCompletionRequest{
    Model: "gpt-4o",
    Messages: []client.Message{
        {Role: "user", Content: "Hello!"},
    },
})
fmt.Println(resp.Choices[0].Message.Content)

// Streaming
stream, err := llm.ChatCompletionStream(ctx, &client.ChatCompletionRequest{
    Model: "claude-sonnet",
    Messages: []client.Message{
        {Role: "user", Content: "Write a poem"},
    },
})
for stream.Next() {
    chunk := stream.Current()
    fmt.Print(chunk.Choices[0].Delta.Content)
}
if err := stream.Err(); err != nil {
    log.Fatal(err)
}
```

**Migration path from OpenAI SDK**: The request/response types
(`ChatCompletionRequest`, `ChatCompletionResponse`, `Message`, `Choice`, etc.)
are intentionally compatible with OpenAI's schema. Switching from `openai-go`
requires changing the client constructor and import path — the rest of the
application code stays the same.

### 4.3 Request Flow

#### Non-Streaming (NATS Request/Reply)

```
Client SDK                    Gateway Service              Provider Adapter
    │                              │                              │
    │  NATS Request                │                              │
    │  subject: llm.chat.complete  │                              │
    │  payload: {model, messages}  │                              │
    │  reply-to: _INBOX.xxx        │                              │
    │ ────────────────────────────►│                              │
    │                              │  Authenticate + Rate Check   │
    │                              │  Resolve model → provider    │
    │                              │                              │
    │                              │  NATS Request                │
    │                              │  subject: llm.provider.openai│
    │                              │ ────────────────────────────►│
    │                              │                              │  HTTP call to
    │                              │                              │  OpenAI API
    │                              │          NATS Reply          │◄────────────
    │                              │◄─────────────────────────────│
    │        NATS Reply            │                              │
    │◄─────────────────────────────│                              │
```

#### Streaming (NATS Pub/Sub)

```
Client SDK                    Gateway Service              Provider Adapter
    │                              │                              │
    │  NATS Request                │                              │
    │  subject: llm.chat.stream    │                              │
    │  payload: {model, messages,  │                              │
    │   stream_subject:            │                              │
    │   _INBOX.stream.xxx}         │                              │
    │ ────────────────────────────►│                              │
    │                              │  Authenticate + Rate Check   │
    │                              │  Resolve model → provider    │
    │                              │                              │
    │                              │  NATS Request                │
    │                              │  subject: llm.provider.openai│
    │                              │  stream_reply:               │
    │                              │   _INBOX.stream.xxx          │
    │                              │ ────────────────────────────►│
    │                              │                              │
    │   NATS Pub (chunk 1)         │                              │
    │◄═══════════════════════════════════════════════════════════ │
    │   NATS Pub (chunk 2)         │                              │
    │◄═══════════════════════════════════════════════════════════ │
    │   NATS Pub (chunk N)         │                              │
    │◄═══════════════════════════════════════════════════════════ │
    │   NATS Pub ([DONE])          │                              │
    │◄═══════════════════════════════════════════════════════════ │
```

For streaming, the provider adapter publishes chunks **directly** to the
client's inbox subject — the gateway service doesn't sit in the data path for
every token. This minimizes latency. The gateway only handles the initial
request (auth, rate limit, routing).

### 4.4 NATS Subject Design

| Subject | Purpose | Pattern |
|---|---|---|
| `llm.chat.complete` | Non-streaming chat completion requests | Request/Reply |
| `llm.chat.stream` | Streaming chat completion requests | Request triggers pub/sub |
| `llm.models` | List available models | Request/Reply |
| `llm.provider.<name>` | Internal: gateway → provider adapter | Request/Reply + queue group |
| `llm.admin.reload` | Config hot-reload signal | Pub/Sub |

- The gateway service subscribes to `llm.chat.complete` and `llm.chat.stream`
  using **queue groups** for horizontal scaling.
- Provider adapters subscribe to `llm.provider.<name>` using **queue groups**
  so multiple replicas share load.
- Streaming chunks flow directly from adapter to client inbox — no gateway hop.

### 4.5 Wire Format

All messages are JSON-encoded. The wire types match OpenAI's API schema:

**Request** (published by SDK to `llm.chat.complete` or `llm.chat.stream`):
```json
{
  "model": "gpt-4o",
  "messages": [
    {"role": "system", "content": "You are helpful."},
    {"role": "user", "content": "Hello!"}
  ],
  "temperature": 0.7,
  "max_tokens": 1024,
  "stream_subject": "_INBOX.stream.abc123",
  "api_key": "sk-my-key"
}
```

`stream_subject` is only present for streaming requests. `api_key` is used
for gateway-level auth (complementing NATS-level auth).

**Response** (non-streaming reply):
```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1709900000,
  "model": "gpt-4o",
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "Hello! How can I help?"},
    "finish_reason": "stop"
  }],
  "usage": {"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20}
}
```

**Streaming chunk** (published to client's `stream_subject`):
```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion.chunk",
  "choices": [{
    "index": 0,
    "delta": {"content": "Hello"},
    "finish_reason": null
  }]
}
```

**Error**:
```json
{
  "error": {
    "message": "Rate limit exceeded. Retry after 2s.",
    "type": "rate_limit_error",
    "code": "rate_limit_exceeded"
  }
}
```

### 4.6 Configuration

```yaml
# configs/gateway.yaml
nats:
  url: "nats://localhost:4222"
  # For WebSocket clients, NATS server must be configured with websocket listener
  # (this is NATS server config, not gateway config)

auth:
  enabled: true
  keys:
    - key: "sk-proj-abc123"
      name: "frontend-app"
      rate_limit: "100/min"
      allowed_models: ["gpt-4o", "claude-sonnet"]
    - key: "sk-proj-def456"
      name: "batch-service"
      rate_limit: "1000/min"

rate_limit:
  global: "5000/min"
  per_model:
    "gpt-4o": "500/min"
    "claude-sonnet": "1000/min"

models:
  "gpt-4o":
    provider: openai
    upstream_model: "gpt-4o"
  "claude-sonnet":
    provider: anthropic
    upstream_model: "claude-sonnet-4-6-20250514"
  "llama3":
    provider: ollama
    upstream_model: "llama3:70b"

providers:
  openai:
    base_url: "https://api.openai.com/v1"
    api_key: "${OPENAI_API_KEY}"
  anthropic:
    base_url: "https://api.anthropic.com"
    api_key: "${ANTHROPIC_API_KEY}"
  ollama:
    base_url: "http://localhost:11434"
```

### 4.7 Authentication (Two Layers)

**Layer 1 — NATS native auth:**
- Clients authenticate to the NATS server using tokens, NKeys, or JWTs.
- NATS accounts and user permissions control which subjects a client can
  publish/subscribe to.
- This is standard NATS server configuration — the gateway doesn't implement it.

**Layer 2 — Gateway API key auth:**
- The gateway validates the `api_key` field in the request payload against its
  configured key store.
- Each key has associated permissions (allowed models, rate limits, metadata).
- This enables application-level identity and policy enforcement on top of
  NATS transport-level auth.

### 4.8 Rate Limiting

Sliding window algorithm enforced at the gateway service before routing:

1. **Per-key limits** — configured per API key (e.g., `100/min`).
2. **Per-model limits** — global limit across all keys for a given model.
3. **Global limit** — overall gateway request cap.

State is stored in **NATS KV** for distributed consistency across gateway
replicas. Falls back to in-memory for single-instance deployments.

Rate limit errors are returned as standard error responses on the NATS reply
subject.

---

## 5. Technology Choices

| Component | Choice | Rationale |
|---|---|---|
| Language | **Go** | Single binary, excellent concurrency, NATS has first-class Go client |
| NATS client | `github.com/nats-io/nats.go` | Official client |
| Config | `github.com/knadh/koanf` | Hot-reload, env var substitution, YAML |
| Logging | `log/slog` (stdlib) | Structured, zero-dependency |
| Metrics | `github.com/prometheus/client_golang` | Industry standard |
| Rate limiting | Custom (sliding window over NATS KV) | Distributed-friendly, no external deps |

---

## 6. Milestones

### M1 — Walking Skeleton
- [ ] Project scaffolding (Go module, directory structure)
- [ ] SDK: `client.New()` with NATS connection
- [ ] SDK: `ChatCompletion()` — non-streaming request/reply
- [ ] Gateway service: subscribe to `llm.chat.complete`, route to provider
- [ ] OpenAI provider adapter (pass-through)
- [ ] End-to-end: SDK → NATS → Gateway → OpenAI → response

### M2 — Streaming & Multi-Provider
- [ ] SDK: `ChatCompletionStream()` with iterator
- [ ] Gateway + adapter streaming via NATS pub/sub
- [ ] Anthropic provider adapter (Messages API → OpenAI format translation)
- [ ] Ollama provider adapter
- [ ] Model aliasing and routing

### M3 — Auth & Rate Limiting
- [ ] Gateway API key authentication
- [ ] Per-key and per-model rate limiting (NATS KV backed)
- [ ] NATS server auth configuration examples (NKeys, JWTs)

### M4 — Production Readiness
- [ ] Prometheus metrics (exposed via small HTTP endpoint on gateway)
- [ ] Health check via NATS subject (`llm.health`)
- [ ] Graceful shutdown with in-flight draining
- [ ] Config hot-reload via NATS signal
- [ ] Dockerfile & docker-compose (gateway + NATS server with WS enabled)
- [ ] Integration tests

### M5 — Advanced Features
- [ ] Tool/function calling pass-through
- [ ] Provider failover
- [ ] NATS JetStream persistence mode
- [ ] Client-side HTTP→NATS proxy (`POST /v1/chat/completions` for legacy clients)
- [ ] Python SDK wrapper
- [ ] Additional provider adapters (Google Vertex, vLLM)

---

## 7. Open Questions

1. **Should adapters run in-process or as separate binaries?**
   Starting in-process for simplicity; the NATS subject-based architecture
   allows splitting them out later with zero changes to the gateway or SDK.

2. **Token counting for rate limiting?**
   Initial rate limiting is request-count based. Token-based limits (using
   tiktoken or provider-reported usage) is a future enhancement.

3. **Multi-tenancy?**
   NATS accounts provide natural tenant isolation. The gateway API key model
   provides basic tenancy. Full multi-tenant isolation (separate NATS
   accounts per tenant) can be layered on.

4. **Should streaming chunks route through the gateway or go direct?**
   Current design: direct from adapter to client inbox for minimum latency.
   Trade-off: gateway can't observe/meter individual chunks. If per-token
   metering is needed, chunks can be routed through the gateway with a
   subject rewrite.
