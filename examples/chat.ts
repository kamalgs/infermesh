#!/usr/bin/env npx tsx
// Interactive chat CLI using InferMeshClient over NATS (no HTTP proxy needed).
// Uses sticky sessions: provider maintains context, client sends only new messages.
// Run: NATS_URL=nats://localhost:14225 npx tsx examples/chat.ts

import * as readline from "readline";
import { InferMeshClient } from "../sdk/js/src/index.js";

const natsUrl = process.env.NATS_URL || "nats://localhost:14225";
let model = process.env.MODEL || "ollama.qwen2.5:0.5b";

function fmtBytes(n: number): string {
  if (n < 1024) return `${n}B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)}KB`;
  return `${(n / (1024 * 1024)).toFixed(1)}MB`;
}

async function main() {
  const client = await InferMeshClient.connect({ natsUrl });
  console.log(`InferMesh Chat — ${model} via ${natsUrl}`);
  console.log(
    "Type /quit to exit, /model <name> to switch models, /clear to reset session.\n"
  );

  const rl = readline.createInterface({
    input: process.stdin,
    output: process.stdout,
  });

  let session = client.chat.createSession(model);
  let eofReached = false;
  let totalBytesSent = 0;
  let totalBytesReceived = 0;
  let totalPromptTokens = 0;
  let totalCompletionTokens = 0;

  rl.on("close", () => {
    eofReached = true;
  });

  const ask = (): Promise<string | null> =>
    new Promise((resolve) => {
      if (eofReached) return resolve(null);
      rl.question("> ", resolve);
    });

  while (true) {
    const input = await ask();
    if (input === null) break;

    const trimmed = input.trim();
    if (!trimmed) continue;

    if (trimmed === "/quit") break;
    if (trimmed === "/clear") {
      session = client.chat.createSession(model);
      totalBytesSent = 0;
      totalBytesReceived = 0;
      totalPromptTokens = 0;
      totalCompletionTokens = 0;
      console.log("Session cleared.");
      continue;
    }
    if (trimmed.startsWith("/model ")) {
      model = trimmed.slice(7).trim();
      session = client.chat.createSession(model);
      totalBytesSent = 0;
      totalBytesReceived = 0;
      totalPromptTokens = 0;
      totalCompletionTokens = 0;
      console.log(`Switched to ${model}`);
      continue;
    }

    try {
      const result = await session.send(trimmed, { max_tokens: 512 });

      totalBytesSent += result.bytesSent;
      totalBytesReceived += result.bytesReceived;

      const usage = result.response.usage;
      const promptTok = usage?.prompt_tokens ?? 0;
      const completionTok = usage?.completion_tokens ?? 0;
      totalPromptTokens += promptTok;
      totalCompletionTokens += completionTok;

      const reply = result.response.choices[0]?.message?.content ?? "(no response)";
      const sid = session.getSessionId();
      console.log(`\n${reply}`);
      console.log(
        `  [tokens: ${promptTok}+${completionTok} (${totalPromptTokens}+${totalCompletionTokens}) | bytes: ${fmtBytes(result.bytesSent)}/${fmtBytes(result.bytesReceived)} (${fmtBytes(totalBytesSent)}/${fmtBytes(totalBytesReceived)})${sid ? ` | session: ${sid.slice(0, 8)}` : ""}]\n`
      );
    } catch (err: any) {
      console.error(`Error: ${err.message}`);
    }
  }

  rl.close();
  await client.close();
}

main().catch((err) => {
  console.error("Fatal:", err);
  process.exit(1);
});
