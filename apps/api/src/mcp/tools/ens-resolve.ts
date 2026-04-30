import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(3).describe("ENS name (e.g. 'vitalik.eth') or Ethereum 0x address"),
});

toolRegistry.register({
  name: "ens_resolve",
  description:
    "**Web3 cross-platform identity resolver** — given an ENS name OR a 0x address, returns ALL identities tied to that wallet across ENS / Lens / Farcaster / dotbit / Linkea, plus aggregated social handles (Twitter, GitHub, website, email), bio, avatar, content-hash. Uses web3.bio meta-resolver (free, no key) with ensdata.net fallback. Crypto-native ER primitive — getting from a wallet to a Twitter handle is the Web3 equivalent of `tracker_correlate`'s operator-binding signal. Use case: trace anonymous wallet activity to a real-world identity.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "ens_resolve", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "ens_resolve failed");
    return res.output;
  },
});
