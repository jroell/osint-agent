import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Brand, keyword, or phrase to search across all of Reddit"),
  time_range: z.enum(["hour", "day", "week", "month", "year", "all"]).default("year"),
  limit: z.number().int().min(1).max(500).default(100),
});

toolRegistry.register({
  name: "reddit_org_intel",
  description:
    "**Comprehensive Reddit recon for a brand/keyword** — searches all of Reddit via free public JSON API, dual sort (top by engagement + most recent) merged + deduped. Aggregates by subreddit (where is X discussed?) and author (who frequently posts about X?). Returns: top posts by engagement, recent posts, top discussing subreddits with mention counts + total scores, top authors mentioning with post counts + total scores + subreddits posted to, sentiment indicators (upvote ratio, comment density). Use cases: brand monitoring, threat actor research, recruiting intel (SMEs in niche topics), competitive feedback mining. Free, no key.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "reddit_org_intel", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "reddit_org_intel failed");
    return res.output;
  },
});
