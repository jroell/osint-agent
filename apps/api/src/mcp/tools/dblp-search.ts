import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["author_search", "author_profile", "publication_search"]).default("author_search"),
  query: z.string().min(2).describe("Author name (author_*) or paper keyword (publication_search). For author_profile mode, also accepts a DBLP PID directly (e.g. 'l/YannLeCun')."),
  limit: z.number().int().min(1).max(100).default(25),
});

toolRegistry.register({
  name: "dblp_search",
  description:
    "**DBLP CS publication index ER** — queries dblp.org, the canonical Computer Science publication database (~6M papers, more comprehensive for CS than Crossref/OpenAlex which lean biomed). Free, no auth. 3 modes: 'author_search' (fuzzy name → DBLP PID + aliases + affiliation notes — namesake disambiguation), 'author_profile' (resolved by PID OR name → full publication list + **CROSS-PLATFORM IDENTITY LINKS panel** with classified URLs to ORCID/Twitter/Google Scholar/Wikipedia/Wikidata/OpenReview/ACM/MathSciNet/GitHub/HuggingFace/LinkedIn/ResearchGate, top venues = research subfield signal, top coauthors = collaboration network, year range), 'publication_search' (full-text title/keyword across all CS papers). The cross-platform-links panel is the unique value: a single DBLP fetch reveals up to 10+ external identity systems for one researcher, making DBLP the natural pivot point in academic ER chains.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "dblp_search", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "dblp_search failed");
    return res.output;
  },
});
