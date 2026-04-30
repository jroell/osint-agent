import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["filings_search", "filing_detail", "contributions_search"])
    .optional()
    .describe(
      "filings_search: by registrant/client/year/period/issue_code → quarterly+annual lobbying filings. filing_detail: by UUID → full record with all activities + named lobbyists + targeted gov entities + income/expenses + filing PDF. contributions_search: lobbyist personal political contributions (HLOGA 2007-mandated)."
    ),
  query: z.string().optional().describe("Generic query — defaults to client_name match for filings_search, contributor_name for contributions_search."),
  registrant_name: z.string().optional().describe("Lobbying firm/registrant name (e.g. 'Akin Gump', 'Brownstein Hyatt')."),
  client_name: z.string().optional().describe("Paying client/corporation name (e.g. 'Anthropic', 'Microsoft')."),
  lobbyist_name: z.string().optional().describe("Search by individual lobbyist name."),
  filing_uuid: z.string().optional().describe("Filing UUID for filing_detail mode."),
  filing_year: z.number().int().optional().describe("Filing year (e.g. 2024, 2025)."),
  filing_period: z
    .enum(["first_quarter", "second_quarter", "third_quarter", "fourth_quarter", "mid_year", "year_end"])
    .optional()
    .describe("Filing period."),
  filing_type: z.string().optional().describe("Filing type (e.g. 'RR' annual, 'Q1' quarterly, 'MM' mid-year, 'TR' termination)."),
  issue_code: z
    .string()
    .optional()
    .describe("LDA general issue code or substring (e.g. 'CPI' Computer Industry, 'SCI' Science/Technology, 'TAX' Taxation, 'TRA' Transportation, 'HCR' Healthcare/HMO)."),
  government_entity: z
    .string()
    .optional()
    .describe("Government entity targeted (e.g. 'Senate', 'House of Representatives', 'White House', 'EPA')."),
  contributor_name: z.string().optional().describe("Lobbyist contributor name (contributions_search mode)."),
  limit: z.number().int().min(1).max(50).optional().describe("Result limit (default 10)."),
});

toolRegistry.register({
  name: "lda_lobbying_search",
  description:
    "**US Senate Lobbying Disclosure Act (LDA) database — every federal lobbying filing since 1995, free no-auth.** Closes the political-OSINT chain: GovTrack (bills+votes) → FEC (campaign donations) → **LDA (who paid lobbyists to push for/against bills)** → Federal Register (resulting regs) → CourtListener (court rulings on those regs). Three modes: (1) **filings_search** — by registrant (lobbying firm), client (paying corp), filing year, period, issue code (CPI/SCI/TAX/HCR/etc), targeted government entity, or lobbyist name. Each filing has activities (issue code + free-text description like 'Artificial intelligence policy'), targeted gov entities (Senate, EPA, FDA, etc.), named lobbyists with covered positions (former gov role), income or expenses (the actual $ paid). Tested with Anthropic 2024 → Rachel Appleton registered as in-house lobbyist for AI policy under CPI/SCI codes; (2) **filing_detail** — by UUID → full record including foreign entities, affiliated organizations, conviction disclosures, and direct PDF link to the original filing; (3) **contributions_search** — lobbyist personal political contributions (HLOGA 2007 mandate) — separate dataset showing what individual lobbyists donate to whom. **Why this is unique ER**: lobbying filings are the closest publicly-available record of corporate political-influence intent. Pairs with `govtrack_search` (the bills they're trying to kill/pass), `fec_donations_lookup` (campaign $ flowing to those legislators).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "lda_lobbying_search",
      input: i,
      timeoutMs: 45_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "lda_lobbying_search failed");
    return res.output;
  },
});
