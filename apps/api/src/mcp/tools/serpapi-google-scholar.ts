import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "author", "author_articles", "cites"]).optional(),
  query: z.string().optional(),
  author: z.string().optional().describe("Author name filter for search mode."),
  year_from: z.number().int().optional(),
  year_to: z.number().int().optional(),
  author_id: z.string().optional().describe("Google Scholar author id (e.g. 'JicYPdAAAAAJ')."),
  articles: z.boolean().optional().describe("Set true with author_id to fetch their articles."),
  cites: z.string().optional().describe("Cluster id of a paper for which to find citing papers."),
});

toolRegistry.register({
  name: "serpapi_google_scholar",
  description:
    "**Google Scholar via SerpAPI — REQUIRES SERPAPI_KEY (paid at serpapi.com/google-scholar-api).** Closes the gap between OpenAlex (open-access only) and the actual published research literature: indexes paywalled papers, theses, books, citations. Modes: search (with author/year filters), author (profile by author_id), author_articles (publications by author_id), cites (papers citing a cluster_id). Each output emits typed entity envelope (kind: scholarly_work | scholar) with stable Google Scholar IDs.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "serpapi_google_scholar",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "serpapi_google_scholar failed");
    return res.output;
  },
});
