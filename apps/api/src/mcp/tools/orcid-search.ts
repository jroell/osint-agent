import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "profile", "works"])
    .optional()
    .describe(
      "search: name/affiliation/keyword → ORCID candidates. profile: by ORCID iD → comprehensive record. works: by ORCID iD → published papers. Auto-detects: orcid present + works=true → works, orcid present → profile, else → search."
    ),
  given_name: z.string().optional().describe("First name (search mode)."),
  family_name: z.string().optional().describe("Last name (search mode)."),
  affiliation: z.string().optional().describe("Affiliation org name substring (search mode)."),
  keywords: z.string().optional().describe("Keyword/topic substring (search mode)."),
  query: z.string().optional().describe("Raw Lucene-style ORCID query (advanced)."),
  orcid: z.string().optional().describe("16-digit ORCID iD with hyphens (e.g. '0000-0002-1992-2684'). Required for profile/works modes."),
  works: z.boolean().optional().describe("If true with orcid present, fetches works mode (papers list)."),
  limit: z.number().int().min(1).max(200).optional().describe("Max results."),
});

toolRegistry.register({
  name: "orcid_search",
  description:
    "**ORCID — global researcher identifier registry, free no-auth, 18M+ researchers.** Closes the academic identity-key triad alongside ISNI (music, via `musicbrainz_search`) and GLEIF (corporate, via `gleif_lei_lookup`). ORCID iDs are accepted as canonical identity by every major publisher (Springer/Elsevier/Wiley), funder (NIH/NSF/EU H2020), and research org. Three modes: (1) **search** — by given_name + family_name + affiliation + keywords (Lucene-style query) → matching ORCID candidates. Tested 'Yann LeCun' → 0000-0002-1992-2684. (2) **profile** — by ORCID iD → comprehensive record: bio + biography excerpt + country + keywords + alternate names + researcher URLs (personal sites, lab pages, social profiles) + works/employments/educations counts + external-ID count (Scopus, Researcher ID, Loop). (3) **works** — by ORCID iD → published papers with title, type, journal, year, DOI for cross-reference into `crossref_paper_search` / `pubmed_search` / `openalex_search`.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "orcid_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "orcid_search failed");
    return res.output;
  },
});
