import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url_a: z.string().describe("First URL to compare"),
  url_b: z.string().describe("Second URL to compare"),
});

toolRegistry.register({
  name: "tracker_correlate",
  description:
    "**The 'are these two sites the same operator?' primitive — 100% free, no external deps.** Runs `tracker_extract` on both URLs in parallel, computes overlap on each tracker platform (GA, GTM, FB pixel, Hotjar, LinkedIn, Shopify, etc.), and returns a verdict: `same` (strong-strength ID match — near-certain), `likely-same` (multi-medium overlap), `unrelated` (both have IDs, no overlap), or `inconclusive` (SPA-rendered, no IDs visible). Returns shared IDs per platform, only-on-A / only-on-B IDs (lateral discovery hints), and shared 3rd-party domains. The cleanest ER primitive for connecting two URLs without paying for tracker-pivot databases.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "tracker_correlate",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "tracker_correlate failed");
    return res.output;
  },
});
