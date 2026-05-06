import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["web", "news", "images", "videos"]).optional(),
  query: z.string(),
  country: z.string().optional().describe("ISO country code (e.g. 'US', 'GB')."),
  lang: z.string().optional().describe("Search language code (e.g. 'en', 'fr')."),
  freshness: z.enum(["pd", "pw", "pm", "py"]).optional().describe("Past day/week/month/year."),
});

toolRegistry.register({
  name: "brave_search",
  description:
    "**Brave Search API — independent search index from Google/Bing/DuckDuckGo. Free tier 2k queries/mo. REQUIRES BRAVE_SEARCH_API_KEY.** 4 modes: web, news (with freshness pd/pw/pm/py), images, videos. Useful for triangulating results that other engines may rank-suppress. Each output emits typed entity envelope (kind: search_result) with stable URLs and search-engine attribution.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "brave_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "brave_search failed");
    return res.output;
  },
});
