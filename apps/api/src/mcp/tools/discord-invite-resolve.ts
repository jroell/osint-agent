import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  invite_code: z.string().min(2).describe("Discord invite code or URL (e.g. 'abc123', 'discord.gg/abc123', 'https://discord.com/invite/abc123')"),
});

toolRegistry.register({
  name: "discord_invite_resolve",
  description:
    "**Novel SOTA OSINT — Discord invite resolution.** Resolves any Discord invite code to full server metadata via Discord's free no-auth API: server name + ID, member count + online presence, channel target, inviter (bot/user/system), verification level, NSFW level, premium boost count, server features (PARTNERED, VERIFIED, COMMUNITY), expiration. Use cases: threat actor research (crypto scam Discord servers), community mapping, phishing investigation (resolve invites posted in malicious tweets without joining). Free, no auth, no rate limits. Underexploited OSINT surface — most catalogs don't expose Discord at all.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "discord_invite_resolve", input: i, timeoutMs: 25_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "discord_invite_resolve failed");
    return res.output;
  },
});
