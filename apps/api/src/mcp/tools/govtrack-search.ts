import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["bill_search", "bill_detail", "vote_recent", "member_search"])
    .optional()
    .describe(
      "bill_search: full-text bills + filters. bill_detail: by bill_id. vote_recent: recent floor votes. member_search: Senators/Reps. Auto-detects: bill_id → detail, query+name → search, state/last_name → member, else → vote_recent."
    ),
  query: z.string().optional().describe("Full-text query (bill_search), or last-name (member_search)."),
  bill_id: z.union([z.string(), z.number()]).optional().describe("GovTrack internal numeric bill id (bill_detail mode)."),
  congress: z.number().int().optional().describe("Congress number (e.g. 119 for the current Congress)."),
  current_status: z
    .string()
    .optional()
    .describe("Bill status filter (e.g. 'introduced', 'passed_house', 'enacted_signed', 'reported', 'fail_originating_chamber')."),
  chamber: z.enum(["house", "senate"]).optional().describe("Chamber filter (vote_recent)."),
  last_name: z.string().optional().describe("Member last name (member_search)."),
  state: z.string().optional().describe("US state 2-letter code filter (member_search)."),
  party: z.enum(["Democrat", "Republican", "Independent"]).optional().describe("Party filter (member_search)."),
  role_type: z.enum(["senator", "representative"]).optional().describe("Role type filter (member_search)."),
  current_only: z.boolean().optional().describe("Limit to currently-serving members (default true)."),
  order_by: z.string().optional().describe("Sort field (e.g. '-introduced_date', '-current_status_date')."),
  limit: z.number().int().min(1).max(50).optional().describe("Result limit (default 10-15 by mode, max 50)."),
});

toolRegistry.register({
  name: "govtrack_search",
  description:
    "**GovTrack — every US Congress bill, vote, and member since 1971 (free, no auth, structured JSON).** Four modes: (1) **bill_search** — full-text query + congress/status filters. Returns title, sponsor, current status with date, introduced date, GPO PDF URL, page count, USC citations parsed out of the text. Tested with 'artificial intelligence' → 465 bills in 119th Congress including VET AI Act (Sen. Hickenlooper D-CO) and Advanced AI Security Readiness Act (Sen. Todd Young R-IN); (2) **bill_detail** — by GovTrack numeric bill_id → full record; (3) **vote_recent** — most-recent floor votes with chamber/congress filter, includes related bill linkage and tally. Tested → today's Senate vote 111 on S.J.Res. 99 (immigration disapproval) rejected 47-50; (4) **member_search** — Senators/Reps by name/state/party/role. **CRITICAL**: returns the **bioguide_id** (Congressional canonical ID), **OpenSecrets osid** (for FEC/campaign finance cross-reference via `fec_donations_lookup`), C-SPAN id, Twitter handle, YouTube channel — these IDs are the cross-reference keys into the rest of the political-OSINT graph. Tested with 'schumer' → bioguide S000148, osid N00001093, @SenSchumer, current Senate Minority Leader. Pairs with `fec_donations_lookup` (campaign $ via osid), `federal_register_search` (regulations they sponsored), `propublica_nonprofit` (NGO ties).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "govtrack_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "govtrack_search failed");
    return res.output;
  },
});
