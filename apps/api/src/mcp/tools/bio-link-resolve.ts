import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  username: z.string().min(2).optional().describe("Username to look up across linktr.ee, about.me, taplink.cc (auto-discovery mode)"),
  url: z.string().url().optional().describe("Direct URL of bio-link page (overrides username/service)"),
  service: z.enum(["linktree", "about_me", "taplink"]).optional().describe("Lock to specific service instead of auto-discovery"),
}).refine((d) => d.username || d.url, { message: "Either username or url is required" });

toolRegistry.register({
  name: "bio_link_resolve",
  description:
    "**Self-published cross-platform identity graph** — resolves a username's 'link in bio' page on linktr.ee, about.me, or taplink.cc and extracts every social/payment handle they've publicly declared. Auto-discovery tries all three. Returns: structured social_handles map (instagram=username, x=handle, linkedin=slug, github=user, etc. across 25+ platforms), all raw links, profile metadata. Highest-trust ER signal short of a verified identity provider — when a user lists their own IG + LinkedIn + Venmo on Linktree, that's a HARD link between those identities. Strong complement to maigret/sherlock (which guess) — bio_link is what the user has actually claimed. Note: bio.link/beacons.ai/lnk.bio are Cloudflare-protected and not yet supported.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "bio_link_resolve", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "bio_link_resolve failed");
    return res.output;
  },
});
