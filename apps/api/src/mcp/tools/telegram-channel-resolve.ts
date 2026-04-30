import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  channel: z.string().min(1).describe("Telegram username/channel — accepts 'durov', '@durov', 'https://t.me/durov', 't.me/s/durov', etc."),
});

toolRegistry.register({
  name: "telegram_channel_resolve",
  description:
    "**Telegram public-preview recon — fills the social-channel gap.** No auth needed. Given a username/URL, fetches t.me/s/<channel> public preview and extracts: channel/user title + bio, subscriber count + member counters, verified/private flags, recent ~10 messages with text + dates + view counts + permalinks, profile photo URL. Use cases: threat actor research (APT groups, crypto scams), news distribution mapping, influencer/community recon. Pairs with `discord_invite_resolve` (iter-29) — together they cover the two biggest non-Western-Twitter messaging platforms.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "telegram_channel_resolve", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "telegram_channel_resolve failed");
    return res.output;
  },
});
