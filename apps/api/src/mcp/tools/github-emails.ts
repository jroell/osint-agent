import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  login: z.string().min(1).describe("GitHub user login"),
  pages: z.number().int().min(1).max(10).default(3).describe("How many 30-event pages of public activity to scan (default 3 ≈ recent month)"),
});

toolRegistry.register({
  name: "github_commit_emails",
  description:
    "Harvest author/committer emails from a GitHub user's recent public PushEvents. The canonical OSINT pattern for associating a real email with a GitHub identity — many users configure their personal email in git, then push it into a public repo. Returns each email with names-seen, repos-seen, and a noreply flag. Free; set GITHUB_TOKEN for 5000 req/hr.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "github_commit_emails",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "github_commit_emails failed");
    return res.output;
  },
});
