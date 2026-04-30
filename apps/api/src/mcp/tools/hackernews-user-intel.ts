import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  username: z.string().min(2).describe("HN username (with or without @)"),
  story_limit: z.number().int().min(1).max(100).default(30),
  comment_limit: z.number().int().min(1).max(100).default(30),
});

toolRegistry.register({
  name: "hackernews_user_intel",
  description:
    "**Per-user HN deep-dive (mirror to reddit_user_intel)** — combines Firebase profile + Algolia full-text indexed stories + comments. Returns: profile (account age, karma, about, total submitted), top stories by points (highest-engagement contributions with HN URL + external URL), recent stories + comments, top submitted domains (interest graph — what blogs/news sites the user shares), posting hour distribution → inferred timezone, story-to-comment ratio (curator vs commenter style), about-field email + URL extraction (cross-platform pivot — many HN users list employer/email/twitter in about). Strong tech-identity ER for senior engineers, founders, VCs, AI researchers. Free, no auth.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "hackernews_user_intel", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "hackernews_user_intel failed");
    return res.output;
  },
});
