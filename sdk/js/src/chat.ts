import type { NatsConnection } from "nats";
import { StringCodec, createInbox } from "nats";
import type {
  ChatCompletionRequest,
  ChatCompletionResponse,
  ErrorResponse,
} from "./types.js";

const sc = StringCodec();

// ProviderRequest matches the Go api.ProviderRequest wire format.
interface ProviderRequest {
  upstream_model: string;
  request: ChatCompletionRequest;
}

function parseModel(model: string): { provider: string; upstream: string } {
  const i = model.indexOf(".");
  if (i <= 0 || i === model.length - 1) {
    throw new Error(
      `model "${model}" must be in the form provider.model (e.g., ollama.qwen2.5:0.5b)`
    );
  }
  return { provider: model.slice(0, i), upstream: model.slice(i + 1) };
}

export interface ChatCompletionResult {
  response: ChatCompletionResponse;
  bytesSent: number;
  bytesReceived: number;
}

export class ChatCompletions {
  constructor(private nc: NatsConnection) {}

  async createWithStats(req: ChatCompletionRequest): Promise<ChatCompletionResult> {
    const result = await this._send(req);
    return result;
  }

  async create(req: ChatCompletionRequest): Promise<ChatCompletionResponse> {
    const result = await this._send(req);
    return result.response;
  }

  private async _send(req: ChatCompletionRequest): Promise<ChatCompletionResult> {
    const { provider, upstream } = parseModel(req.model);
    const subject = `llm.provider.${provider}.${upstream}`;

    const providerReq: ProviderRequest = {
      upstream_model: upstream,
      request: req,
    };

    const payload = sc.encode(JSON.stringify(providerReq));

    // Create a temp reply subject, subscribe, publish, and wait.
    const replySubject = createInbox();
    const sub = this.nc.subscribe(replySubject, { max: 1 });

    this.nc.publish(subject, payload, { reply: replySubject });

    for await (const msg of sub) {
      const bytesReceived = msg.data.length;
      const data = JSON.parse(sc.decode(msg.data));

      if (data.error) {
        const err = data as ErrorResponse;
        throw new Error(`[${err.error.code}] ${err.error.message}`);
      }

      return {
        response: data as ChatCompletionResponse,
        bytesSent: payload.length,
        bytesReceived,
      };
    }

    throw new Error("no response received");
  }
}

export class Chat {
  completions: ChatCompletions;

  constructor(nc: NatsConnection) {
    this.completions = new ChatCompletions(nc);
  }
}
