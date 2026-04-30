import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "document", "recent_eos"])
    .optional()
    .describe(
      "search: full-text term + agency/type/date filters. document: by document_number → full record + optional raw text fetch. recent_eos: presidential executive orders feed. Auto-detects: document_number → document, query → search, else → recent_eos."
    ),
  query: z.string().optional().describe("Full-text search term (search mode)."),
  document_number: z
    .string()
    .optional()
    .describe("Federal Register document number (e.g. '2026-00206'). Required for document mode."),
  agency: z
    .string()
    .optional()
    .describe("Agency slug filter — see federalregister.gov/agencies for slug list (e.g. 'commerce-department', 'environmental-protection-agency')."),
  doc_type: z
    .string()
    .optional()
    .describe("Doc type filter (comma-separated). Values: RULE (final rules), PRORULE (proposed rules), NOTICE (agency notices/RFIs), PRESDOCU (presidential docs), OTHER (corrections, etc)."),
  start_date: z.string().optional().describe("YYYY-MM-DD lower bound on publication_date."),
  end_date: z.string().optional().describe("YYYY-MM-DD upper bound on publication_date."),
  effective_after: z.string().optional().describe("YYYY-MM-DD lower bound on effective_on date."),
  order: z
    .string()
    .optional()
    .describe("Sort order ('newest' | 'oldest' | 'relevance'). Default: newest."),
  limit: z.number().int().min(1).max(100).optional().describe("Max results (default 10 search / 20 EOs, max 100)."),
  fetch_text: z
    .boolean()
    .optional()
    .describe("In document mode: fetch raw OCR text into abstract field (default false)."),
  max_text_chars: z
    .number()
    .int()
    .min(500)
    .max(100000)
    .optional()
    .describe("Max raw-text characters when fetch_text=true (default 8000)."),
});

toolRegistry.register({
  name: "federal_register_search",
  description:
    "**Federal Register — every US federal regulation, proposed rule, agency notice, RFI, and presidential document since 1994 (free, no auth, official daily journal of the federal government).** Three modes: (1) **search** — full-text query with optional agency / doc-type (RULE/PRORULE/NOTICE/PRESDOCU/OTHER) / date filters. Each result has citation (e.g. '91 FR 698', the legally-binding reference), agencies, publication date, effective date, comment-period close date, and direct text URLs (HTML + raw text + PDF + XML); (2) **document** — by document_number → full record with optional raw OCR text fetch (up to 100k chars) for direct content reading; (3) **recent_eos** — presidential executive orders feed sorted newest first, with signing date when available. **Why this is unique ER**: the citation is the cross-reference key into court rulings (`courtlistener_search`), academic legal commentary (via `documentcloud_search` and `crossref_paper_search`), and SEC Reg-D / 10-K disclosures (`sec_edgar_search`) that cite it. Comment-period filings reveal which companies + industry groups + individuals fought a specific regulation. Tested with 'artificial intelligence' since 2026-01-01 → 90 hits including NIST AI Agents RFI (2026-01-08, comments closed 2026-03-09) and Education Dept's AI-priority rule (2026-04-13).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "federal_register_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "federal_register_search failed");
    return res.output;
  },
});
