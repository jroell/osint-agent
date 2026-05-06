import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "item", "subject_authority"])
    .optional()
    .describe("search: full-text search across LoC. item: fetch one item by URL slug. subject_authority: id.loc.gov authority lookup."),
  query: z.string().optional().describe("Search query."),
  collection: z.string().optional().describe("Collection slug to restrict the search."),
  only_online: z.boolean().optional().describe("If true, restrict to digitized/online items."),
  loc_url_slug: z.string().optional().describe("Item URL slug for item mode (e.g. 'item/96526449')."),
  subject: z.string().optional().describe("Subject term for subject_authority mode (e.g. 'Theosophy')."),
});

toolRegistry.register({
  name: "loc_catalog_search",
  description:
    "**Library of Congress catalog (loc.gov + id.loc.gov) — free no-key.** Authority files, subject headings, books, prints, photographs, manuscripts, maps. Modes: search (loc.gov full-text), item (specific item by slug), subject_authority (id.loc.gov suggest2 lookup of preferred subject term). Each result emits typed entity envelope (kind: library_item | subject_authority) with stable LoC IDs. Use for cross-reference of authority records, ID disambiguation, and book lookups outside HathiTrust/WorldCat.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "loc_catalog_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "loc_catalog_search failed");
    return res.output;
  },
});
