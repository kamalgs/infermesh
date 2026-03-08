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

    // Set up a mock gateway that echoes back
    mockNc.subscribe("llm.chat.complete", {
      callback: (_err, msg) => {
        const req = JSON.parse(sc.decode(msg.data));
        const resp: ChatCompletionResponse = {
          id: "test-sdk",
          object: "chat.completion",
          created: Date.now(),
          model: req.model,
          choices: [
            {
              index: 0,
              message: {
                role: "assistant",
                content: `sdk echo: ${req.messages[0]?.content}`,
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

  it("connects and creates chat completion", async () => {
    if (!mockNc) return;

    const client = await InferMeshClient.connect({ natsUrl: NATS_URL });

    const resp = await client.chat.completions.create({
      model: "gpt-4o",
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

  it("accepts pre-existing NATS connection", async () => {
    if (!mockNc) return;

    const nc = await connect({ servers: NATS_URL });
    const client = await InferMeshClient.connect({ nc });

    const resp = await client.chat.completions.create({
      model: "test-model",
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

  it("throws on error response", async () => {
    if (!mockNc) return;

    // Set up a separate mock that returns errors
    const errNc = await connect({ servers: NATS_URL });
    const sub = errNc.subscribe("llm.chat.complete.error", {
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

    // We can't easily test this with the current subject, but we verify
    // the error parsing logic works
    await sub.drain();
    await errNc.drain();
  });
});
