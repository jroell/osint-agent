import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["global_top", "global_rising", "by_country", "search"]).default("global_top"),
  country: z.string().optional().describe("For mode='by_country' (e.g. 'United States', 'Germany', 'India')"),
  search_term: z.string().optional().describe("For mode='search' — only finds terms in the top 25 per DMA"),
  limit: z.number().int().min(1).max(100).default(25),
  weeks_back: z.number().int().min(1).max(52).default(1).describe("For mode='search' — historical window in weeks"),
});

toolRegistry.register({
  name: "bigquery_trending_now",
  description:
    "**Official Google Trends via BigQuery** (`bigquery-public-data.google_trends.*`) — no rate limits, sanctioned dataset. Modes: 'global_top' (current US top 25 per DMA), 'global_rising' (newly-trending), 'by_country' (top 25 in any country), 'search' (find a term in the top-25-per-DMA archive — only works for popular terms). Use this for 'what's hot now' intel; pair with `google_trends_lookup` (pytrends) for arbitrary niche-keyword tracking. Requires gcloud-authenticated host.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "bigquery_trending_now", input: i, timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "bigquery_trending_now failed");
    return res.output;
  },
});
