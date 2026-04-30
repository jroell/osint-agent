import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["user_search", "user_expertise", "tag_top_users", "keyword_search"]).default("user_search"),
  query: z.string().min(1).describe("Display name (user_search), numeric user_id (user_expertise), tag name (tag_top_users), or keyword (keyword_search)"),
  limit: z.number().int().min(1).max(100).default(20),
});

toolRegistry.register({
  name: "bigquery_stack_overflow",
  description:
    "**Tech-identity ER via Stack Overflow archive on BigQuery** — `bigquery-public-data.stackoverflow.*`. Modes: 'user_search' (find users by display name → reputation, location, bio, votes), 'user_expertise' (top tags this user has asked questions in — proxy for domain expertise; pass user_id), 'tag_top_users' (top experts in a tag like 'langchain' by answer count), 'keyword_search' (questions matching a keyword). Use cases: tech-identity ER (cross-reference with `github_user`/`github_emails`), recruiting (find domain experts), competitive intel (which engineers are asking about your tech stack?).",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "bigquery_stack_overflow", input: i, timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "bigquery_stack_overflow failed");
    return res.output;
  },
});
