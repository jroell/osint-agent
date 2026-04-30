import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["lookup", "package", "commit"]).default("lookup"),
  query: z.string().min(2).describe("Vuln ID for 'lookup' (CVE-2021-44228 / GHSA-jfh8-c2jp-5v3q / PYSEC-2021-NN); package name for 'package' (with input.ecosystem); git commit SHA for 'commit'"),
  ecosystem: z.string().optional().describe("Required for 'package' mode: 'npm', 'PyPI', 'Go', 'crates.io', 'Maven', 'RubyGems', 'Packagist', 'Pub', 'NuGet', 'Hex', 'Hackage', etc."),
  version: z.string().optional().describe("Optional package version filter for 'package' mode"),
  limit: z.number().int().min(1).max(100).default(25),
});

toolRegistry.register({
  name: "osv_vuln_search",
  description:
    "**OSV (Open Source Vulnerability) database search** — queries api.osv.dev (Google's free OSV.dev). 3 modes: 'lookup' (direct vulnerability ID — CVE/GHSA/PYSEC/RUSTSEC/MAL/GO), 'package' (every vuln affecting a specific package — npm/PyPI/Go/Cargo/Maven/RubyGems/etc.), 'commit' (every vuln tracked at a specific git commit hash — unique feature). Distinct from `cve_intel_chain` (which uses NVD/EPSS/KEV for system CVE prioritization) — OSV is the **canonical PACKAGE-ECOSYSTEM** vulnerability database that aggregates GitHub Advisory Database + Linux distros + every major package manager. Returns: vulnerability records with aliases, summary, details, modified/published dates, affected packages, severity (CVSS V3 + label), references, CWE IDs, GitHub-reviewed flag. Aggregations: top ecosystems, severity breakdown, unique aliases. Use cases: software supply-chain ER, dependency audit, commit-hash forensics. Free, no auth.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "osv_vuln_search", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "osv_vuln_search failed");
    return res.output;
  },
});
