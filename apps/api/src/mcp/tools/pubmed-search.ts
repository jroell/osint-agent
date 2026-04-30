import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "author_search", "pmid_lookup"]).default("search"),
  query: z.string().min(2).describe("Keyword query (search), author name like 'June CH' (author_search), or comma-separated PMIDs (pmid_lookup)"),
  limit: z.number().int().min(1).max(100).default(25),
});

toolRegistry.register({
  name: "pubmed_search",
  description:
    "**PubMed E-utilities biomed paper search** — queries NCBI's free public API (~37M papers). 3 modes: 'search' (keyword across titles + abstracts), 'author_search' (author name in 'Last Initials' format like 'June CH' → all their papers), 'pmid_lookup' (full metadata for specific PMIDs comma-separated). Returns full author lists with **per-author ORCID + per-paper affiliation** (temporal employer trail like IETF datatracker), abstract, MeSH terms (controlled-vocabulary classification), DOI, journal, year. Aggregations: unique affiliations across papers, unique ORCIDs (cross-paper hard ER), top authors, top MeSH terms (research-topic profile), year range. Direct complement to NIH RePORTER (grants), Crossref (DOIs), OpenAlex (citations) for full biomed researcher graph. Free, no auth (3 req/s rate limit).",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "pubmed_search", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "pubmed_search failed");
    return res.output;
  },
});
