import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2),
  limit: z.number().int().min(1).max(1000).default(50),
  tags: z.string().optional().describe("Algolia tags filter, e.g. '(story,comment)' or 'story' or 'show_hn'"),
  mode: z.enum(["search", "search_by_date"]).default("search").describe("'search' = relevance ranking; 'search_by_date' = recency"),
});

toolRegistry.register({
  name: "hackernews_search",
  description:
    "**Tech-OSINT goldmine** — Algolia-powered HN full-text search across all stories + comments. Returns: total hit count, top stories by points+comments engagement, most recent posts, top authors mentioning the term (potential SMEs/founders/critics), story vs comment counts. HN is uniquely valuable because comments often reveal insider info absent from posts (e.g. 'I worked at X and can confirm...'). Use cases: tech-stack reconnaissance, founder discovery, product launch monitoring, threat-tool surfacing (security researchers post finds on HN). Free, no auth. Pairs with `hackernews_user` (per-user history) and `reddit_org_intel` (broader social).",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "hackernews_search", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "hackernews_search failed");
    return res.output;
  },
});
