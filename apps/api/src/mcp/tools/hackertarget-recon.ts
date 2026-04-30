import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(3).describe("Domain or IP to recon (e.g. 'vurvey.app' or '104.26.5.89')"),
  ops: z.array(z.enum(["hostsearch", "dnshost", "reverseiplookup", "aslookup", "whois"])).optional()
    .describe("Operations to run in parallel. Defaults to all 5. hostsearch=passive subdomains+IPs, reverseiplookup=co-tenanted domains on same IP (THE classic ER pivot), dnshost=full DNS records, aslookup=ASN info, whois=registrar metadata."),
});

toolRegistry.register({
  name: "hackertarget_recon",
  description:
    "**Multi-endpoint OSINT recon staple.** Free, no-auth wrapper for hackertarget.com's 5 endpoints (hostsearch, dnshost, reverseiplookup, aslookup, whois) — runs them in parallel. Most valuable: `reverseiplookup` returns ALL OTHER domains hosted on the same IP — classic shared-hosting pivot for finding sister sites of small operators. `hostsearch` is one of the best free passive subdomain enumerators. Pairs with shodan_internetdb (per-IP fingerprint) and alienvault_otx_passive_dns (passive DNS shadow) for a 3-way infrastructure-recon stack. Free tier: 100 req/day per source IP.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "hackertarget_recon",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "hackertarget_recon failed");
    return res.output;
  },
});
