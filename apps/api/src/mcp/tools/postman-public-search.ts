import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe("Search query — brand name, internal hostname, or any string. e.g. 'vurvey', 'api.target.com', 'admin-api'"),
  limit: z.number().int().min(1).max(100).default(25).describe("Max workspaces to return"),
  exact_match_only: z.boolean().default(false).describe("If true, filter out fuzzy matches (only return workspaces where query appears verbatim in name or description)"),
});

toolRegistry.register({
  name: "postman_public_search",
  description:
    "**The OSINT goldmine no one talks about — searches ~10M public Postman workspaces.** Companies routinely leak internal API collections by accidentally publishing them: hardcoded Bearer tokens in Authorization headers, internal API hostnames, partner API keys, sensitive endpoints with example payloads. Postman's index is invisible to Google/Tavily — only Postman search itself surfaces it. Returns workspaces with leak severity (critical: 'internal'/'admin'/'staging' tokens in name; high: exact-match in workspace name; medium: exact-match in description; low: legit verified-publisher workspaces). Pairs with `github_code_search`: GitHub finds leaked URLs/configs in code, Postman finds leaked URLs+CREDENTIALS in API definitions. Free, no API key.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "postman_public_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "postman_public_search failed");
    return res.output;
  },
});
