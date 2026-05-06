import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["country", "list"]).optional(),
  code: z.string().optional().describe("CIA Factbook 2-letter code (e.g. 'us', 'fr', 'ja')."),
  name: z.string().optional().describe("Common country name (e.g. 'United States', 'Japan')."),
});

toolRegistry.register({
  name: "cia_factbook",
  description:
    "**CIA World Factbook (via factbook.json mirror) — country-level reference data, free no-key.** Modes: country (full Factbook record by code/name — population, GDP, government, capital, languages, labor force) and list (well-known codes). Each output emits typed entity envelope (kind: country) with stable Factbook code. Use for sanity-checking national-totals constraints (e.g., 1.8M manufacturing workers vs country labor force).",
  inputSchema: input,
  costMillicredits: 0,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "cia_factbook",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "cia_factbook failed");
    return res.output;
  },
});
