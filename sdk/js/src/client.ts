import { connect, type NatsConnection, type ConnectionOptions } from "nats";
import { Chat } from "./chat.js";

export interface NATSLLMClientOptions {
  /** NATS server URL, e.g. "nats://localhost:4222" */
  natsUrl?: string;
  /** API key for gateway-level auth (M0: unused, placeholder) */
  apiKey?: string;
  /** Pass an existing NATS connection instead of creating one */
  nc?: NatsConnection;
}

export class NATSLLMClient {
  private nc: NatsConnection;
  private ownsConnection: boolean;
  chat: Chat;

  private constructor(nc: NatsConnection, ownsConnection: boolean) {
    this.nc = nc;
    this.ownsConnection = ownsConnection;
    this.chat = new Chat(nc);
  }

  /**
   * Connect to NATS and create a new client.
   * Use this as the primary constructor since NATS connection is async.
   */
  static async connect(opts: NATSLLMClientOptions = {}): Promise<NATSLLMClient> {
    if (opts.nc) {
      return new NATSLLMClient(opts.nc, false);
    }

    const natsOpts: ConnectionOptions = {
      servers: opts.natsUrl || "nats://localhost:4222",
    };

    const nc = await connect(natsOpts);
    return new NATSLLMClient(nc, true);
  }

  async close(): Promise<void> {
    if (this.ownsConnection) {
      await this.nc.drain();
    }
  }
}
