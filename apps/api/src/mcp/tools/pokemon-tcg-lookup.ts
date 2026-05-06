import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["card_search", "card_by_id", "set_list"]).optional(),
  query: z.string().optional().describe("Pokémon TCG syntax (e.g. 'name:charizard', 'set.id:swsh4 hp:[100 TO *]')."),
  card_id: z.string().optional().describe("Card id (e.g. 'swsh4-25')."),
});

toolRegistry.register({
  name: "pokemon_tcg_lookup",
  description:
    "**Pokémon TCG (api.pokemontcg.io) — free, optional POKEMONTCG_API_KEY for higher rate limits.** Modes: card_search (Pokémon TCG syntax), card_by_id, set_list. Each card emits typed entity envelope (kind: trading_card, game: pokemon) with stable Pokémon TCG ID.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "pokemon_tcg_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "pokemon_tcg_lookup failed");
    return res.output;
  },
});
