import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  contributor_name: z.string().min(2).optional().describe("Donor name — e.g. 'Jason Roell'. Fuzzy-matched by FEC."),
  committee_id: z.string().min(4).optional().describe("FEC committee ID (e.g. 'C00703975') — query by recipient committee."),
  employer: z.string().min(2).optional().describe("Donor's employer (exact substring match)."),
  state: z.string().length(2).optional().describe("2-letter US state filter (donor address)."),
  limit: z.number().int().min(1).max(100).default(30),
});

toolRegistry.register({
  name: "fec_donations_lookup",
  description:
    "**FEC political donations ER** — queries api.open.fec.gov individual contribution records. Returns donations with donor name + employer + occupation + city/state + recipient committee + amount + date. Aggregates: top employers by total amount donated (operator-portfolio signal), top recipient committees, unique occupations. Use cases: identity → employer chain (if donor disclosed), employer-of-record cross-check, political alignment of an executive, finding others at the same employer (employer mode). Free with DEMO_KEY (30 req/hr) — set FEC_API_KEY for higher limits. US persons / committees only.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "fec_donations_lookup", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "fec_donations_lookup failed");
    return res.output;
  },
});
