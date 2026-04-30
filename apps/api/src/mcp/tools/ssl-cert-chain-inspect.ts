import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  target: z.string().min(3).describe("Host or host:port (e.g. 'vurvey.app' or 'api.example.com:8443')"),
  server_name: z.string().optional().describe("Override SNI ServerName (rare — use when host doesn't match cert SAN)"),
});

toolRegistry.register({
  name: "ssl_cert_chain_inspect",
  description:
    "**Live TLS handshake recon — pure Go, no external API.** Performs a real TLS handshake to extract leaf + intermediate + root cert chain. Per cert: subject + issuer + SAN list + validity dates + days-until-expiry + IsCA + signature/key algorithms + SHA-256/SHA-1 fingerprints + OCSP/CRL endpoints + self-signed detection. Reveals: TLS version + cipher suite + ALPN + OCSP stapling status. Pivots: same SHA-256 fingerprint across multiple domains = SAME CERT (cross-domain operator binding). Issuer org reveals operational maturity (Let's Encrypt vs DigiCert vs internal CA). Wildcard SANs reveal blast radius. Pairs with `cert_transparency` (CT log historical) and `favicon_pivot` (favicon ER).",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "ssl_cert_chain_inspect", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "ssl_cert_chain_inspect failed");
    return res.output;
  },
});
