import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["lookup_cve", "search_product", "recent", "ransomware"])
    .optional()
    .describe(
      "lookup_cve: single CVE-ID → KEV record (or null if not in KEV). search_product: vendor/product fuzzy match → all KEV entries. recent: most recent additions (last N days). ransomware: KEV entries flagged for known ransomware campaign use. Auto-detects: cve_id present → lookup_cve, query present → search_product, else → recent."
    ),
  cve_id: z
    .string()
    .optional()
    .describe("CVE identifier (e.g. 'CVE-2024-1708'). Required for lookup_cve mode."),
  query: z
    .string()
    .optional()
    .describe("Vendor or product fuzzy substring (case-insensitive). Required for search_product mode (e.g. 'ConnectWise', 'Apache', 'Microsoft Windows', 'iOS')."),
  days: z
    .number()
    .int()
    .min(1)
    .max(365)
    .optional()
    .describe("How many days back to scan in recent mode (default 14, max 365)."),
  limit: z
    .number()
    .int()
    .min(1)
    .max(1000)
    .optional()
    .describe("Max entries to return (default 50 for search_product, 100 for ransomware)."),
});

toolRegistry.register({
  name: "cisa_kev_lookup",
  description:
    "**CISA Known Exploited Vulnerabilities (KEV) catalog — federal-source list of CVEs actively exploited in the wild.** Free, no auth, ~1500 entries, updated daily. Each entry carries unique federal-only fields beyond OSV/NVD: (a) `dueDate` — federal civilian agencies must patch by this date under BOD 22-01 (anything past dueDate is officially overdue and surfaced with `is_overdue` + `days_overdue`); (b) `knownRansomwareCampaignUse` flag distinguishes APT/nation-state exploitation from commodity ransomware; (c) `requiredAction` — official mitigation guidance. **Why this is unique**: OSV/NVD tell you a vulnerability EXISTS; KEV tells you it's WEAPONIZED right now. Pairs with `osv_vuln_search`, `shodan`, `cve_intel_chain` for full triangulation. Four modes: lookup_cve (single CVE → KEV record), search_product (vendor/product → all matches), recent (last N days additions — operational ransomware feed), ransomware (all KEV entries flagged for ransomware campaign use, with top-targeted vendor breakdown). 6h in-memory cache.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "cisa_kev_lookup",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "cisa_kev_lookup failed");
    return res.output;
  },
});
