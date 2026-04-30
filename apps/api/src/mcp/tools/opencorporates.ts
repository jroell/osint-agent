import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Company name to search"),
  jurisdiction_code: z.string().optional().describe("Optional ISO jurisdiction (e.g. 'us_de' for Delaware, 'gb' for UK)"),
  page: z.number().int().min(1).default(1),
  per_page: z.number().int().min(1).max(100).default(30),
});

toolRegistry.register({
  name: "opencorporates_search",
  description:
    "Search the OpenCorporates database (~200M companies across 140 jurisdictions) for matching company entities. REQUIRES OPENCORPORATES_API_KEY env var (the unauthenticated tier was retired in 2024; free trial available at https://opencorporates.com/api_accounts/new).",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "opencorporates_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "opencorporates_search failed");
    return res.output;
  },
});
