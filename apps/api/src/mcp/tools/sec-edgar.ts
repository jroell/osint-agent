import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(1).describe("Stock ticker (e.g. 'AAPL') or numeric CIK ('320193')"),
  limit: z.number().int().min(1).max(500).default(50),
  form: z.string().optional().describe("Optional form-type filter (e.g. '10-K', '8-K', 'DEF 14A')"),
});

toolRegistry.register({
  name: "sec_edgar_filing_search",
  description:
    "Look up SEC EDGAR filings for a public company by ticker or CIK. Returns recent filings with form type, filing date, and direct URL to the primary document. Free, no API key. Source: data.sec.gov.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "sec_edgar_filing_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "sec_edgar_filing_search failed");
    return res.output;
  },
});
