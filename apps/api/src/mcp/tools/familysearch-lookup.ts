import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "person", "ancestry", "descendancy"])
    .optional()
    .describe("search: by name. person/ancestry/descendancy: by FamilySearch PID."),
  given_name: z.string().optional(),
  surname: z.string().optional(),
  born_year: z.string().optional(),
  died_year: z.string().optional(),
  born_place: z.string().optional(),
  pid: z.string().optional().describe("FamilySearch Person ID."),
  generations: z.number().int().min(1).max(8).optional().describe("Generation depth for ancestry/descendancy (default 4)."),
});

toolRegistry.register({
  name: "familysearch_lookup",
  description:
    "**FamilySearch (LDS Church) — the most comprehensive global genealogy database, ~1.5B persons. REQUIRES FAMILYSEARCH_ACCESS_TOKEN (free OAuth at familysearch.org/developers).** Modes: search (name + birth/death year + place), person (by PID), ancestry (pedigree N generations), descendancy. Each output emits typed entity envelope (kind: person) with stable FamilySearch PID. Pairs with `wikitree_lookup` (open) and `findagrave_search` (death records).",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "familysearch_lookup",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "familysearch_lookup failed");
    return res.output;
  },
});
