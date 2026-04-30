import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  identifier: z.string().min(3).describe("Nostr identifier — npub1xxxx, hex pubkey (64 chars), or NIP-05 (user@domain.com)"),
});

toolRegistry.register({
  name: "nostr_user_lookup",
  description:
    "**Nostr — censorship-resistant decentralized social.** Resolves npub/hex pubkey/NIP-05 to profile metadata + recent notes via njump.me HTTP gateway. Returns: display name, bio, picture, banner, NIP-05 verification, lightning address (Lud16), website, recent note text. Use cases: crypto/Web3 social ER (Nostr is the Twitter alternative for crypto-native communities), threat actor research (some actors use Nostr to evade deplatforming), cross-reference with `ens_resolve` (many Web3 users tie ENS+Nostr to same wallet). Free, no auth.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "nostr_user_lookup", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "nostr_user_lookup failed");
    return res.output;
  },
});
