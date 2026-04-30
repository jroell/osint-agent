import { z } from "zod";
import { toolRegistry } from "./instance";
import { callPyWorker } from "../../workers/py-client";

const input = z.object({
  keywords: z.array(z.string()).min(1).max(5).describe("Up to 5 search terms (Google Trends hard cap)").optional(),
  keyword: z.string().optional().describe("Single keyword (alternative to keywords array)"),
  timeframe: z.string().default("today 12-m").describe("Time window: 'today 12-m', 'today 5-y', 'now 7-d', 'all', or 'YYYY-MM-DD YYYY-MM-DD'"),
  geo: z.string().default("").describe("ISO country code (e.g. 'US', 'GB', 'DE') or empty for worldwide"),
  include_related: z.boolean().default(true),
  include_regional: z.boolean().default(true),
}).refine((d) => (d.keywords && d.keywords.length > 0) || !!d.keyword, { message: "keywords or keyword required" });

toolRegistry.register({
  name: "google_trends_lookup",
  description:
    "**Google Trends search-volume intel** — interest-over-time, interest-by-region (top 30 countries), related queries (top + rising), per keyword. Free, no key (uses unofficial pytrends API; fragile but reliable for normal queries). Up to 5 keywords compared simultaneously. Use cases: brand monitoring (search velocity vs competitors), geographic intel (where is X searched most?), discovery (what keywords are people associating with X?), threat-actor tracking (search-volume spikes around exploit names). Pairs with `reddit_org_intel` + `hackernews_search` to triangulate brand discussion across discovery (search) + community (social) signals.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callPyWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "google_trends_lookup",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "google_trends_lookup failed");
    return res.output;
  },
});
