import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search_organizations", "organization_details", "search_people", "person_details", "funding_rounds"])
    .optional(),
  query: z.string().optional(),
  who: z.enum(["org", "person"]).optional(),
  org_permalink: z.string().optional().describe("Crunchbase organization permalink (e.g. 'anthropic')."),
  person_permalink: z.string().optional().describe("Crunchbase person permalink."),
  funding: z.boolean().optional().describe("Set true with org_permalink to list funding rounds."),
});

toolRegistry.register({
  name: "crunchbase_lookup",
  description:
    "**Crunchbase Basic v4 — startup funding rounds, founder/investor relationships, acquisitions, exec histories. REQUIRES CRUNCHBASE_API_KEY ($1k+/mo).** 5 modes: search_organizations, organization_details, search_people, person_details, funding_rounds (per-org). Each output emits typed entity envelope (kind: organization | person | funding_round) with stable Crunchbase permalinks. Closes the 'company X CEO at acquisition + spouse Columbia MBA' OSINT chain class.",
  inputSchema: input,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "crunchbase_lookup",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "crunchbase_lookup failed");
    return res.output;
  },
});
