import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  username: z.string().min(1).describe("Keybase username"),
});

toolRegistry.register({
  name: "keybase_lookup",
  description:
    "Look up a Keybase user and return CRYPTOGRAPHICALLY VERIFIED identity proofs across Twitter, GitHub, Reddit, HackerNews, and websites. Each linked account has been signed by the Keybase user — much higher precision than Sherlock-family heuristic matching. Returns PGP fingerprint, full name, bio, and per-platform proof URLs. Free, no key. Sparser data than 5y ago (Keybase usage peaked pre-Zoom-acquisition) but when present it's gold.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "keybase_lookup",
      input: i,
      timeoutMs: 15_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "keybase_lookup failed");
    return res.output;
  },
});
