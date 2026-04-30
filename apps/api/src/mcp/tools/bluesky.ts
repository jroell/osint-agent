import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  handle: z.string().min(1).describe("Bluesky handle, with or without leading @ (e.g. 'alice.bsky.social' or just 'alice' — bare names default to .bsky.social)"),
  include_recent: z.boolean().default(true),
});

toolRegistry.register({
  name: "bluesky_user",
  description:
    "Fetch a Bluesky profile via the public AT-Protocol XRPC API. Returns DID, display name, bio, follower/following/post counts, and recent posts. Free, no key, very stable. Bluesky's full firehose is publicly readable, making it the most OSINT-friendly active social network.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "bluesky_user",
      input: i,
      timeoutMs: 20_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "bluesky_user failed");
    return res.output;
  },
});
