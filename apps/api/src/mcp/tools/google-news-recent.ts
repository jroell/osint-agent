import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Search query — supports Google News operators like 'when:7d', 'when:1y', 'intitle:'. Quote phrases."),
  country: z.string().length(2).default("US").describe("ISO 2-letter country (US, GB, DE, JP, etc.)"),
  language: z.string().length(2).default("en").describe("ISO 2-letter language (en, de, ja, fr, etc.)"),
  time_filter: z.enum(["1h", "1d", "7d", "1m", "1y"]).optional().describe("Optional time-window filter (appended as 'when:X' to query)"),
  limit: z.number().int().min(1).max(100).default(50),
});

toolRegistry.register({
  name: "google_news_recent",
  description:
    "**Google News RSS query for current-events context** — free, no auth. Returns up to 100 recent articles for any query (person, org, topic). Pairs with bigquery_gdelt (historical news 2017+) for full coverage. Returns: items with title, source, source URL, pub date, description; aggregations: top sources (Reuters/NYT/Axios = mainstream vs niche-blog = specialty), unique source domains, date distribution (coverage cadence — one-time event vs sustained = different OPSEC signals). Localized via country + language for non-US/non-English markets. The query supports Google News operators: 'when:7d' (last 7 days), 'when:1y', 'intitle:', etc.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "google_news_recent", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "google_news_recent failed");
    return res.output;
  },
});
