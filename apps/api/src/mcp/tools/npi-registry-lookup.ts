import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "by_npi", "org_search"])
    .optional()
    .describe(
      "search: by first/last name + optional state/city/specialty. by_npi: direct 10-digit NPI. org_search: organization name → all NPI-2 records. Auto-detects: npi present → by_npi, organization → org_search, else → search."
    ),
  first_name: z.string().optional().describe("Provider first name (search mode)."),
  last_name: z.string().optional().describe("Provider last name (search mode)."),
  npi: z.string().optional().describe("10-digit NPI for direct lookup."),
  organization: z.string().optional().describe("Healthcare organization name (org_search mode)."),
  state: z.string().optional().describe("US state 2-letter code filter (e.g. 'OH', 'CA')."),
  city: z.string().optional().describe("City name filter."),
  postal_code: z.string().optional().describe("ZIP/postal code filter."),
  taxonomy_description: z
    .string()
    .optional()
    .describe("Specialty filter — partial match against taxonomy description (e.g. 'Cardiology', 'Pediatrics', 'Family Medicine', 'Neurological Surgery')."),
  enumeration_type: z
    .enum(["NPI-1", "NPI-2"])
    .optional()
    .describe("Limit to NPI-1 (individual provider, default for search) or NPI-2 (organization, default for org_search)."),
  limit: z.number().int().min(1).max(200).optional().describe("Max results (default 20, max 200)."),
});

toolRegistry.register({
  name: "npi_registry_lookup",
  description:
    "**CMS National Provider Identifier registry — every US healthcare provider + organization, free no-auth, 2.5M+ individual + 1M+ organizational records.** Every doctor, dentist, NP, PA, physical therapist, pharmacist, hospital, clinic, pharmacy in the US has a 10-digit HIPAA-mandated NPI. Three modes: (1) **search** — by first/last name + optional state/city/postal/specialty, returns providers with full metadata; (2) **by_npi** — direct 10-digit NPI → full record (active/inactive status, enumeration date, all addresses with phone+fax, all specialties with state license numbers, gender, sole-proprietor flag, alternate names); (3) **org_search** — organization name (e.g. 'Cleveland Clinic') → all affiliated NPI-2 records with authorized official + title. **Why this is unique ER**: state license numbers cross-check medical board records and malpractice databases — none of which are in generic people-search. Active/inactive flag is the single best 'currently practicing' signal. Tested with 'Sanjay Gupta in GA' → returned NPI 1760499529, M.D., Neurological Surgery, GA license #050521, Emory faculty office address with phone/fax. Pairs with `documentcloud_search` (medical-malpractice court docs), `propublica_nonprofit` (nonprofit hospital filings), `sec_edgar_search` (publicly-traded healthcare cos).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "npi_registry_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "npi_registry_lookup failed");
    return res.output;
  },
});
