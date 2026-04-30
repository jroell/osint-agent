import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  tracker_id: z.string().min(2).describe("The tracker ID surfaced by tracker_extract (e.g. 'GTM-MZPXMPW6', 'UA-12345-1', 'G-ABC123', '987654321' for FB pixel)"),
  platform: z.enum([
    "google_analytics_universal",
    "google_analytics_4",
    "google_tag_manager",
    "facebook_pixel",
    "hotjar",
    "linkedin_insight",
    "stripe_publishable_key",
    "generic",
  ]).optional().describe("Platform hint for query precision. If omitted, inferred from ID prefix."),
  limit: z.number().int().min(10).max(1000).default(100).describe("Max results from urlscan (free tier: 1000/day)"),
});

toolRegistry.register({
  name: "tracker_pivot",
  description:
    "**The missing half of `tracker_extract`.** Given a tracker ID (GTM-XXX, UA-XXX, G-XXX, FB pixel, Hotjar, LinkedIn, Stripe pk_), searches urlscan.io's 800M-scan archive for EVERY OTHER site running the same ID. Returns aggregated unique domains/IPs/ASNs + hosting clusters (ASN-name → domains). Same tracker ID across two domains = nearly-certain shared operator — this is how investigative journalists map shell-site networks. Free urlscan tier (1000 searches/day, no key needed); set URLSCAN_API_KEY for higher quota. Pair with tracker_extract: extract IDs from one site → pivot each high-strength ID → map operator's full portfolio.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "tracker_pivot",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "tracker_pivot failed");
    return res.output;
  },
});
