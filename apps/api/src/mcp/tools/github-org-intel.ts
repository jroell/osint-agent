import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  org: z.string().min(1).describe("GitHub org login (e.g. 'anthropics', 'google')"),
  include_members: z.boolean().default(true),
  max_repos: z.number().int().min(1).max(100).default(30),
});

toolRegistry.register({
  name: "github_org_intel",
  description:
    "**Comprehensive GitHub org recon** — completes the GitHub trio with `github_code_search` (find leaks) + `github_emails` (extract commit emails). For an org returns: name/blog/location/twitter/email metadata, top repos by stars + by recent push activity, language breakdown (tech-stack signal), public team members, oldest+newest repo timestamps, headline summary. Auth via GITHUB_TOKEN unlocks higher rate limit. Use as the first GitHub recon step on any new org target.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "github_org_intel", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "github_org_intel failed");
    return res.output;
  },
});
