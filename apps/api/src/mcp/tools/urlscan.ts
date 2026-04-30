import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z.string().min(2).describe(
    "urlscan.io search syntax. Pivot examples: " +
    "`domain:example.com` (every scan of this domain), " +
    "`ip:1.2.3.4` (every site seen at this IP — primary entity-resolution pivot), " +
    "`asn:AS13335` (every site in this ASN), " +
    "`hash:<TLS-cert-sha256>` (sites sharing a cert), " +
    "`favicon_hash:<mmh3>` (sites with same favicon — high-precision shared-infra pivot), " +
    "`page.title:\"Vurvey\"`, `technology:\"Sanity\"`. Combine with AND/OR/NOT."
  ),
  size: z.number().int().min(1).max(1000).default(50),
});

toolRegistry.register({
  name: "urlscan_search",
  description:
    "Query urlscan.io's 800M+ historical scans — the most powerful entity-resolution primitive in this catalog. Every scan is indexed by IP, ASN, TLS cert hash, favicon hash, page content, hostname, technology, and geo. Free public-only path needs no key (rate-limited); set URLSCAN_API_KEY for 1000 searches/month. Use for: 'find all domains seen at this IP', 'find all sites with this favicon', 'find all sites sharing this TLS cert', etc. Returns deduplication stats (unique IPs / domains / ASNs) so the agent can spot infrastructure clusters at a glance.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "urlscan_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "urlscan_search failed");
    return res.output;
  },
});
