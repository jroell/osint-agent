import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  ip: z.string().optional().describe("Single IPv4 address to query"),
  ips: z.array(z.string()).optional().describe("Up to 50 IPv4 addresses (parallel fetch, concurrency capped at 8)"),
  domain: z.string().optional().describe("Hostname; resolves A records first, then enriches each IP"),
}).refine((d) => d.ip || (d.ips?.length ?? 0) > 0 || d.domain, {
  message: "Provide one of: 'ip', 'ips', or 'domain'",
});

toolRegistry.register({
  name: "shodan_internetdb",
  description:
    "**Free, no-auth Shodan-grade IP fingerprinting.** Queries Shodan's free InternetDB API (https://internetdb.shodan.io) — no key required, no rate limit beyond reasonable use. For each IP returns: open ports, CPE software fingerprints (e.g. cpe:/a:nginx:nginx:1.18.0), Shodan tags (cdn/vpn/self-signed/honeypot/etc.), CVE IDs of known vulnerabilities, and reverse DNS hostnames. Pairs naturally with passive DNS / subfinder output: given a domain, resolves A records and enriches each IP. The 'what's actually running on this infrastructure?' primitive — a free Shodan-grade fingerprint without burning paid credits.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "shodan_internetdb",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "shodan_internetdb failed");
    return res.output;
  },
});
