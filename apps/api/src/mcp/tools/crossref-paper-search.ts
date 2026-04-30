import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["query", "author", "orcid"]).default("query"),
  query: z.string().min(2).describe("Keyword (query mode), researcher name (author mode), or ORCID 0000-0000-0000-0000 (orcid mode)"),
  limit: z.number().int().min(1).max(100).default(20),
});

toolRegistry.register({
  name: "crossref_paper_search",
  description:
    "**Crossref scholarly works ER** — queries api.crossref.org (~140M+ DOI-registered works across all academic publishers). Free, no key (uses polite-pool User-Agent). Three modes: 'query' (full-text-ish keyword), 'author' (name → returns matches across multiple distinct people sharing a name; built-in namesake disambiguation), 'orcid' (filter by exact ORCID iD — every work that ORCID is on). Aggregations: top papers by citation count, co-author network with ORCIDs (hard cross-paper ER signal), unique affiliations (employer-of-record at publication time), unique publishers, subjects, year range. Pairs with arxiv_search (preprints) + openalex_search (citations + h-index) + bigquery_patents (commercial IP) for full academic identity-graph traversal.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "crossref_paper_search", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "crossref_paper_search failed");
    return res.output;
  },
});
