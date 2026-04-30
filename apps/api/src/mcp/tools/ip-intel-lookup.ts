import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

// =============================================================================
// ip_intel_lookup — single IP recon
// =============================================================================
const singleInput = z.object({
  ip: z.string().min(7).describe("IPv4 or IPv6 address"),
});

toolRegistry.register({
  name: "ip_intel_lookup",
  description:
    "**IP geo + ASN + ISP + threat-flag lookup via ip-api.com** — free, no-auth (45 req/min/source). Returns: country/region/city/zip/lat/lon/timezone, ISP + organization + ASN + ASname, reverse DNS, plus **defensive flags**: is_mobile (mobile carrier), is_proxy (VPN/proxy/Tor), is_hosting (datacenter/cloud edge). The proxy/hosting/mobile flags are uniquely free here — most other 'free' IP services charge for them. Defensive ER use cases: identify proxy traffic in logs, geolocate connection origins, distinguish residential vs hosting/cloud IPs. Pairs with shodan/censys/urlscan for full IP recon.",
  inputSchema: singleInput,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "ip_intel_lookup", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "ip_intel_lookup failed");
    return res.output;
  },
});

// =============================================================================
// ip_intel_batch — batch lookup (1-100 IPs)
// =============================================================================
const batchInput = z.object({
  ips: z.array(z.string().min(7)).min(1).max(100).describe("1-100 IPv4/IPv6 addresses"),
});

toolRegistry.register({
  name: "ip_intel_batch",
  description:
    "**Batch IP intel via ip-api.com** — same as ip_intel_lookup but for 1-100 IPs in a single call (uses /batch endpoint). Returns per-IP results plus aggregations: unique countries, unique ASNs, unique orgs, hosting/proxy/mobile counts. Strong for analyzing log files (visualize geographic distribution of an attack), bulk recon (geo-distribute a list of suspicious IPs), or comparing IP footprints across orgs. Free, no-auth.",
  inputSchema: batchInput,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "ip_intel_batch", input: i, timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "ip_intel_batch failed");
    return res.output;
  },
});
