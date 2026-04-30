import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

// =============================================================================
// gemini_search_grounded — Gemini + Google Search tool with citations
// =============================================================================
const searchInput = z.object({
  prompt: z.string().min(2).describe("Question to answer with Google Search grounding"),
  model: z.string().default("gemini-3.1-pro-preview").describe("Gemini model — 'gemini-3.1-pro-preview' (default, deepest), 'gemini-3-flash-preview' (fast), 'gemini-2.5-flash' (cheaper)"),
});

toolRegistry.register({
  name: "gemini_search_grounded",
  description:
    "**Gemini 3.1 Pro with Google Search tool** — synthesizes web search results into a coherent answer with citations. Distinct from tavily_search/google_news_recent: Gemini issues N web searches itself, reads results, and writes a single coherent narrative answer with verifiable URLs. Returns: answer text, the search queries Gemini USED (transparency trail), and grounding URL citations. Strong for any 'what's the latest on X' / 'tell me about Y' question. REQUIRES GOOGLE_AI_API_KEY (or GEMINI_API_KEY).",
  inputSchema: searchInput,
  costMillicredits: 8,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "gemini_search_grounded", input: i, timeoutMs: 240_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "gemini_search_grounded failed");
    return res.output;
  },
});

// =============================================================================
// gemini_url_context — Gemini fetches and analyzes any URL(s)
// =============================================================================
const urlInput = z.object({
  prompt: z.string().min(2).describe("Question or extraction task for the URLs"),
  url: z.string().url().optional().describe("Single URL (or use urls array)"),
  urls: z.array(z.string().url()).optional().describe("Up to 20 URLs Gemini will fetch and analyze"),
  model: z.string().default("gemini-3.1-pro-preview"),
}).refine((d) => d.url || (d.urls && d.urls.length > 0), { message: "Either url or urls is required" });

toolRegistry.register({
  name: "gemini_url_context",
  description:
    "**Gemini with url_context tool — fetch + analyze any URLs** — pass up to 20 URLs (HTML / PDFs / public files) and a question/task; Gemini fetches them, reads, and answers. Strong for cross-document analysis: extract names from a court filing PDF, compare press releases from two companies, summarize a long article without download. Distinct from firecrawl_parse (single-doc) and firecrawl_extract (no fetch+analyze chain). REQUIRES GOOGLE_AI_API_KEY.",
  inputSchema: urlInput,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "gemini_url_context", input: i, timeoutMs: 240_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "gemini_url_context failed");
    return res.output;
  },
});

// =============================================================================
// gemini_youtube_understanding — native YouTube video Q&A
// =============================================================================
const ytInput = z.object({
  video_url: z.string().url().describe("YouTube video URL"),
  prompt: z.string().min(2).describe("Question/task — e.g. 'Summarize', 'Who is the speaker?', 'What slides appear at 10:30?'"),
  model: z.string().default("gemini-3.1-pro-preview"),
  media_resolution: z.enum(["low", "medium", "default"]).default("low").describe("'low' = up to ~5hr videos, less visual detail; 'default' = best quality, ~10min limit"),
});

toolRegistry.register({
  name: "gemini_youtube_understanding",
  description:
    "**Native YouTube video understanding via Gemini** — Gemini ingests YouTube videos directly via fileData (no transcript extraction needed). Can answer questions about: visual content (who appears, what's on screen, slides shown), audio (speaker identity, accent, music), and combined (e.g. 'who is shown in the photo at 5:30?'). Strong upgrade over youtube_transcript: gives **visual + audio + temporal reasoning** instead of just text. For long videos (>30 min), keep media_resolution=low to fit 1M token window. REQUIRES GOOGLE_AI_API_KEY.",
  inputSchema: ytInput,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "gemini_youtube_understanding", input: i, timeoutMs: 300_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "gemini_youtube_understanding failed");
    return res.output;
  },
});
