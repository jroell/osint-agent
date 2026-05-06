import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "page", "hierarchy"]).optional(),
  query: z.string().optional(),
  page_id: z.number().int().optional(),
  hierarchy_id: z.number().int().optional(),
});

toolRegistry.register({
  name: "eol_search",
  description:
    "**Encyclopedia of Life (eol.org) — ~2M+ taxa with descriptions, common names, references, free no-key.** Modes: search (taxon by name), page (full taxon record by EOL id), hierarchy (taxonomic hierarchy). Complementary to `inaturalist_search` (observations) — use EOL for taxon-level reference data, iNaturalist for observation/observer data. Each output emits typed entity envelope (kind: taxon) with stable EOL page IDs.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "eol_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "eol_search failed");
    return res.output;
  },
});
