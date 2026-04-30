import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  email: z.string().email(),
});

toolRegistry.register({
  name: "gravatar_lookup",
  description:
    "Resolve an email to a Gravatar profile if one exists. Returns display name, location, bio, profile URL, avatar, AND any LINKED VERIFIED accounts (Twitter, GitHub, Mastodon, etc.) the user has connected. Free, no key. Excellent OSINT pivot — Gravatar profiles are voluntarily linked, so matches are high-precision.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "gravatar_lookup",
      input: i,
      timeoutMs: 15_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "gravatar_lookup failed");
    return res.output;
  },
});
