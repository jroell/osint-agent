import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["lookup_cves", "top", "time_series"])
    .optional()
    .describe(
      "lookup_cves: 1-100 CVE IDs → EPSS records. top: highest-EPSS CVEs globally. time_series: historical EPSS for one CVE (track velocity). Auto-detects: cves array → lookup_cves, single cve → time_series, neither → top."
    ),
  cves: z
    .union([z.array(z.string()), z.string()])
    .optional()
    .describe("Array of CVE IDs (e.g. ['CVE-2024-1708','CVE-2025-24813']) or comma-separated string. Required for lookup_cves mode (1-100 CVEs)."),
  cve: z
    .string()
    .optional()
    .describe("Single CVE ID for time_series mode (e.g. 'CVE-2024-1708')."),
  limit: z
    .number()
    .int()
    .min(1)
    .max(200)
    .optional()
    .describe("Result limit for top mode (default 20, max 200)."),
  epss_min: z
    .number()
    .min(0)
    .max(1)
    .optional()
    .describe("Minimum EPSS threshold for top mode (e.g. 0.7 for ≥70% probability filter)."),
});

toolRegistry.register({
  name: "epss_score",
  description:
    "**EPSS (Exploit Prediction Scoring System) — first.org's actuarial CVE prioritization (free, no-auth, daily updates).** EPSS is the only public dataset that gives a *probability* (0–1) that a given CVE will be exploited in the wild within the next 30 days, plus its percentile rank against the entire CVE corpus. **Why this is THE complement to `cisa_kev_lookup`**: KEV says 'this is being exploited NOW (binary flag)'; EPSS says 'probability X% it will be exploited soon (continuous, ML-derived)'. Together they enable full prioritization: KEV+EPSS>0.9 = CRITICAL fire-now; KEV=false+EPSS>0.9 = LEADING INDICATOR (patch before it lands in KEV); KEV=true+EPSS<0.5 = exploited but losing momentum. Three modes: lookup_cves (1-100 CVE IDs → records with severity buckets), top (highest-EPSS CVEs globally — what's everyone exploiting right now), time_series (historical EPSS for one CVE — track velocity, EPSS rising fast is a red flag). Auto-buckets: 🔥 critical (>0.9), ⚠️ high (0.5-0.9), 🟡 medium (0.1-0.5), ⚪ low (≤0.1). Pairs with `cisa_kev_lookup`, `osv_vuln_search`, `cve_intel_chain`, `shodan` for full vuln triangulation.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "epss_score",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "epss_score failed");
    return res.output;
  },
});
