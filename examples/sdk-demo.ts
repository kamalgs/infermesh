// M0 tracer bullet — test the JS SDK end-to-end
// Prerequisites: NATS running, gateway running
// Run: cd sdk/js && npx tsx ../../examples/sdk-demo.ts

import { NATSLLMClient } from "../sdk/js/src/index.js";

async function main() {
  const client = await NATSLLMClient.connect({
    natsUrl: process.env.NATS_URL || "nats://localhost:4222",
  });

  console.log("Connected to NATS\n");

  try {
    console.log("=== Chat completion (via JS SDK → NATS → gateway → OpenAI) ===");
    const response = await client.chat.completions.create({
      model: "gpt-4o",
      messages: [{ role: "user", content: "Say hello in one sentence." }],
    });

    console.log("Response:", JSON.stringify(response, null, 2));
    console.log("\nAssistant:", response.choices[0]?.message?.content);
  } catch (err) {
    console.error("Error:", err);
  } finally {
    await client.close();
  }
}

main();
