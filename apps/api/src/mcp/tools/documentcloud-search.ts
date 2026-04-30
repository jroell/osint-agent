import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "document"])
    .optional()
    .describe(
      "search: full-text query → matching documents with metadata. document: by document_id → full metadata + OCR text excerpt. Auto-detects: document_id present → document, else → search."
    ),
  query: z
    .string()
    .optional()
    .describe("Full-text search query (any keywords). Required for search mode."),
  document_id: z
    .union([z.string(), z.number()])
    .optional()
    .describe("DocumentCloud document ID (numeric or string). Required for document mode."),
  organization: z
    .string()
    .optional()
    .describe("Filter by uploader organization name (e.g. 'New York Times', 'ProPublica', 'Reuters')."),
  source: z
    .string()
    .optional()
    .describe("Filter by document source field (often the original publisher/jurisdiction)."),
  language: z
    .string()
    .optional()
    .describe("ISO 639-2 language code (e.g. 'eng', 'spa', 'fra'). Default: all."),
  start_date: z.string().optional().describe("YYYY-MM-DD lower bound on created_at."),
  end_date: z.string().optional().describe("YYYY-MM-DD upper bound on created_at."),
  order: z
    .string()
    .optional()
    .describe("Sort key (e.g. '-created_at' for newest first, 'title' for alphabetical, blank for relevance)."),
  limit: z.number().int().min(1).max(25).optional().describe("Search result limit (default 10, max 25)."),
  fetch_text: z
    .boolean()
    .optional()
    .describe("In document mode: fetch OCR text excerpt (default true)."),
  max_text_chars: z
    .number()
    .int()
    .min(500)
    .max(100000)
    .optional()
    .describe("Max OCR text characters returned in document mode (default 8000)."),
});

toolRegistry.register({
  name: "documentcloud_search",
  description:
    "**DocumentCloud — investigative-journalism document repository (~3M docs, free no-auth, OCR-searchable).** Run by MuckRock + UC Berkeley Investigative Reporting Program. Newsrooms (NYT, WaPo, ProPublica, Reuters, Bloomberg, regional papers) upload primary-source documents — legal filings, court records, FOIA responses, leaked memos, regulatory submissions, public comments, government reports — and OCR them publicly. **Why this is unique ER**: journalists do the gruntwork of getting documents out from behind PACER paywalls and regulatory portals. Search for `Anthropic` returns 467 docs including Anthropic PBC's Oct 2023 Copyright Office public comment signed by Janel Thamkul (Deputy General Counsel). That's executive name + title + employer + dated artifact in one query. Two modes: **search** (full-text query → docs with title, page count, contributor + uploader org, canonical URL, full_text_url for direct OCR fetch — filterable by organization, source, language, date), **document** (by ID → full metadata + first N chars of OCR text directly readable). Pairs with `sec_edgar_search` (regulatory filings), `courtlistener_search` (court records), `propublica_nonprofit` (501(c) filings).",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "documentcloud_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "documentcloud_search failed");
    return res.output;
  },
});
