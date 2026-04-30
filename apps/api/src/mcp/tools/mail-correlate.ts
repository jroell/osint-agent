import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  domain_a: z.string().min(3).describe("First apex domain"),
  domain_b: z.string().min(3).describe("Second apex domain"),
  probe_dkim: z.boolean().default(true).describe("Probe ~30 DKIM selectors per domain (adds ~3s but reveals vendor stack overlap)"),
});

toolRegistry.register({
  name: "mail_correlate",
  description:
    "**Email-side companion to `tracker_correlate` — 'are these two domains run by the same email/IT team?'** Runs `spf_dmarc_chain` on both domains in parallel, computes overlap on every layer (SPF expanded includes, email vendors, MX hostnames+apexes, DKIM selectors, DMARC reporting addresses), returns a verdict: `same` (DMARC rua/ruf match — smoking gun, OR exact fingerprint match), `likely-same` (3+ overlapping layers with shared MX), `shared-vendor` (same vendor stack only — common SaaS), `unrelated`, or `inconclusive`. The DMARC `rua=` overlap is THE strongest signal — companies put internal security emails there expecting nobody to read them. Pair with `tracker_correlate` (web side) for full operator-binding coverage.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "mail_correlate",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "mail_correlate failed");
    return res.output;
  },
});
