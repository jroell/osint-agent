import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  subreddit: z.string().min(2).describe("Subreddit name (with or without 'r/' prefix)"),
  top_time_window: z.enum(["hour", "day", "week", "month", "year", "all"]).default("month"),
  post_limit: z.number().int().min(1).max(100).default(50),
});

toolRegistry.register({
  name: "reddit_subreddit_intel",
  description:
    "**Subreddit-level community recon** — fetches subreddit metadata + top + hot post listings, aggregates: top posters (community leaders), top external linked domains (curation patterns), self-post vs link-post ratio (discussion vs curation culture), avg upvote ratio (controversy detection — low = polarized, high = echo-chamber), avg score, avg comments, unique authors/domains in sample. Returns: about (subscribers, age, type — public/private/restricted/quarantined, NSFW flag, lang, public description), top posts list, hot posts list, top posters with karma totals, top domains with link counts. Note: Reddit's mod list endpoint requires authentication post-2023; tool surfaces top posters as proxy for community leaders. Use cases: community profiling, top-contributor mining, controversy detection, content-source mapping. Free, no auth.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "reddit_subreddit_intel", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "reddit_subreddit_intel failed");
    return res.output;
  },
});
