import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2),
  scope: z.enum(["projects", "blobs", "commits", "issues", "merge_requests", "users"]).default("blobs"),
  limit: z.number().int().min(1).max(100).default(20),
});

toolRegistry.register({
  name: "gitlab_search",
  description:
    "**Seventh leak-discovery channel** — GitLab.com public search (mirrors github_code_search for orgs that prefer GitLab). Auto-classifies leak severity by file path (env/secrets/config = critical; Dockerfile/.tf/CI = high; source = medium). Without GITLAB_TOKEN: limited to project search; with token (free signup): full code-content blob search. Use when a target's GitHub presence is sparse but they may host on GitLab.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "gitlab_search", input: i, timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "gitlab_search failed");
    return res.output;
  },
});
