import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["lookup_company", "company_filings", "full_text_search"])
    .optional()
    .describe(
      "lookup_company: name/ticker → CIK + full company metadata. company_filings: list filings for a CIK (with optional form/date filters). full_text_search: keyword search across all SEC filings since 2001. If omitted, auto-detects: cik present → company_filings, forms present → full_text_search, otherwise → lookup_company."
    ),
  query: z
    .string()
    .optional()
    .describe(
      "For lookup_company: company name or ticker. For full_text_search: free-text keyword (the SEC wraps in quotes for exact match). For company_filings: ticker as fallback if cik is omitted."
    ),
  cik: z
    .string()
    .optional()
    .describe(
      "10-digit SEC CIK (zero-padded — e.g. '0000320193' for Apple). Required for company_filings unless query/ticker is provided."
    ),
  ciks: z
    .string()
    .optional()
    .describe(
      "Comma-separated CIKs to scope full_text_search to specific companies (e.g. '0000320193,0001318605')."
    ),
  forms: z
    .string()
    .optional()
    .describe(
      "Comma-separated SEC form types to filter by. Examples: '4' (insider transactions), '3,4,5' (all insider forms), 'SC 13D,SC 13G' (5%+ beneficial owners), '13F-HR' (institutional holdings), '8-K' (material events), '10-K,10-Q' (financials), 'DEF 14A' (proxy statements with exec comp + board), 'D' (Reg D private placements), 'S-1' (IPO registrations)."
    ),
  start_date: z.string().optional().describe("YYYY-MM-DD lower bound on filing date."),
  end_date: z.string().optional().describe("YYYY-MM-DD upper bound on filing date."),
  limit: z
    .number()
    .int()
    .min(1)
    .max(200)
    .optional()
    .describe("Max rows to return (default 50 for filings, 30 for FTS, 5 for lookup)."),
});

toolRegistry.register({
  name: "sec_edgar_search",
  description:
    "**SEC EDGAR — full search across all 30M+ U.S. SEC filings since 2001 (free, no auth).** Three modes: (1) **lookup_company** — fuzzy ticker/name → CIK + business address + mailing address + SIC industry + exchange + ticker list + former names + state-of-incorporation + entity type; (2) **company_filings** — by CIK, list recent filings filterable by form (Form 4 insider transactions, SC 13D/G beneficial owners >5%, 13F-HR institutional positions, 8-K material events, 10-K/Q financials, DEF 14A proxy statements with executive comp, D Reg-D private placements, S-1 IPO registrations) and date range — highlights group results by form and surface insider transactions / 5%+ holders / material events specifically; (3) **full_text_search** — keyword search across all filings since 2001 with optional CIK + form + date scoping. **Why this is essential ER**: insider Form 4s reveal who's selling stock right before/after material events, 13D/G filings name everyone with >5% control, 8-K item codes flag executive departures (item 5.02) and M&A (items 1.01/2.01) before press cycles. Pairs with `wikidata_lookup` (which has Wikidata-tagged corp identifiers via P1278/P249/P946 but ~5% coverage) and `propublica_nonprofit` (501(c) filings only).",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "sec_edgar_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "sec_edgar_search failed");
    return res.output;
  },
});
