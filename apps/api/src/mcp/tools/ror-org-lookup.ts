import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "lookup"]).default("search"),
  query: z.string().min(2).describe("Org name (search mode) or ROR ID — full URL like 'https://ror.org/00f54p054' or trailing slug '00f54p054' (lookup mode)"),
  limit: z.number().int().min(1).max(50).default(20),
});

toolRegistry.register({
  name: "ror_org_lookup",
  description:
    "**Research Organization Registry (ROR) institutional ER glue** — queries api.ror.org/v2 for canonical institutional identifiers. Free, no auth. Two modes: 'search' (fuzzy name → up to 20 candidate ROR IDs with disambiguation by location/established/types/external_ids), 'lookup' (by ROR ID → full org detail with **external IDs in 4+ namespaces** (Wikidata Q-IDs, GRID, ISNI, Funder Registry/OFR), **relationship graph** (parent/child/related/successor/predecessor — Stanford has 16+ children incl Brown Institute, Eterna lab, etc.), Wikipedia link, website, geographic location with GeoNames ID. The CRITICAL value: ROR IDs are the canonical institutional identifier used by Crossref, OpenAlex, ORCID, NIH RePORTER affiliation fields. Resolving fuzzy 'Stanford' to 'https://ror.org/00f54p054' enables high-confidence affiliation queries against the academic tools instead of name-based fuzzy matching.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "ror_org_lookup", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "ror_org_lookup failed");
    return res.output;
  },
});
