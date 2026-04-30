import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  username: z.string().min(1).describe("Hacker News username (e.g. 'pg', 'patio11')"),
  include_recent: z.boolean().default(true),
  recent_n: z.number().int().min(1).max(20).default(5),
});

toolRegistry.register({
  name: "hackernews_user",
  description:
    "Fetch a Hacker News user profile (karma, account age, bio) and recent submissions. HN's Firebase-backed API is among the most stable public APIs in OSINT — use freely. Excellent for tech-persona attribution since most OSINT-relevant developers/founders have an HN account.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "hackernews_user",
      input: i,
      timeoutMs: 20_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "hackernews_user failed");
    return res.output;
  },
});
