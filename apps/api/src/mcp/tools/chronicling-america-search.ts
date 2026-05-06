import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search_pages", "newspaper"])
    .optional()
    .describe("search_pages: full-text page-level search. newspaper: title metadata by LCCN."),
  query: z.string().optional().describe("Search query for search_pages mode."),
  year_from: z.number().int().optional().describe("Earliest year (e.g. 1890)."),
  year_to: z.number().int().optional().describe("Latest year (e.g. 1920)."),
  state: z.string().optional().describe("US state filter (e.g. 'Illinois')."),
  lccn: z.string().optional().describe("Library of Congress Control Number (e.g. 'sn84038095'). For newspaper mode = the title; for search_pages mode = restrict to one paper."),
  limit: z.number().int().min(1).max(100).optional().describe("Max results (default 20)."),
});

toolRegistry.register({
  name: "chronicling_america_search",
  description:
    "**Chronicling America (Library of Congress) — US historic newspapers, free no-key.** ~3.7M digitized newspaper pages 1690-1963 with OCR full-text search. Modes: search_pages (full-text with year/state/LCCN filters) and newspaper (title metadata by LCCN). Each result emits typed entity envelope (kind: newspaper_page | newspaper_title) with stable LoC URL. Pairs with `trove_search` (Australian counterpart) and `loc_catalog_search` (full LoC catalog).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "chronicling_america_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "chronicling_america_search failed");
    return res.output;
  },
});
