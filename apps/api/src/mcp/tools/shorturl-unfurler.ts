import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().describe("Shortened URL to unfurl (e.g. 'bit.ly/abc123', 'https://t.co/xyz')"),
  max_hops: z.number().int().min(1).max(50).default(15),
});

toolRegistry.register({
  name: "shorturl_unfurler",
  description:
    "**Phishing-analysis primitive.** Follows redirect chain of shortened URLs (bit.ly, t.co, ow.ly, lnkd.in, discord.gg, etc.) capturing each hop's URL/status/Set-Cookie/redirect mechanism. Detects three redirect types: 30x HTTP, meta-refresh, AND `javascript:window.location` (the latter two used by phishing operators to evade simple HTTP redirect followers). Computes suspicion_score (0-100) combining hop count, distinct domains, and presence of evasive mechanisms. Use to trace short links to final landing pages WITHOUT visiting them in a browser. Free, no key.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "shorturl_unfurler", input: i, timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "shorturl_unfurler failed");
    return res.output;
  },
});
