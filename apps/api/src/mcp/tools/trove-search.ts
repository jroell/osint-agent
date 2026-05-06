import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "newspaper_article"])
    .optional()
    .describe("search: full-text Trove search. newspaper_article: fetch a single article by Trove id."),
  query: z.string().optional().describe("Search query for search mode."),
  category: z.string().optional().describe("Category filter — newspaper, book, magazine, image, music, map, list, people. Default: newspaper."),
  date_from: z.string().optional().describe("Earliest year (YYYY) for date range filter."),
  state: z.string().optional().describe("Australian state filter for newspapers (NSW, Vic, Qld, SA, WA, Tas, ACT, NT, National)."),
  newspaper_title_id: z.string().optional().describe("Trove newspaper title id to restrict to a specific paper."),
  article_id: z.string().optional().describe("Trove newspaper article id for newspaper_article mode."),
});

toolRegistry.register({
  name: "trove_search",
  description:
    "**Trove (NLA Australia) — historical Australian archive, free w/ TROVE_API_KEY.** ~700M+ items including 200+ years of digitized newspapers (1803-2010s), books, magazines, images, music, maps. The dominant source for Australian/NZ biography, Indigenous history, and any AU page-of-record question. Modes: search (full-text with date+state+paper filters) and newspaper_article (full text by article id). Each result emits typed entity envelope (kind: newspaper_article | book) with stable Trove permalinks for ER chaining.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "trove_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "trove_search failed");
    return res.output;
  },
});
