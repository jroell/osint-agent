import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["by_uploader_email", "search", "item_detail"]).default("search"),
  query: z.string().min(2).describe("Email (by_uploader_email — e.g. 'jason@textfiles.com'), keyword (search), or archive.org identifier (item_detail)"),
  limit: z.number().int().min(1).max(100).default(25),
});

toolRegistry.register({
  name: "internet_archive_search",
  description:
    "**archive.org search and uploader trace** — queries archive.org's free public advanced-search + metadata APIs (~50M items: government documents, court records, leaked corporate materials, video archives, books, software, personal uploads). 3 modes: 'by_uploader_email' (canonical email-based uploader query — trace ALL items uploaded by one email = a public-archiving footprint, e.g. 'jason@textfiles.com' returns Jason Scott's 54+ uploads), 'search' (full-text title/description across all items, sorted by downloads), 'item_detail' (full metadata + file count for a specific identifier). Aggregations: top mediatypes, top collections, year distribution, total downloads. archive.org is massively under-leveraged in OSINT — this is the canonical surface for tracing prolific archivists, public-domain hoarders, and document-leak preservers.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "internet_archive_search", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "internet_archive_search failed");
    return res.output;
  },
});
