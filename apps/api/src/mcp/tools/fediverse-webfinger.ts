import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  handle: z.string().min(3).describe("Fediverse handle: '@user@instance', 'user@instance', or 'https://instance/@user'"),
  probe_outbox: z.boolean().default(true).describe("Probe outbox for total post count"),
  probe_nodeinfo: z.boolean().default(true).describe("Probe instance nodeinfo for server software detection"),
});

toolRegistry.register({
  name: "fediverse_webfinger",
  description:
    "**Universal Fediverse identity resolver** — given a handle in the form '@user@instance', resolves the canonical profile via WebFinger + ActivityPub. Works across the entire ActivityPub ecosystem: Mastodon, Pleroma, Akkoma, Misskey/Calckey/Firefish/Sharkey, PixelFed (federated IG), PeerTube (federated YouTube), Lemmy (federated Reddit), Friendica, GoToSocial, kbin/Mbin. Returns: profile (display name, bio plain+HTML, avatar/header, account published date, followers/following/statuses counts, locked flag, profile-card 'verified link' URLs), ActivityPub actor data (inbox/outbox/publicKey PEM = cryptographic identity for cross-instance migration verification), instance metadata (server software + version + open_registrations + user counts via nodeinfo). Strong novel ER: Fediverse is a major identity space invisible to centralized social tools, and ActivityPub `publicKey` is a hard cryptographic identity signal that survives instance migrations. Free, no auth.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "fediverse_webfinger", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "fediverse_webfinger failed");
    return res.output;
  },
});
