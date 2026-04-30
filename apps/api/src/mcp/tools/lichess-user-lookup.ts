import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  username: z.string().min(2).describe("Lichess username (with or without @, full URLs stripped automatically)"),
});

toolRegistry.register({
  name: "lichess_user_lookup",
  description:
    "**Lichess.org chess player ER** — public-API user lookup, no auth required. Returns: profile (real name, country code, city/location, bio, FIDE rating, USCF rating, ECF rating, links), title (GM/IM/WGM/FM/CM/NM — FIDE-awarded for the master titles, verifiable via fide.com), account age, total games + W/L/D record, play time, performance ratings across all time controls (bullet/blitz/rapid/classical/chess960/atomic), patron flag (financial supporter), TOS violation flag (cheating history). Auto-extracts emails + URLs from bio (cross-platform pivot — many players list Twitter/YouTube/Twitch/email in their Lichess bio). Strong international ER: Lichess has ~200M+ users globally. Real names + city locations + emails are commonly disclosed. Pairs with chess_com_user (different Lichess) and FIDE/USCF database lookups for full chess identity graph.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "lichess_user_lookup", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "lichess_user_lookup failed");
    return res.output;
  },
});
