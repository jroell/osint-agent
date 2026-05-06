import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search_taxa", "search_observations", "user_profile"]).optional(),
  taxon_query: z.string().optional(),
  q: z.string().optional(),
  taxon_id: z.number().int().optional(),
  taxon_name: z.string().optional(),
  place_id: z.number().int().optional(),
  user_login: z.string().optional(),
  year: z.number().int().optional(),
  month: z.number().int().min(1).max(12).optional(),
});

toolRegistry.register({
  name: "inaturalist_search",
  description:
    "**iNaturalist (api.inaturalist.org) — biodiversity OSINT, free no-key.** Modes: search_taxa (species + ranks), search_observations (with taxon/place/year/month/user filters), user_profile. Each output emits typed entity envelope (kind: taxon | observation | person) with stable iNat IDs. Critical for biology questions and observer-network analysis.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "inaturalist_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "inaturalist_search failed");
    return res.output;
  },
});
