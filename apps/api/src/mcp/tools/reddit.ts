import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["user", "subreddit", "search"]).default("search"),
  query: z.string().min(1).describe("For mode=user: the username. For mode=subreddit: the subreddit name (without /r/). For mode=search: a free-text query."),
  limit: z.number().int().min(1).max(100).default(25),
});

toolRegistry.register({
  name: "reddit_query",
  description:
    "Query Reddit's public JSON API: a user's recent posts/comments, the hot listing in a subreddit, or a site-wide search. Free, no API key. Read-only.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "reddit_query",
      input: i,
      timeoutMs: 25_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "reddit_query failed");
    return res.output;
  },
});
