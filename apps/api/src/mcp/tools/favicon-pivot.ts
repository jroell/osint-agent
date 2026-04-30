import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().url().describe("Target site URL — favicon will be auto-discovered (HTML <link rel='icon'> first, /favicon.ico fallback)"),
  pivot_limit: z.number().int().min(1).max(200).default(50).describe("Max urlscan results to return"),
});

toolRegistry.register({
  name: "favicon_pivot",
  description:
    "**Marquee entity-resolution tool.** Fetches a site's favicon, computes MD5 + SHA256 + FOFA-style mmh3 hash, then auto-pivots through urlscan.io's 800M-scan archive to find every other site serving the same favicon. Returns a ready-to-paste Shodan/FOFA/ZoomEye/Censys query string. Why this works: favicons rarely change, so identical favicons across hosts almost always indicate shared infrastructure — hidden CDN origins, brand subdomains on different SLDs, phishing lookalikes copying real favicons, forgotten staging environments. The mmh3 hash is the canonical bug-bounty pivot key — same hash works across all 4 major internet-scan platforms. No API key required (uses urlscan public tier; URLSCAN_API_KEY raises rate limit).",
  inputSchema: input,
  costMillicredits: 8,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "favicon_pivot",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "favicon_pivot failed");
    return res.output;
  },
});
