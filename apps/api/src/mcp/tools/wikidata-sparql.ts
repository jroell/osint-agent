import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["sparql", "find_humans_by_attr"])
    .optional()
    .describe("sparql: raw SPARQL query (default if input.query). find_humans_by_attr: templated 'humans matching attributes' helper without SPARQL knowledge."),
  query: z.string().optional().describe("Raw SPARQL for sparql mode."),
  occupation: z.string().optional().describe("Wikidata QID for occupation (e.g. Q170790 = mathematician). For find_humans_by_attr."),
  occupation_label: z.string().optional().describe("English occupation label as fallback when QID unknown."),
  died_year: z.string().optional().describe("YYYY filter for date-of-death."),
  born_year: z.string().optional().describe("YYYY filter for date-of-birth."),
  nationality: z.string().optional().describe("Wikidata QID for country of citizenship (e.g. Q30 = USA)."),
  limit: z.number().int().min(1).max(200).optional().describe("Max bindings (default 50)."),
});

toolRegistry.register({
  name: "wikidata_sparql",
  description:
    "**Wikidata SPARQL — arbitrary structured queries over Wikidata's ~110M entity graph, free no-key.** The single most powerful catalog tool: ask 'all behavioral ecologists who died in 2023', 'all colleges founded 1925-1930', 'all monarchs deposed in the 19th century with descendants at 13th-c universities', etc. Modes: sparql (expert raw query) and find_humans_by_attr (templated 'humans matching profession+born/died-year+nationality' helper for non-experts). Each binding emits a typed entity envelope (kind: wikidata_entity) with stable QID. Pairs with `wikidata_entity_lookup` for follow-up enrichment.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "wikidata_sparql",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "wikidata_sparql failed");
    return res.output;
  },
});
