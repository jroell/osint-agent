import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  username: z.string().optional().describe("Farcaster username (with or without '@', e.g. 'dwr' or 'dwr.eth')"),
  fid: z.number().int().positive().optional().describe("Numeric Farcaster ID (alternative to username)"),
  include_verifications: z.boolean().default(true).describe("Pull cryptographically-verified wallet addresses (Ethereum + Solana)"),
  include_casts: z.boolean().default(true).describe("Pull recent casts (posts)"),
  cast_limit: z.number().int().min(1).max(50).default(10),
}).refine((d) => d.username || d.fid, { message: "Either username or fid is required" });

toolRegistry.register({
  name: "farcaster_user_lookup",
  description:
    "**Farcaster (Web3 social) identity ER** — queries Warpcast's public Farcaster API. Free, no auth. Returns: FID (Farcaster ID, anchored to a blockchain registry — globally unique + immutable), username, display name, bio, location, profile image, follower + following counts, power badge flag (Warpcast power-user signal), early wallet adopter flag, account level (pro/etc.), **CRYPTOGRAPHICALLY-VERIFIED WALLETS** (Ethereum + Solana addresses linked to the FID via signed messages — hard cross-chain Web3 ER), and recent casts with engagement counts. Pairs with ens_resolve (verified Ethereum addresses → ENS names → onchain history), nostr_user_lookup (alt Web3 social), and onchain_tx_analysis (BigQuery Eth tx analysis) — together they form a complete Web3-native identity stack. Auto-detects FID-vs-username input.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "farcaster_user_lookup", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "farcaster_user_lookup failed");
    return res.output;
  },
});
