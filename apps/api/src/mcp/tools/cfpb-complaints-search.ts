import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "complaint_detail"])
    .optional()
    .describe(
      "search: filter the 14.8M complaint corpus. complaint_detail: by complaint_id → full record. Auto-detects: complaint_id present → complaint_detail, else → search."
    ),
  company: z
    .string()
    .optional()
    .describe("Exact company name match (CFPB-canonical, often UPPERCASE with '& COMPANY' suffix). For fuzzy match use search_term."),
  search_term: z.string().optional().describe("Full-text search across all complaint fields including narrative."),
  query: z.string().optional().describe("Generic query → maps to search_term if no other filter set."),
  product: z
    .string()
    .optional()
    .describe("Product category (e.g. 'Credit reporting or other personal consumer reports', 'Credit card', 'Mortgage', 'Debt collection', 'Money transfer, virtual currency, or money service', 'Checking or savings account')."),
  sub_product: z.string().optional().describe("Sub-product narrowing (e.g. 'Credit reporting', 'General-purpose credit card or charge card')."),
  issue: z.string().optional().describe("Issue (e.g. 'Improper use of your report', 'Attempts to collect debt not owed', 'Identity theft protection')."),
  state: z.string().optional().describe("US state 2-letter code filter."),
  date_received_min: z.string().optional().describe("YYYY-MM-DD lower bound on date received."),
  date_received_max: z.string().optional().describe("YYYY-MM-DD upper bound on date received."),
  submitted_via: z.string().optional().describe("Channel: 'Web' | 'Phone' | 'Postal mail' | 'Email' | 'Fax' | 'Referral'."),
  has_narrative: z.boolean().optional().describe("If true, only complaints with consumer narrative."),
  company_response: z
    .string()
    .optional()
    .describe("Company response: 'Closed with explanation' | 'Closed with monetary relief' | 'Closed with non-monetary relief' | 'Untimely response' | 'In progress'."),
  timely: z.enum(["Yes", "No"]).optional().describe("Whether company responded within 15 days."),
  tags: z.string().optional().describe("Demographic tag: 'Servicemember', 'Older American', or both (comma-separated)."),
  complaint_id: z.union([z.string(), z.number()]).optional().describe("Numeric complaint ID for complaint_detail mode."),
  sort: z
    .string()
    .optional()
    .describe("Sort order: 'created_date_desc' (default), 'created_date_asc', 'relevance_desc'."),
  limit: z.number().int().min(1).max(100).optional().describe("Max results (default 25)."),
});

toolRegistry.register({
  name: "cfpb_complaints_search",
  description:
    "**Consumer Financial Protection Bureau public complaints database — every complaint filed against a US financial-services company since 2011, free no-auth, ~14.8M records.** No other catalog tool covers consumer-facing financial complaints. **Why this is unique ER**: each complaint includes the company, product/sub-product/issue/sub-issue taxonomy, state + ZIP, optional consumer free-text narrative, company response category ('Closed with monetary relief' = good outcome, 'Closed with explanation' = brush-off), timeliness flag, and demographic tags ('Servicemember', 'Older American'). Two modes: (1) **search** with rich filters (company exact match, search_term full-text, product/issue taxonomy, state, date range, response type, timely flag, demographic tags) — returns matching complaints PLUS client-side aggregations across the result set (by_product, by_issue, by_state, by_company_response, narrative count, untimely count). Tested with Wells Fargo → 165,938 complaints (latest 2026-04-23 debt collection in CA, 2026-04-22 credit card customer service in CT). (2) **complaint_detail** — by complaint_id. Pairs with `sec_edgar_search` (material consumer harm in 10-K), `documentcloud_search` (CFPB enforcement actions), `propublica_nonprofit` (consumer-advocacy groups).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "cfpb_complaints_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "cfpb_complaints_search failed");
    return res.output;
  },
});
