import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["domain", "domain_history", "subdomains", "associated", "ip_neighbors", "whois_history"]).optional(),
  domain: z.string().optional(),
  ip: z.string().optional().describe("IP address for ip_neighbors mode."),
  record_type: z.enum(["a", "aaaa", "mx", "ns", "txt", "soa"]).optional().describe("DNS record type for domain_history."),
});

toolRegistry.register({
  name: "securitytrails_lookup",
  description:
    "**SecurityTrails — historical-WHOIS + DNS-history. REQUIRES SECURITYTRAILS_API_KEY ($1k+/mo).** 6 modes: domain (current DNS+WHOIS), domain_history (historical DNS by record_type), subdomains (full enumeration), associated (registrar/email-linked domains), ip_neighbors (domains on same IP), whois_history. Each output emits typed entity envelope (kind: domain | ip_address | dns_record) with first/last_seen attributes for time-series ER.",
  inputSchema: input,
  costMillicredits: 10,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "securitytrails_lookup",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "securitytrails_lookup failed");
    return res.output;
  },
});
