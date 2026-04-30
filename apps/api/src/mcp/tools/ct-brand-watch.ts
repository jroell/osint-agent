import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  brand: z.string().min(2).describe("Brand name to monitor (e.g. 'vurvey' or 'anthropic'). If a full domain is given, the SLD stem is used."),
  window_hours: z.number().int().min(1).max(720).default(168).describe("Lookback window in hours (default 168 = 7 days, max 720 = 30 days)"),
  limit: z.number().int().min(50).max(2000).default(200).describe("Max crt.sh entries to examine"),
  owned_apexes: z.array(z.string()).optional().describe("Apex domains the brand owns (e.g. ['vurvey.app','vurvey.com']) — these are filtered out as benign"),
});

toolRegistry.register({
  name: "ct_brand_watch",
  description:
    "**Real-time phishing/brand-impersonation detection via Certificate Transparency log monitoring.** Queries the public crt.sh CT log archive for ALL certs containing a brand name pattern. Filters to certs issued within a watch window (default 7d) and ranks each by an impersonation score that combines: Levenshtein distance from the brand stem (close lookalike = high score), cert age (fresh = scarier), suspicious tokens in subdomain (login/secure/verify/account/etc), and an owned-apex filter to exclude legit brand certs. Returns critical/high/medium/low/benign threat classification. This is how brand-protection SaaS works — but free, no API key, real-time. Pair with `typosquat_scan` (catches lookalikes pre-registration) and `favicon_pivot`/`tracker_extract` (confirms impersonation) for a 3-stage phishing defense pipeline.",
  inputSchema: input,
  costMillicredits: 4,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "ct_brand_watch",
      input: i,
      timeoutMs: 90_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "ct_brand_watch failed");
    return res.output;
  },
});
