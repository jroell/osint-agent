import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["lookup_doi", "recent_preprints"])
    .optional()
    .describe(
      "lookup_doi: DOI → full paper. recent_preprints: date range + optional category → preprints. Auto-detects: doi → lookup_doi, else → recent_preprints."
    ),
  server: z
    .enum(["biorxiv", "medrxiv"])
    .optional()
    .describe("Which preprint server (default biorxiv = life sciences; medrxiv = clinical/health policy)."),
  doi: z
    .string()
    .optional()
    .describe("DOI for lookup_doi mode (e.g. '10.1101/2024.01.15.575735'). Strips doi: prefix and URL automatically."),
  start_date: z.string().optional().describe("YYYY-MM-DD lower bound on posting date (recent_preprints mode)."),
  end_date: z.string().optional().describe("YYYY-MM-DD upper bound (recent_preprints mode)."),
  category: z
    .string()
    .optional()
    .describe(
      "Category substring filter (e.g. 'microbiology', 'neuroscience', 'epidemiology', 'genetics', 'cell biology', 'bioinformatics'). Case-insensitive."
    ),
  limit: z.number().int().min(1).max(500).optional().describe("Max results for recent_preprints (default 100, paginated up to 500)."),
});

toolRegistry.register({
  name: "biorxiv_search",
  description:
    "**bioRxiv / medRxiv biomedical preprint search — free no-auth.** Closes the academic chain alongside `pubmed_search` (peer-reviewed PubMed/MEDLINE), `arxiv_search` (CS/physics/math), `crossref_paper_search` (DOI-indexed), `openalex_search` (citation graph). **Why preprints matter for OSINT**: this is where biomedical research breaks first — typically 6-18 months before peer-reviewed publication. New outbreak surveillance, vaccine candidates, drug-trial results, epidemiological updates appear here weeks before they hit PubMed. Two modes: (1) **lookup_doi** — DOI → paper detail (title, authors, category, posting date, version, abstract, JATS XML URL for full-text access, license, and **published_journal/published_doi if the preprint has since been peer-reviewed and published**); (2) **recent_preprints** — date range + optional category filter → list of preprints with category breakdown. server=biorxiv (life sciences default) or medrxiv (clinical / health policy).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "biorxiv_search",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "biorxiv_search failed");
    return res.output;
  },
});
