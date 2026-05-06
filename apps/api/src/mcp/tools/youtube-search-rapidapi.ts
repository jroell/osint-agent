import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "video_details", "video_comments", "channel_info", "channel_videos", "trending"])
    .optional(),
  query: z.string().optional().describe("Search keyword."),
  type: z.enum(["video", "channel", "playlist"]).optional(),
  video_id: z.string().optional(),
  channel_id: z.string().optional(),
  videos: z.boolean().optional().describe("If true with channel_id, fetches channel uploads."),
  comments: z.boolean().optional().describe("If true with video_id, fetches comments."),
  geo: z.string().optional().describe("ISO country code for trending."),
});

toolRegistry.register({
  name: "youtube_search_rapidapi",
  description:
    "**YouTube discovery via yt-api RapidAPI — REQUIRES RAPID_API_KEY.** Complements free `youtube_transcript` (caption fetch) with discovery + metadata. 6 modes: search (query + type), video_details (id → full metadata), video_comments (id → top comments), channel_info, channel_videos, trending (per-country). Each output emits typed entity envelope (kind: video, platform: youtube) with stable video IDs.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "youtube_search_rapidapi",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "youtube_search_rapidapi failed");
    return res.output;
  },
});
