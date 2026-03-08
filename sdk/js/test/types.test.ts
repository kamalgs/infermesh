import { describe, it, expect } from "vitest";
import type {
  ChatCompletionRequest,
  ChatCompletionResponse,
  ErrorResponse,
} from "../src/types.js";

describe("types", () => {
  it("ChatCompletionRequest matches OpenAI schema", () => {
    const req: ChatCompletionRequest = {
      model: "gpt-4o",
      messages: [
        { role: "system", content: "You are helpful." },
        { role: "user", content: "Hello!" },
      ],
      temperature: 0.7,
      max_tokens: 1024,
    };

    const json = JSON.stringify(req);
    const parsed = JSON.parse(json) as ChatCompletionRequest;

    expect(parsed.model).toBe("gpt-4o");
    expect(parsed.messages).toHaveLength(2);
    expect(parsed.messages[1].content).toBe("Hello!");
    expect(parsed.temperature).toBe(0.7);
    expect(parsed.max_tokens).toBe(1024);
  });

  it("ChatCompletionResponse roundtrips with real OpenAI JSON", () => {
    const openaiJson = `{
      "id": "chatcmpl-abc123",
      "object": "chat.completion",
      "created": 1709900000,
      "model": "gpt-4o-2024-08-06",
      "choices": [{
        "index": 0,
        "message": {"role": "assistant", "content": "Hello!"},
        "finish_reason": "stop"
      }],
      "usage": {
        "prompt_tokens": 12,
        "completion_tokens": 8,
        "total_tokens": 20
      }
    }`;

    const resp = JSON.parse(openaiJson) as ChatCompletionResponse;

    expect(resp.id).toBe("chatcmpl-abc123");
    expect(resp.object).toBe("chat.completion");
    expect(resp.choices[0].message?.content).toBe("Hello!");
    expect(resp.choices[0].finish_reason).toBe("stop");
    expect(resp.usage?.total_tokens).toBe(20);
  });

  it("ErrorResponse has correct structure", () => {
    const err: ErrorResponse = {
      error: {
        message: "model not found",
        type: "error",
        code: "model_not_found",
      },
    };

    const json = JSON.stringify(err);
    const parsed = JSON.parse(json) as ErrorResponse;

    expect(parsed.error.code).toBe("model_not_found");
    expect(parsed.error.message).toBe("model not found");
  });

  it("optional fields are correctly omitted", () => {
    const req: ChatCompletionRequest = {
      model: "gpt-4o",
      messages: [{ role: "user", content: "Hi" }],
    };

    const json = JSON.stringify(req);
    const parsed = JSON.parse(json);

    expect(parsed.temperature).toBeUndefined();
    expect(parsed.max_tokens).toBeUndefined();
    expect(parsed.stream).toBeUndefined();
  });
});
