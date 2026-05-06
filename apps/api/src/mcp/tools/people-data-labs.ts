import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["person_enrich", "person_search", "company_enrich", "company_search"]).optional(),
  email: z.string().email().optional(),
  phone: z.string().optional(),
  profile: z.string().optional().describe("LinkedIn profile URL."),
  name: z.string().optional(),
  company: z.string().optional().describe("Company name (paired with name for person_enrich)."),
  person_es: z.record(z.string(), z.unknown()).optional().describe("Elasticsearch query for person_search."),
  company_website: z.string().optional(),
  company_name: z.string().optional(),
  company_linkedin: z.string().optional(),
  company_es: z.record(z.string(), z.unknown()).optional().describe("Elasticsearch query for company_search."),
  size: z.number().int().min(1).max(100).optional(),
});

toolRegistry.register({
  name: "people_data_labs",
  description:
    "**People Data Labs (PDL) — ~3B+ profiles with verified employment + education histories. REQUIRES PEOPLE_DATA_LABS_API_KEY.** 4 modes: person_enrich (by email/phone/linkedin/name+company), person_search (Elasticsearch query), company_enrich (by website/name/linkedin), company_search (ES query). Each output emits typed entity envelope (kind: person | organization) with stable PDL IDs. Closes the 'academic with this exact career path' and B2B contact-enrichment classes.",
  inputSchema: input,
  costMillicredits: 20,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "people_data_labs",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "people_data_labs failed");
    return res.output;
  },
});
