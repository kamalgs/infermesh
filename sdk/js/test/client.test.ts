import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { connect, type NatsConnection, StringCodec } from "nats";
import { InferMeshClient } from "../src/index.js";
import type { ChatCompletionResponse, ErrorResponse } from "../src/types.js";

const sc = StringCodec();

// These tests require a NATS server running on localhost:4222.
// Skip if not available.
const NATS_URL = process.env.NATS_URL || "nats://localhost:4222";

describe("InferMeshClient", () => {
  let mockNc: NatsConnection;

  beforeAll(async () => {
    try {
      mockNc = await connect({ servers: NATS_URL });
    } catch {
      console.warn("NATS not available, skipping client tests");
      return;
    }

    // Mock provider subscribes to llm.provider.openai.> (wildcard)
    mockNc.subscribe("llm.provider.openai.>", {
      callback: (_err, msg) => {
        const provReq = JSON.parse(sc.decode(msg.data));
        const resp: ChatCompletionResponse = {
          id: "test-sdk",
          object: "chat.completion",
          created: Date.now(),
          model: provReq.upstream_model,
          choices: [
            {
              index: 0,
              message: {
                role: "assistant",
                content: `sdk echo: ${provReq.request.messages[0]?.content}`,
              },
              finish_reason: "stop",
            },
          ],
          usage: { prompt_tokens: 5, completion_tokens: 3, total_tokens: 8 },
        };
        msg.respond(sc.encode(JSON.stringify(resp)));
      },
    });

    // Mock ollama provider
    mockNc.subscribe("llm.provider.ollama.>", {
      callback: (_err, msg) => {
        const provReq = JSON.parse(sc.decode(msg.data));
        const resp: ChatCompletionResponse = {
          id: "test-ollama",
          object: "chat.completion",
          created: Date.now(),
          model: provReq.upstream_model,
          choices: [
            {
              index: 0,
              message: {
                role: "assistant",
                content: `ollama echo: ${provReq.request.messages[0]?.content}`,
              },
              finish_reason: "stop",
            },
          ],
          usage: { prompt_tokens: 5, completion_tokens: 3, total_tokens: 8 },
        };
        msg.respond(sc.encode(JSON.stringify(resp)));
      },
    });
  });

  afterAll(async () => {
    if (mockNc) await mockNc.drain();
  });

  it("routes to correct provider subject", async () => {
    if (!mockNc) return;

    const client = await InferMeshClient.connect({ natsUrl: NATS_URL });

    const resp = await client.chat.completions.create({
      model: "openai.gpt-4o",
      messages: [{ role: "user", content: "hello from sdk" }],
    });

    expect(resp.id).toBe("test-sdk");
    expect(resp.model).toBe("gpt-4o");
    expect(resp.choices[0].message?.content).toBe(
      "sdk echo: hello from sdk"
    );
    expect(resp.usage?.total_tokens).toBe(8);

    await client.close();
  });

  it("routes to ollama provider", async () => {
    if (!mockNc) return;

    const client = await InferMeshClient.connect({ natsUrl: NATS_URL });

    const resp = await client.chat.completions.create({
      model: "ollama.qwen2.5:0.5b",
      messages: [{ role: "user", content: "test ollama" }],
    });

    expect(resp.id).toBe("test-ollama");
    expect(resp.model).toBe("qwen2.5:0.5b");
    expect(resp.choices[0].message?.content).toBe("ollama echo: test ollama");

    await client.close();
  });

  it("accepts pre-existing NATS connection", async () => {
    if (!mockNc) return;

    const nc = await connect({ servers: NATS_URL });
    const client = await InferMeshClient.connect({ nc });

    const resp = await client.chat.completions.create({
      model: "openai.test-model",
      messages: [{ role: "user", content: "pre-connected" }],
    });

    expect(resp.choices[0].message?.content).toBe(
      "sdk echo: pre-connected"
    );

    await client.close();
    // nc should not be drained since we didn't own it
    expect(nc.isClosed()).toBe(false);
    await nc.drain();
  });

  it("throws on invalid model format", async () => {
    if (!mockNc) return;

    const client = await InferMeshClient.connect({ natsUrl: NATS_URL });

    await expect(
      client.chat.completions.create({
        model: "no-dot-model",
        messages: [{ role: "user", content: "test" }],
      })
    ).rejects.toThrow("must be in the form provider.model");

    await client.close();
  });

  it("throws on error response", async () => {
    if (!mockNc) return;

    // Mock error provider
    const errSub = mockNc.subscribe("llm.provider.errtest.>", {
      callback: (_err, msg) => {
        const errResp: ErrorResponse = {
          error: {
            message: "test error",
            type: "error",
            code: "test_error",
          },
        };
        msg.respond(sc.encode(JSON.stringify(errResp)));
      },
    });

    const client = await InferMeshClient.connect({ natsUrl: NATS_URL });

    await expect(
      client.chat.completions.create({
        model: "errtest.model",
        messages: [{ role: "user", content: "test" }],
      })
    ).rejects.toThrow("[test_error] test error");

    await client.close();
    await errSub.drain();
  });
});
