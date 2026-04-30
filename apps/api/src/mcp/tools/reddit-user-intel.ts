import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  username: z.string().min(2).describe("Reddit username (with or without u/ prefix)"),
  post_limit: z.number().int().min(1).max(100).default(25),
  comment_limit: z.number().int().min(1).max(100).default(50),
});

toolRegistry.register({
  name: "reddit_user_intel",
  description:
    "**Per-user Reddit deep dive** — pulls full Reddit profile + recent posts + recent comments via public JSON API (no auth). Returns: account profile (age in years, total/link/comment karma, employee/mod/gold/verified flags, NSFW flag), recent posts, recent comments with bodies, top subreddits aggregation (interest graph), posting-hour distribution (UTC), inferred timezone from posting peak window, and self-disclosure extraction (emails, URLs, 'I live in X' / 'I work at Y' keyword phrases). Use cases: behavioral ER, sock-puppet detection (very-new account flag), interest profiling, timezone inference for OPSEC analysis, employer/location disclosure mining. Pairs with reddit_org_intel (keyword search) for full Reddit coverage. Free, no auth.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "reddit_user_intel", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "reddit_user_intel failed");
    return res.output;
  },
});
