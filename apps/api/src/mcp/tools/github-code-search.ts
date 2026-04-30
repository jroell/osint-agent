import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Search query — domain ('api.target.com'), email, internal hostname, or any string"),
  limit: z.number().int().min(1).max(100).default(30).describe("Max results (max 100 per GitHub API)"),
  language: z.string().optional().describe("Filter by language (e.g. 'python', 'go')"),
  repo: z.string().optional().describe("Filter to a specific repo (e.g. 'octocat/hello-world')"),
  user: z.string().optional().describe("Filter to a specific user/org (e.g. 'anthropics')"),
  extension: z.string().optional().describe("Filter by file extension (e.g. 'env', 'tf', 'yml')"),
  in_path: z.string().optional().describe("Filter by path substring (e.g. '.github/workflows', 'config/')"),
});

toolRegistry.register({
  name: "github_code_search",
  description:
    "**Highest-leverage public-leak discovery primitive.** Searches GitHub's ~230M public-repo code index for any string — domain, email, internal hostname, partial credential. Returns matched repos with file paths + auto-classified leak severity (critical: .env/secrets/private_key; high: Dockerfile/terraform/CI configs; medium: source code; low: tests/docs). Use cases: (1) find every public repo hardcoding 'api.target.com' (often reveals deprecated subdomains, partner integrations, leaked internal URLs); (2) given an employee email, find all their public repos; (3) given an internal hostname pattern, map shadow infrastructure. Pairs with `git_secrets` — code_search FINDS the leak repo, git_secrets deep-scans it. Auth via GITHUB_TOKEN (set) → 30 req/min. Requires the search string to appear in indexed code (recent files prioritized).",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "github_code_search",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "github_code_search failed");
    return res.output;
  },
});
