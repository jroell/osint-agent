import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["org_activity", "user_activity", "repo_history", "shadow_contributors"]).default("org_activity"),
  target: z.string().min(1).describe("Org login, user login, or 'owner/repo' (mode-dependent)"),
  days_back: z.number().int().min(1).max(90).default(7),
  limit: z.number().int().min(1).max(500).default(50),
});

toolRegistry.register({
  name: "bigquery_github_archive",
  description:
    "**GH Archive via BigQuery — every public GitHub event since 2011.** Modes: 'org_activity' (all events for an org over date range — events_by_type + top_actors + top_repos), 'user_activity' (per-user history), 'repo_history' (per-repo activity), 'shadow_contributors' (top pushers/PR-authors who AREN'T in github_org_intel's public_members list — reveals employees/contractors not publicly attributed). Default 7 days, max 90. Requires gcloud-authenticated host.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "bigquery_github_archive", input: i, timeoutMs: 120_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "bigquery_github_archive failed");
    return res.output;
  },
});
