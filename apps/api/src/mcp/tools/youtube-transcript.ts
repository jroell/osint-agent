import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  video: z.string().min(11).describe("YouTube video — accepts video ID (11 chars), full watch URL, or youtu.be short URL"),
  language: z.string().length(2).default("en").describe("Preferred caption language (ISO-2). Falls back to first available if unavailable."),
  include_segments: z.boolean().default(true).describe("Whether to include timestamped segments (false = full_text only)"),
});

toolRegistry.register({
  name: "youtube_transcript",
  description:
    "**YouTube transcript extractor** — fetches caption tracks embedded in any public YouTube watch-page HTML and parses the timedtext JSON. Free, no auth. Returns: video metadata (title, channel, upload date, duration, view count, description excerpt), available caption tracks (with auto-generated 'asr' flag), selected-language transcript with timestamped segments + full text + word count. Strong OSINT value: makes video content (interviews, conference talks, podcasts, leaked footage, livestream recordings) text-searchable. Pairs with firecrawl_extract for downstream entity extraction from transcript text. Use cases: extract names mentioned in conference talks, verify when a person made a public claim, find specific quotes/statements from public-figure interviews. Input is video ID, watch URL, or youtu.be URL — all auto-resolve.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "youtube_transcript", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "youtube_transcript failed");
    return res.output;
  },
});
