import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum([
      "person_profile",
      "company_profile",
      "company_employee_count",
      "lookup_company_by_domain",
      "lookup_person_by_email",
      "person_email",
      "find_company_role",
    ])
    .optional()
    .describe("Auto-detects from inputs."),
  url: z.string().url().optional().describe("Backwards-compat: full LinkedIn person URL — auto-routes to person_profile."),
  person_url: z.string().url().optional().describe("LinkedIn /in/<slug>/ URL for person modes."),
  company_url: z.string().url().optional().describe("LinkedIn /company/<slug>/ URL."),
  domain: z.string().optional().describe("Corporate domain to resolve to a LinkedIn company URL."),
  work_email: z.string().email().optional().describe("Work email to resolve to LinkedIn profile."),
  want_email: z.boolean().optional().describe("If true with person_url, fetches verified personal email instead of profile."),
  company: z.string().optional().describe("Company name for find_company_role."),
  role: z.string().optional().describe("Role title for find_company_role (e.g. 'CTO')."),
});

toolRegistry.register({
  name: "linkedin_proxycurl",
  description:
    "**Nubela Proxycurl LinkedIn enrichment — paid, REQUIRES PROXYCURL_API_KEY ($49+/mo at nubela.co/proxycurl/pricing).** 7 modes: person_profile (full bio + experiences + education + skills), company_profile (description, industry, HQ, founded, size), company_employee_count, lookup_company_by_domain (corp domain → LI URL), lookup_person_by_email (work email → LI profile), person_email (LI URL → verified personal email), find_company_role (company+role → person at that role). Backwards-compat: `url` param auto-routes to person_profile. Each output emits typed entity envelope (kind: person | organization) with stable LinkedIn URL.",
  inputSchema: input,
  costMillicredits: 15,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "linkedin_proxycurl",
      input: i,
      timeoutMs: 60_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "linkedin_proxycurl failed");
    return res.output;
  },
});
