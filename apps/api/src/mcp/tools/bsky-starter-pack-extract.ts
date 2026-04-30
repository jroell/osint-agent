import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().min(5).describe("Bluesky starter pack URL (https://bsky.app/starter-pack/<handle>/<rkey>) OR AT-URI (at://did:plc:.../app.bsky.graph.starterpack/<rkey>)"),
});

toolRegistry.register({
  name: "bsky_starter_pack_extract",
  description:
    "**Community-mapping primitive** — Bluesky starter packs are public curated lists of accounts ('Follow these 30 security researchers', 'Follow these 50 climate scientists', etc.). Resolves a starter pack URL/AT-URI to the COMPLETE member list with profiles (handle, display name, bio, avatar, follower/following/post counts). Pages through the full member list (capped at 500). Use case: when an account surfaces in a threat actor cluster, finding their starter packs reveals adjacent community structure. Free, no auth.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "bsky_starter_pack_extract", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "bsky_starter_pack_extract failed");
    return res.output;
  },
});
