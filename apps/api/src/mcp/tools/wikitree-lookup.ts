import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "profile", "ancestors", "descendants", "relatives"])
    .optional()
    .describe("search: by name. profile/ancestors/descendants/relatives: by WikiTree ID."),
  first_name: z.string().optional(),
  last_name: z.string().optional(),
  wikitree_id: z.string().optional().describe("WikiTree ID like 'Smith-1'."),
  depth: z.number().int().min(1).max(10).optional().describe("Tree depth for ancestors/descendants (default 3)."),
});

toolRegistry.register({
  name: "wikitree_lookup",
  description:
    "**WikiTree — community-curated global family tree, ~30M+ profiles, free no-key.** Modes: search (first+last name), profile (by WikiTree ID), ancestors (pedigree to depth N), descendants (descendant tree), relatives (parents+spouses+children+siblings). Each result emits typed entity envelope (kind: person | relationship) with stable WikiTree ID and role-edge attributes (parent_of:X, spouse_of:Y) that panel_entity_resolution ingests directly to build family graphs. Pairs with `familysearch_lookup` (LDS, broader) and `findagrave_search` (death records).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "wikitree_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "wikitree_lookup failed");
    return res.output;
  },
});
