import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  file_url: z.string().url().describe("URL of PDF/DOCX/DOC/ODT/RTF/XLSX/XLS/HTML to parse (max 50MB)"),
  structured_prompt: z.string().min(2).optional().describe("Optional natural-language extraction prompt — combines parse + LLM extract in one call"),
  include_summary: z.boolean().default(false).describe("Also include AI-generated summary"),
});

toolRegistry.register({
  name: "firecrawl_parse",
  description:
    "**Document parser via Fire-PDF (Rust + neural layout)** — Firecrawl's `/v2/parse` endpoint. Tool fetches the file from URL and uploads to Firecrawl, which uses a Rust-based parsing engine + neural document layout model. Auto-detects PDF type (text-based vs scanned) and chooses fast/auto/ocr extraction. Tables → full markdown; formulas → preserved LaTeX; reading order predicted. Supports PDF, DOCX, DOC, ODT, RTF, XLSX, XLS, HTML up to 50MB. Optional `structured_prompt` runs LLM extraction on the parsed text in the same call. Use cases: court filings (CourtListener PDF URLs), SEC EDGAR filings, FOIA releases, scientific papers (arxiv URLs), government documents, leaked corporate materials. REQUIRES FIRECRAWL_API_KEY.",
  inputSchema: input,
  costMillicredits: 12,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "firecrawl_parse", input: i, timeoutMs: 240_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "firecrawl_parse failed");
    return res.output;
  },
});
