import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["name", "fname", "id", "archetype"])
    .optional()
    .describe(
      "name: exact card name. fname: fuzzy/contains. id: numeric YGO id. archetype: list cards in archetype (e.g. 'Ally of Justice'). Auto-detects from inputs."
    ),
  name: z.string().optional().describe("Exact card name."),
  query: z.string().optional().describe("Fuzzy substring for fname mode."),
  fname: z.string().optional().describe("Alias for query."),
  id: z.number().int().optional().describe("YGO numeric card id."),
  archetype: z.string().optional().describe("Archetype name (e.g. 'Ally of Justice', 'Blue-Eyes')."),
});

toolRegistry.register({
  name: "ygoprodeck_lookup",
  description:
    "**YGOPRODeck — Yu-Gi-Oh! card database, free no-key.** Canonical YGO TCG/OCG metadata: ATK/DEF, level/rank/link, attribute, race, archetype, set printings, banlist status (TCG/OCG/GOAT), lore. 4 modes: name (exact), fname (fuzzy), id (numeric), archetype (list-by-archetype). Each card emits typed entity (kind: trading_card, game: yugioh) for ER. Pairs with `scryfall_lookup` (MTG) for cross-game card OSINT.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "ygoprodeck_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "ygoprodeck_lookup failed");
    return res.output;
  },
});
