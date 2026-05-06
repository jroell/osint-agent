import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["user_profile", "user_videos", "video_info", "challenge_info", "challenge_posts"])
    .optional(),
  username: z.string().optional().describe("TikTok username without @."),
  videos: z.boolean().optional().describe("If true with username, fetches recent videos."),
  limit: z.number().int().min(1).max(100).optional(),
  video_url: z.string().optional().describe("Full TikTok video URL for video_info."),
  challenge_name: z.string().optional().describe("Hashtag/challenge name for challenge_info."),
  challenge_id: z.string().optional().describe("Challenge id for challenge_posts."),
  count: z.number().int().min(1).max(100).optional(),
});

toolRegistry.register({
  name: "tiktok_lookup",
  description:
    "**TikTok via tiktok-scraper7 RapidAPI — REQUIRES RAPID_API_KEY (subscribe at rapidapi.com/tikwm-tikwm-default/api/tiktok-scraper7).** 5 modes: user_profile (followers/hearts/videos), user_videos (recent uploads), video_info (URL → full metadata), challenge_info (hashtag), challenge_posts (top videos in a challenge). Each output emits typed entity envelope (kind: social_account | social_post, platform: tiktok). Closes the TikTok OSINT gap entirely (previously absent from the catalog).",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "tiktok_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "tiktok_lookup failed");
    return res.output;
  },
});
