import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().url().describe("Root URL to map (e.g. 'https://example.com')"),
  search: z.string().optional().describe("Optional keyword filter — return only URLs matching (e.g. 'login OR admin' to find auth-related pages)"),
  limit: z.number().int().min(1).max(5000).default(100),
  include_subdomains: z.boolean().default(true).describe("Whether to crawl subdomains of the root domain"),
});

toolRegistry.register({
  name: "firecrawl_map",
  description:
    "**Single-call site URL discovery via Firecrawl /map** — given a root URL, returns up to 5000 internal URLs in seconds. Pairs naturally with subfinder (subdomain discovery), wayback_url_history (temporal recon), js_endpoint_extract (API endpoints from JS), swagger_openapi_finder, and well_known_recon. The optional `search` parameter filters URLs by keyword — extremely useful for 'find all login/admin/api/dashboard/auth pages' across a target. Aggregations: unique subdomains discovered (e.g. mapping vurvey.com surfaces help.vurvey.com automatically), top path prefixes (which sections does the site have? /blog /careers /api etc.), and highlight panel for high-value URLs (login/admin/api/auth/dashboard/wp-admin/graphql/swagger). REQUIRES FIRECRAWL_API_KEY.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "firecrawl_map", input: i, timeoutMs: 75_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "firecrawl_map failed");
    return res.output;
  },
});
