import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  title: z.string().min(1).describe("Wikipedia article title (CASE-SENSITIVE; use 'Anthropic' not 'anthropic'). Spaces or underscores OK."),
  wiki: z.string().default("en").describe("Wiki language code (en, de, fr, ja, etc.)"),
  days_back: z.number().int().min(1).max(365).default(30),
  granularity: z.enum(["hourly", "daily"]).default("daily"),
});

toolRegistry.register({
  name: "bigquery_wikipedia_pageviews",
  description:
    "**Cultural attention velocity** via `bigquery-public-data.wikipedia.pageviews_*`. Returns hourly or daily pageview counts for a Wikipedia article over a date window, with peak-hour identification + average-daily aggregate. Triangulates with `bigquery_trending_now` (Google Trends — search interest) and `bigquery_gdelt` (news mentions) for **3-channel cultural attention measurement**: search volume + news coverage + Wikipedia consultation. CAVEAT: titles are case-sensitive (use exact form from URL). Free via BigQuery free tier.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "bigquery_wikipedia_pageviews", input: i, timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "bigquery_wikipedia_pageviews failed");
    return res.output;
  },
});
