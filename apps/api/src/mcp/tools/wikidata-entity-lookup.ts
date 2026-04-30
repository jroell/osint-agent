import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Entity name (e.g. 'Anthropic') or QID (e.g. 'Q116758847')"),
  max_hits: z.number().int().min(1).max(20).default(5),
  expand_top: z.boolean().default(true).describe("Fetch full claims for the top hit"),
});

toolRegistry.register({
  name: "wikidata_entity_lookup",
  description:
    "**Foundational ER infrastructure — open knowledge graph access.** Wikidata is the open KG powering Wikipedia. Given a name → search returns top QID candidates; given a QID → returns rich entity facts including: founders (P112), CEO (P169), inception date (P571), headquarters (P159), parent organizations (P749), subsidiaries (P355), industry (P452), employee count (P1128), stock exchange (P414), official website (P856), GitHub (P1324), Twitter (P2002), LinkedIn (P6634), OpenCorporates ID (P1320). Pairs with `entity_link_finder` (Diffbot KG, curated for-profit) — Wikidata is the open complement. Free, no key.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "wikidata_entity_lookup", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "wikidata_entity_lookup failed");
    return res.output;
  },
});
