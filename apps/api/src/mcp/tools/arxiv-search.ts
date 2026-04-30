import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2),
  mode: z.enum(["all", "title", "abstract", "author", "category"]).default("all"),
  sort: z.enum(["relevance", "lastUpdatedDate", "submittedDate"]).default("relevance"),
  limit: z.number().int().min(1).max(100).default(20),
});

toolRegistry.register({
  name: "arxiv_search",
  description:
    "**arxiv preprint search** — ~2.5M papers, the canonical AI/ML/physics preprint server. Modes: 'all' (all fields), 'title', 'abstract', 'author', 'category' (e.g. 'cs.AI', 'cs.LG', 'cs.CL'). Sort: relevance / lastUpdatedDate / submittedDate. Returns: title, abstract excerpt, authors, categories, primary category, PDF URL, abstract URL, DOI, journal_ref, comments. Pairs with `openalex_search` (peer-reviewed published) for full pre+post-publication research coverage. Free, no key. ~95% of AI/ML papers hit arxiv first.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "arxiv_search", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "arxiv_search failed");
    return res.output;
  },
});
