import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["user_profile", "user_info", "user_posts", "user_reels", "user_stories", "highlights", "post_by_url", "post_by_shortcode"])
    .optional(),
  username: z.string().optional(),
  posts: z.boolean().optional(),
  reels: z.boolean().optional(),
  stories: z.boolean().optional(),
  highlights: z.boolean().optional(),
  post_url: z.string().optional(),
  shortcode: z.string().optional(),
});

toolRegistry.register({
  name: "instagram_rapidapi",
  description:
    "**Instagram via instagram120 RapidAPI — REQUIRES RAPID_API_KEY.** Ported from vurvey-api. 8 modes covering user profile, posts, reels, stories, highlights, post-by-URL, post-by-shortcode. Each output emits typed entity envelope (kind: social_account | social_post, platform: instagram). Complements existing `instagram_user` (Apify) tool with broader coverage and a different upstream provider.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "instagram_rapidapi",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "instagram_rapidapi failed");
    return res.output;
  },
});
