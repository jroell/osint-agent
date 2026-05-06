import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["by_id", "search"])
    .optional()
    .describe("by_id: fetch by MGP id. search: search by name."),
  mgp_id: z.union([z.string(), z.number()]).optional().describe("Mathematics Genealogy Project numeric id."),
  name: z.string().optional().describe("Name to search."),
});

toolRegistry.register({
  name: "math_genealogy",
  description:
    "**Mathematics Genealogy Project (MGP) — the authoritative public dataset of PhD-supervisor chains for math, statistics, and adjacent quantitative fields, free no-key.** Modes: by_id (MGP numeric id → person + dissertation + advisors + students) and search (by name → candidate ids). Each result emits typed entity envelope (kind: scholar) with role discriminators (advisor_of:X, student_of:X) — directly ingestable by panel_entity_resolution to build supervisor-chain graphs. Closes the dissertation-supervisor lookup gap for STEM scholars.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "math_genealogy",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "math_genealogy failed");
    return res.output;
  },
});
