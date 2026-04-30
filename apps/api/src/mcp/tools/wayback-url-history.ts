import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  url: z.string().min(3).describe("URL or domain (e.g. 'anthropic.com' or 'https://example.com/path')"),
  match_type: z.enum(["exact", "host", "domain", "prefix"]).default("exact").describe("'exact' = this URL only; 'host' = same hostname any path; 'domain' = same domain incl subdomains; 'prefix' = URL-prefix"),
  from: z.string().optional().describe("Earliest YYYY[MMDD] (e.g. '2010' or '20100101')"),
  to: z.string().optional().describe("Latest YYYY[MMDD]"),
  limit: z.number().int().min(1).max(5000).default(200).describe("Max unique-digest snapshots to return"),
});

toolRegistry.register({
  name: "wayback_url_history",
  description:
    "**Wayback Machine temporal recon for a URL/domain** — queries archive.org's CDX API for the full snapshot timeline. Free, no auth. Returns: first-seen + last-seen snapshots with archive URLs, total snapshot count, unique content versions (collapsed by digest), unique-digest snapshots list (deduplicated content changes), yearly distribution with gap detection (dormancy periods), status code breakdown (4xx/5xx history), MIME type breakdown. Use cases: domain ownership-transition detection (e.g. anthropic.com had snapshots from 2013 but Anthropic was founded 2021 = previous owner), domain age verification, dormancy/parking detection (gaps in yearly distribution), historical content audit, takedown evidence. Pairs with `wayback`, `wayback_endpoint_extract` for endpoint extraction. ",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "wayback_url_history", input: i, timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "wayback_url_history failed");
    return res.output;
  },
});
