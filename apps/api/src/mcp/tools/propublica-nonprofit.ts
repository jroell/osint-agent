import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z.enum(["search", "org_detail"]).default("search").describe("'search' = fuzzy name search across 1.8M U.S. exempt orgs; 'org_detail' = full filing history by EIN"),
  query: z.string().min(2).describe("Org name (search mode) or EIN — 9-digit, with or without dash, e.g. '20-0097189' or '200097189' (org_detail mode)"),
  state: z.string().length(2).optional().describe("2-letter state filter (search mode only)"),
  limit: z.number().int().min(1).max(100).default(25),
});

toolRegistry.register({
  name: "propublica_nonprofit",
  description:
    "**U.S. nonprofit / NGO ER via IRS Form 990** — queries ProPublica Nonprofit Explorer (1.8M+ tax-exempt orgs). Two modes: 'search' (fuzzy name match — natural ER disambiguation: 'Mozilla' returns 6 distinct charities), 'org_detail' (by EIN — returns up to 13 years of Form 990 filings with revenue, assets, expenses, officer compensation, grants made, payroll tax, plus PDF links). PDF links resolve to actual 990 forms which list individual board members and officers. Use cases: 'is this person a board member of any 501(c)(3)?', 'what's the executive compensation at org X?', 'when was this charity founded?', 'find all charities in state Y under NTEE code Z'. Free, no auth.",
  inputSchema: input,
  costMillicredits: 3,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "propublica_nonprofit", input: i, timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "propublica_nonprofit failed");
    return res.output;
  },
});
