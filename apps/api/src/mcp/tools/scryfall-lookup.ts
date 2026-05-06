import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "named", "card_by_id", "card_by_set"])
    .optional()
    .describe(
      "search: Scryfall syntax (q=). named: exact or fuzzy name match. card_by_id: Scryfall UUID. card_by_set: set_code+collector_number. Auto-detects from inputs."
    ),
  query: z.string().optional().describe("Scryfall syntax: 't:dragon mv<=4 c:r' etc."),
  name: z.string().optional().describe("Card name for named mode."),
  exact: z.boolean().optional().describe("If true, named mode uses exact match; default fuzzy."),
  scryfall_id: z.string().optional().describe("Scryfall UUID for card_by_id."),
  set_code: z.string().optional().describe("Set code (e.g. 'lea') for card_by_set."),
  collector_number: z.string().optional().describe("Collector number for card_by_set."),
  order: z.string().optional().describe("Sort order: name, released, set, rarity, color, usd, eur, tix, cmc, power, toughness, edhrec, penny, artist, review."),
});

toolRegistry.register({
  name: "scryfall_lookup",
  description:
    "**Scryfall — Magic: The Gathering card database, free no-key.** The canonical MTG metadata source. 4 modes: search (Scryfall syntax — t:dragon, mv<=4, c:r, set:lea, etc.), named (exact/fuzzy), card_by_id (UUID), card_by_set (set+collector#). Returns full card record with mana cost, oracle text, type, P/T, set printings, format legalities, prices, image. Each card emits typed entity (kind: trading_card, game: magic_the_gathering) with stable Scryfall+Oracle IDs for ER linking.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "scryfall_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "scryfall_lookup failed");
    return res.output;
  },
});
