import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["user_details", "user_tweets", "search", "tweet_details", "hashtag", "geo_search"])
    .optional(),
  username: z.string().optional(),
  tweets: z.boolean().optional().describe("If true with username, fetches recent tweets."),
  query: z.string().optional().describe("Search keyword for search mode."),
  section: z.enum(["top", "latest", "people", "photos", "videos"]).optional(),
  language: z.string().length(2).optional().describe("ISO-2 language filter."),
  limit: z.number().int().min(1).max(100).optional(),
  tweet_id: z.string().optional(),
  hashtag: z.string().optional(),
  latitude: z.number().optional(),
  longitude: z.number().optional(),
  radius_km: z.number().optional(),
});

toolRegistry.register({
  name: "twitter_rapidapi",
  description:
    "**Twitter via twitter154 RapidAPI — REQUIRES RAPID_API_KEY. Cheaper alternative to X API v2 Premium ($100+/mo).** 6 modes: user_details (profile), user_tweets (recent), search (keyword/hashtag with section/language filters), tweet_details (single tweet by id), hashtag (top tweets for #tag), geo_search (lat/lng + radius). Each output emits typed entity envelope (kind: social_account | social_post, platform: twitter). Pairs with the existing free `twitter_user` (X API v2) and `grok_x_search` tools.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "twitter_rapidapi",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "twitter_rapidapi failed");
    return res.output;
  },
});
