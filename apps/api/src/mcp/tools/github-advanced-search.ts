import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["commits", "issues", "users"])
    .default("commits")
    .describe(
      "commits: search commit messages globally (use qualifiers like 'author-email:foo@bar.com', 'author-name:\"Jane Doe\"', 'repo:owner/name', 'committer-date:>2024-01-01'). issues: search issues+PRs (qualifiers like 'is:pr', 'state:closed', 'author:username', 'label:bug', 'in:title', 'repo:owner/name'). users: search user profiles (qualifiers like 'in:login', 'in:name', 'in:email', 'location:Cincinnati', 'language:Go', 'followers:>100', 'repos:>5')."
    ),
  query: z
    .string()
    .min(1)
    .describe(
      "GitHub search query. Free text + qualifiers (see mode description). Examples: 'author-email:jane@acme.com' (commits mode), 'is:pr is:open vurvey' (issues mode), 'location:Cincinnati language:Go followers:>50' (users mode)."
    ),
  sort: z
    .enum(["author-date", "committer-date", "created", "updated", "comments", "followers", "repositories", "joined"])
    .optional()
    .describe(
      "Sort field. commits: author-date|committer-date. issues: created|updated|comments. users: followers|repositories|joined."
    ),
  order: z.enum(["asc", "desc"]).default("desc").describe("Sort order (default desc)."),
  limit: z.number().int().min(1).max(100).default(20).describe("Max results to return (default 20, max 100)."),
});

toolRegistry.register({
  name: "github_advanced_search",
  description:
    "**GitHub Advanced Search — three surfaces beyond `github_code_search`.** (1) **commits** — keyword across ALL public commit messages on GitHub, returns SHA + author/committer name+email+date + repo + commit URL. **Killer ER usage**: `author-email:foo@bar.com` enumerates every public-GitHub repo a given email has ever committed to (we tested with `jroell@batterii.com` → 622 commits across multiple repos including private-looking project names — a person's full coding-history shadow). (2) **issues** — keyword across ALL public issues + PRs (treated uniformly), returns title + author + state + labels + body excerpt. Use cases: vulnerability disclosures, complaint trails, social-graph evidence (who comments on whose projects). (3) **users** — search user profiles by login/name/email/location/language/followers — useful for finding alt accounts of a target. Auth via GITHUB_TOKEN env var (already set; gives 30 req/min vs 10 unauth). Distinct from `github_code_search` (code content), `github_commit_emails` (per-user email scrape), `github_user_profile` (full single-profile fetch), `github_org_intel` (org metadata).",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "github_advanced_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "github_advanced_search failed");
    return res.output;
  },
});
