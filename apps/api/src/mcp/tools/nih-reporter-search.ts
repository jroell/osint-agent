import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["pi_name", "org_name", "text", "project_num"]).default("pi_name").describe("'pi_name' = grants for a person; 'org_name' = grants at an institution; 'text' = full-text title/abstract search; 'project_num' = lookup by grant ID"),
  query: z.string().min(2).describe("PI name (e.g. 'Carl June'), institution (e.g. 'Stanford University'), keyword, or project number (e.g. '5R01CA123456-04')"),
  limit: z.number().int().min(1).max(500).default(50),
});

toolRegistry.register({
  name: "nih_reporter_search",
  description:
    "**NIH grant ER via federal funding records** — queries NIH RePORTER (every NIH-funded grant since 1985: ~3M grants, $300B+ in cumulative funding). Free, no auth. Modes: pi_name, org_name, text, project_num. Returns: grants list (project number, title, PIs, institution, fiscal year, award amount, agency, mechanism, project dates), aggregations (top PIs by grant count + total funding, top organizations with year range = institutional history, unique NIH profile_ids = hard cross-grant identity signal that resolves name variants like 'Carl June' vs 'CARL H. JUNE'). Use cases: biomedical researcher career trace, institutional moves, co-investigator network mapping, grant size → seniority assessment, namesake disambiguation via profile_id. Pairs with crossref/openalex (publications) and bigquery_patents (commercial IP) for the full biomedical researcher graph.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "nih_reporter_search", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "nih_reporter_search failed");
    return res.output;
  },
});
