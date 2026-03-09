import type { NatsConnection } from "nats";
import { StringCodec, createInbox } from "nats";
import type {
  ChatCompletionRequest,
  ChatCompletionResponse,
  ErrorResponse,
  Message,
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
    return this._send(req);
  }

  async create(req: ChatCompletionRequest): Promise<ChatCompletionResponse> {
    const result = await this._send(req);
    return result.response;
  }

  /**
   * Send to a specific subject (used for session-based messaging).
   */
  async sendToSubject(
    subject: string,
    req: ChatCompletionRequest,
  ): Promise<ChatCompletionResult> {
    const { upstream } = parseModel(req.model);

    const providerReq: ProviderRequest = {
      upstream_model: upstream,
      request: req,
    };

    return this._publish(subject, providerReq);
  }

  private async _send(req: ChatCompletionRequest): Promise<ChatCompletionResult> {
    const { provider, upstream } = parseModel(req.model);
    const subject = `llm.provider.${provider}.${upstream}`;

    const providerReq: ProviderRequest = {
      upstream_model: upstream,
      request: req,
    };

    return this._publish(subject, providerReq);
  }

  private async _publish(
    subject: string,
    providerReq: ProviderRequest,
  ): Promise<ChatCompletionResult> {
    const payload = sc.encode(JSON.stringify(providerReq));

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

/**
 * ChatSession maintains a sticky session with a provider.
 * The provider keeps the conversation context; the client sends only new messages.
 * If the session expires, falls back to sending full history.
 */
export class ChatSession {
  private sessionId: string | null = null;
  private sessionSubject: string | null = null;
  private history: Message[] = [];
  private completions: ChatCompletions;
  private model: string;

  constructor(completions: ChatCompletions, model: string) {
    this.completions = completions;
    this.model = model;
  }

  async send(
    content: string,
    opts?: { temperature?: number; max_tokens?: number },
  ): Promise<ChatCompletionResult> {
    const userMsg: Message = { role: "user", content };
    this.history.push(userMsg);

    try {
      let result: ChatCompletionResult;

      if (this.sessionId && this.sessionSubject) {
        // Session mode: send only the new message
        try {
          result = await this.completions.sendToSubject(
            this.sessionSubject,
            {
              model: this.model,
              messages: [userMsg],
              session_id: this.sessionId,
              ...opts,
            },
          );
        } catch (err: any) {
          if (err.message.includes("session_expired")) {
            // Session expired: reset and send full history
            this.sessionId = null;
            this.sessionSubject = null;
            result = await this.completions.createWithStats({
              model: this.model,
              messages: this.history,
              ...opts,
            });
          } else {
            throw err;
          }
        }
      } else {
        // No session yet: send full history
        result = await this.completions.createWithStats({
          model: this.model,
          messages: this.history,
          ...opts,
        });
      }

      // Track session from response
      if (result.response.session_id) {
        this.sessionId = result.response.session_id;
      }
      if (result.response.session_subject) {
        this.sessionSubject = result.response.session_subject;
      }

      // Keep local history in sync (for fallback)
      const reply = result.response.choices[0]?.message;
      if (reply) {
        this.history.push(reply);
      }

      return result;
    } catch (err) {
      this.history.pop(); // remove failed user message
      throw err;
    }
  }

  clear(): void {
    this.sessionId = null;
    this.sessionSubject = null;
    this.history = [];
  }

  getSessionId(): string | null {
    return this.sessionId;
  }

  getHistory(): Message[] {
    return [...this.history];
  }
}

export class Chat {
  completions: ChatCompletions;

  constructor(nc: NatsConnection) {
    this.completions = new ChatCompletions(nc);
  }

  createSession(model: string): ChatSession {
    return new ChatSession(this.completions, model);
  }
}
