import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search"]).optional(),
  query: z.string().describe("CQL query or simple keyword. Plain keywords auto-wrap as gallica adj \"...\"."),
});

toolRegistry.register({
  name: "gallica_search",
  description:
    "**Gallica (BnF, France) — ~10M+ digitized French/European newspapers, books, manuscripts, maps, photos, audio. Free no-key.** SRU CQL search. Each result emits typed entity envelope with type-aware kind (book | newspaper | manuscript | map | image) and stable Gallica ARK URLs. Critical for any French-language historical-source question and pre-1950 Continental European cultural references.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "gallica_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "gallica_search failed");
    return res.output;
  },
});
