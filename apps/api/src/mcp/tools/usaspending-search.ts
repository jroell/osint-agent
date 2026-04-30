import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search_awards", "award_detail"])
    .optional()
    .describe(
      "search_awards: filter contracts/grants. award_detail: by generated_internal_id → full record. Auto-detects: award_id present → award_detail, else → search_awards."
    ),
  recipient: z
    .string()
    .optional()
    .describe("Recipient name fuzzy match (e.g. 'Anthropic', 'MIT', 'Lockheed Martin', 'Pfizer')."),
  keyword: z.string().optional().describe("Free-text keyword in award description (e.g. 'AI', 'cybersecurity', 'COVID-19')."),
  agency: z.string().optional().describe("Awarding agency top-tier name (e.g. 'Department of Defense', 'Department of State', 'NASA')."),
  award_type_codes: z
    .array(z.string())
    .optional()
    .describe(
      "Award type codes (default: ['A','B','C','D'] = contracts). 02/03/04/05 = grants. 07/08 = loans. 06/09/10/11 = direct payments / other financial assistance."
    ),
  start_date: z.string().optional().describe("YYYY-MM-DD lower bound on action date (default 2020-01-01)."),
  end_date: z.string().optional().describe("YYYY-MM-DD upper bound (default today)."),
  award_id: z.string().optional().describe("Generated internal ID (e.g. 'CONT_AWD_19PCRD26K4661_1900_-NONE-_-NONE-') for award_detail mode."),
  limit: z.number().int().min(1).max(100).optional().describe("Max results (default 25)."),
});

toolRegistry.register({
  name: "usaspending_search",
  description:
    "**USAspending.gov — every US federal contract, grant, loan, and direct payment since 2008. Free, no auth.** Closes the federal political-OSINT chain: GovTrack (bills) → Federal Register (regs) → LDA (lobbying) → **USASpending (who got paid by the government)** → SEC EDGAR (publicly-traded recipient corp filings) → CFPB (consumer harm if recipient mishandled). Two modes: (1) **search_awards** — by recipient/agency/keyword/date/award-type filters. Returns awards with recipient + agency + sub-agency + description + amount + award type + URL. **Includes client-side aggregations**: total amount in result set, top recipients by $, top awarding agencies by $. Tested with `recipient=Anthropic` → State Department contract 19PCRD26K4661 for 'CLAUDE AI' ($18,960) — proof the federal government has procured Claude. (2) **award_detail** — by generated_internal_id → full record with recipient + agency hierarchy + period of performance. Award type codes: A/B/C/D = contracts (default), 02/03/04/05 = grants, 07/08 = loans, 06/09/10/11 = direct payments.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "usaspending_search",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "usaspending_search failed");
    return res.output;
  },
});
