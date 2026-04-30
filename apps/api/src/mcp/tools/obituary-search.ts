import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  name: z.string().min(2).describe("Deceased person's name"),
  location: z.string().optional().describe("Optional city/state filter (e.g. 'Cincinnati Ohio')"),
  year_range: z.string().optional().describe("Optional year-range hint (e.g. '2010-2020')"),
});

toolRegistry.register({
  name: "obituary_search",
  description:
    "**Multi-source obituary search with relative-list parsing** — closes the family-tree OSINT gap. Searches newspapers.com (paywalled but Google-indexed), legacy.com (largest free obituary aggregator), tributearchive.com, dignitymemorial.com via the Tavily-bypass infrastructure. Parses snippets for: deceased name, age at death, birth/death dates, city/state, funeral home (geographic ER signal — e.g. 'Hodapp Funeral Home' → Cincinnati OH), surviving spouse + children + parents + siblings + grandchildren, and predeceased relatives. Aggregates relatives across all sources with mention counts (cross-source agreement = high-confidence family graph). Use cases: find someone's relatives via a deceased family member, build family-tree-of-deceased for genealogy, find next-of-kin for a missing person investigation. Closes the FIL-benchmark gap from iter-36 by enumerating relatives explicitly. REQUIRES TAVILY_API_KEY.",
  inputSchema: input,
  costMillicredits: 8,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "obituary_search", input: i, timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "obituary_search failed");
    return res.output;
  },
});
