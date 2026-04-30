import { z } from "zod";
import { toolRegistry } from "./instance";
import { callPyWorker } from "../../workers/py-client";

const input = z.object({
  url: z.string().url().describe("URL of a publicly-hosted PDF document"),
  full: z.boolean().default(false).describe("Return the entire document text (capped at 5 MiB) instead of the first 50k chars"),
  timeout_seconds: z.number().int().min(5).max(180).default(30),
});

toolRegistry.register({
  name: "pdf_document_analyze",
  description:
    "Fetch a PDF by URL and extract its metadata (author, title, dates, creator) plus the document text. Useful for analyzing leaked documents, public filings, and PDFs surfaced by other tools (cert-transparency hits, Wayback snapshots, etc.).",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callPyWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "pdf_document_analyze",
      input: i,
      timeoutMs: (i.timeout_seconds + 30) * 1000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "pdf_document_analyze failed");
    return res.output;
  },
});
