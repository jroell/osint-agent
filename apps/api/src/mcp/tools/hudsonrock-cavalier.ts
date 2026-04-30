import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  query: z
    .string()
    .min(2)
    .describe("The email address, corporate domain, or username to check."),
  mode: z
    .enum(["email", "domain", "username"])
    .optional()
    .describe(
      "Lookup mode. If omitted, auto-detects: contains '@' → email, has '.' → domain, otherwise → username."
    ),
});

toolRegistry.register({
  name: "hudsonrock_cavalier",
  description:
    "**Hudson Rock Cavalier — infostealer-log OSINT lookup (35M+ stealer logs, FREE no-auth).** Three modes: (1) email — returns infostealer infections containing this email in the saved-credentials list (per-infection: computer name, OS, IP prefix, malware path, antivirus, top stolen passwords/logins, total corporate vs user services compromised on that machine, date compromised); (2) domain — returns aggregate stats: total stealer logs touching the domain, # of employees / users / third-parties with stolen creds, plus TOP COMPROMISED LOGIN ENDPOINTS (e.g. 'idmsa.apple.com/appleauth/auth/signin' — 356 employee compromises) showing exactly which corporate URLs were targeted; (3) username — same as email but searches by username field. **WHY THIS IS UNIQUE**: this signal otherwise costs $thousands/month from breach-data vendors (Recorded Future, Flashpoint, KELA). Hudson Rock surfaces it free as enterprise lead-gen. Use cases: validate an email is genuinely high-risk, score corporate breach exposure, find compromised employee endpoints, attribute infrastructure to compromised credentials. Pairs with `ip_intel_lookup` (verify suspicious IP) and `panel-consult` for triage. Star-redacted passwords/logins/IPs (per legal requirements) but full metadata.",
  inputSchema: input,
  costMillicredits: 2,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "hudsonrock_cavalier",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "hudsonrock_cavalier failed");
    return res.output;
  },
});
