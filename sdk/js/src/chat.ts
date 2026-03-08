import type { NatsConnection } from "nats";
import { StringCodec } from "nats";
import type {
  ChatCompletionRequest,
  ChatCompletionResponse,
  ErrorResponse,
} from "./types.js";

const sc = StringCodec();
const REQUEST_TIMEOUT = 30_000; // 30 seconds

export class ChatCompletions {
  constructor(private nc: NatsConnection) {}

  async create(req: ChatCompletionRequest): Promise<ChatCompletionResponse> {
    const payload = sc.encode(JSON.stringify(req));
    const msg = await this.nc.request("llm.chat.complete", payload, {
      timeout: REQUEST_TIMEOUT,
    });

    const data = JSON.parse(sc.decode(msg.data));

    // Check for error response
    if (data.error) {
      const err = data as ErrorResponse;
      throw new Error(`[${err.error.code}] ${err.error.message}`);
    }

    return data as ChatCompletionResponse;
  }
}

export class Chat {
  completions: ChatCompletions;

  constructor(nc: NatsConnection) {
    this.completions = new ChatCompletions(nc);
  }
}
