import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  domain: z.string().min(3).describe("Apex domain (e.g. 'vurvey.app')"),
  max_spf_depth: z.number().int().min(1).max(10).default(5).describe("Max recursion depth for SPF include: chain expansion (RFC 7208 caps at 10 lookups; we default to 5)"),
  probe_dkim: z.boolean().default(true).describe("Probe ~30 common DKIM selectors at <selector>._domainkey.<domain>. Adds ~3s but reveals vendor stack."),
});

toolRegistry.register({
  name: "spf_dmarc_chain",
  description:
    "**Email-infrastructure operator-binding fingerprint via DNS — pure DNS, no rate limits, no API keys.** Recursively expands a domain's SPF record's `include:` directives (catches the full vendor stack: Google Workspace + SendGrid + Mailgun etc.), resolves DMARC policy + reporting addresses, and probes ~30 common DKIM selectors. Returns: full SPF tree, DMARC policy + RUA/RUF reporting addresses (often INTERNAL security emails — strong identity signal), MX records, found DKIM selectors with vendor hints, and a sortable `operator_fingerprint` string for cross-domain ER comparison. Two domains with identical fingerprints are run by the same email team. Email-side equivalent of `tracker_correlate` (web-side) — together they're the strongest non-WHOIS operator-binding signal in OSINT.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "spf_dmarc_chain",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "spf_dmarc_chain failed");
    return res.output;
  },
});
