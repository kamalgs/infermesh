#!/usr/bin/env npx tsx
// Interactive chat CLI using InferMeshClient over NATS (no HTTP proxy needed).
// Run: NATS_URL=nats://localhost:14225 npx tsx examples/chat.ts

import * as readline from "readline";
import { InferMeshClient, type Message } from "../sdk/js/src/index.js";

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
    "Type /quit to exit, /model <name> to switch models, /clear to reset history.\n"
  );

  const rl = readline.createInterface({
    input: process.stdin,
    output: process.stdout,
  });

  const history: Message[] = [];
  let eofReached = false;
  let totalBytesSent = 0;
  let totalBytesReceived = 0;

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
      history.length = 0;
      console.log("History cleared.");
      continue;
    }
    if (trimmed.startsWith("/model ")) {
      model = trimmed.slice(7).trim();
      console.log(`Switched to ${model}`);
      continue;
    }

    history.push({ role: "user", content: trimmed });

    try {
      const result = await client.chat.completions.createWithStats({
        model,
        messages: history,
        max_tokens: 512,
      });

      totalBytesSent += result.bytesSent;
      totalBytesReceived += result.bytesReceived;

      const reply = result.response.choices[0]?.message?.content ?? "(no response)";
      history.push({ role: "assistant", content: reply });
      console.log(`\n${reply}`);
      console.log(`  [sent: ${fmtBytes(result.bytesSent)} | recv: ${fmtBytes(result.bytesReceived)} | total: ${fmtBytes(totalBytesSent)}/${fmtBytes(totalBytesReceived)}]\n`);
    } catch (err: any) {
      console.error(`Error: ${err.message}`);
      history.pop();
    }
  }

  rl.close();
  await client.close();
}

main().catch((err) => {
  console.error("Fatal:", err);
  process.exit(1);
});
