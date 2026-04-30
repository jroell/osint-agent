import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["condition", "sponsor", "pi_name", "text", "nct_lookup"]).default("condition"),
  query: z.string().min(2).describe("Disease/condition (condition mode), sponsor name (sponsor), PI name (pi_name), keyword (text), or NCT ID like 'NCT05668741' (nct_lookup)"),
  status: z.enum(["RECRUITING", "ACTIVE_NOT_RECRUITING", "COMPLETED", "ENROLLING_BY_INVITATION", "NOT_YET_RECRUITING", "SUSPENDED", "TERMINATED", "WITHDRAWN", "UNKNOWN"]).optional().describe("Optional filter on overall trial status"),
  limit: z.number().int().min(1).max(100).default(25),
});

toolRegistry.register({
  name: "clinicaltrials_search",
  description:
    "**ClinicalTrials.gov registry search** — queries the public v2 API (~480K registered trials since 2000). Distinct from pubmed_search (publications) and nih_reporter_search (grants). 5 modes: condition (disease), sponsor (drug company / institution), pi_name (uses AREA[OverallOfficialName] filter), text (general), nct_lookup (direct lookup by NCT ID like 'NCT05668741'). Returns full study metadata: brief title + official title + sponsor + status + start/completion dates + phases + study type + enrollment + conditions + interventions + overall officials with per-trial affiliation snapshot + multi-site location list + brief summary. Aggregations: top sponsors (pharma vs academic vs gov), top PIs (most-active researchers), top conditions, status breakdown, multi-country footprint, total enrollment. Use cases: trace researcher's trial portfolio, find pharma sponsorship of drug class, multi-site collaborator networks. Free, no auth.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "clinicaltrials_search", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "clinicaltrials_search failed");
    return res.output;
  },
});
