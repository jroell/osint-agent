import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["opinions", "dockets"]).default("opinions").describe("'opinions' = decided cases with published rulings; 'dockets' = active+historical RECAP filings (better for ongoing litigation)"),
  query: z.string().min(2).describe("Party name (e.g. 'Sam Bankman-Fried'), case keyword, or docket number"),
  court: z.string().optional().describe("Court ID filter — e.g. 'scotus' (Supreme Court), 'cand' (N.D. Cal), 'nysd' (S.D.N.Y.), 'flsd' (S.D. Fla), 'deb' (Delaware Bankruptcy)"),
  limit: z.number().int().min(1).max(100).default(25),
});

toolRegistry.register({
  name: "courtlistener_search",
  description:
    "**Federal court records ER** — queries CourtListener (free.law's ~5M federal court opinions + RECAP docket archive). Two modes: 'opinions' (decided cases with published rulings — case law trail), 'dockets' (active and historical PACER filings — pending litigation). Returns: case name, court, filing date, docket number, citations, judge, snippet. Aggregates: top courts by case count, unique docket numbers, year range. Use cases: identity → litigation history, finding bankruptcy creditors, securities defendants, FOIA requestors, criminal defendants. Strong ER signal — being named in a federal docket is a hard identity link to the named individual + their attorneys + venue. Free without auth (lower rate limit) — set COURTLISTENER_TOKEN for higher limits.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "courtlistener_search", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "courtlistener_search failed");
    return res.output;
  },
});
