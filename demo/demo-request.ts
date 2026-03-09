import { InferMeshClient } from "../sdk/js/src/index.js";

async function main() {
  const client = await InferMeshClient.connect({
    natsUrl: process.env.NATS_URL,
  });

  const model = process.env.MODEL!;
  console.log(">>> Model:", model);

  const resp = await client.chat.completions.create({
    model,
    messages: [{ role: "user", content: "Say hello in one sentence." }],
    max_tokens: 64,
  });

  console.log("    Upstream model:", resp.model);
  console.log("    Response:", resp.choices[0]?.message?.content);
  console.log("    Tokens:", resp.usage?.total_tokens ?? "N/A");

  await client.close();
}

main();
