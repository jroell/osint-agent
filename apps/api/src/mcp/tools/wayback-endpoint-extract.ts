import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(3).describe("Apex domain (e.g. 'vurvey.app'). Wayback covers all subdomains via matchType=domain."),
  limit: z.number().int().min(100).max(50000).default(5000).describe("Max captures to fetch from CDX. Wayback can return millions for popular domains."),
  match_type: z.enum(["exact", "prefix", "host", "domain"]).default("domain"),
  mime_prefix: z.string().optional().describe("Filter to MIME types starting with this prefix (e.g. 'application/json' for API responses only)"),
  success_only: z.boolean().default(true).describe("Only include captures with HTTP 200 (filters out the 404 noise)"),
});

toolRegistry.register({
  name: "wayback_endpoint_extract",
  description:
    "**Third moat-feeding discovery channel.** Queries Wayback Machine's CDX API (858B+ captures, free, no key) for every URL ever archived under a target domain. Filters to API-pattern URLs (`/api/`, `/v\\d+/`, `/graphql`, `/admin/`, etc.) and scores each for API-likelihood with the same algorithm as `js_endpoint_extract` for cross-tool comparability. Captures = signal of importance (URLs hit many times across years are more important than one-off captures). Includes oldest/newest capture timestamps so the agent can spot endpoints that have been around for years vs. newly-deployed ones. Pair with `api_endpoint_record` to write findings to the moat database.",
  inputSchema: input,
  costMillicredits: 6,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "wayback_endpoint_extract",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "wayback_endpoint_extract failed");
    return res.output;
  },
});
