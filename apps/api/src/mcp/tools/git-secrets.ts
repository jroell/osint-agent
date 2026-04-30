import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(1).describe("GitHub user or org login (e.g. 'anthropics')"),
  scope: z.enum(["user", "org"]).default("org"),
  limit_per_pattern: z.number().int().min(1).max(50).default(10),
});

toolRegistry.register({
  name: "leaked_secret_git_scan",
  description:
    "Search public GitHub repos under a user or org for files matching common secret-bearing patterns (.env, credentials.json, id_rsa, AWS keys, Slack tokens, etc.). Uses the GitHub code-search API — REQUIRES the GITHUB_TOKEN env var (a free PAT works fine; unauthenticated code search is blocked by GitHub). Hits are *candidates* — manually verify before action.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "leaked_secret_git_scan",
      input: i,
      timeoutMs: 120_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "leaked_secret_git_scan failed");
    return res.output;
  },
});
