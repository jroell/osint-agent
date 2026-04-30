import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["hacks_search", "recent_hacks", "protocol", "top_tvl"])
    .optional()
    .describe(
      "hacks_search: filter the DeFi-hacks catalog (~500 entries) by chain/technique/target/date/min_amount. recent_hacks: last N days operational threat feed. protocol: fuzzy lookup by name → metadata (TVL, audits, twitter, github). top_tvl: top protocols by total value locked, optional category filter. Auto-detects: min_amount → hacks_search, category → top_tvl, query → protocol, else → recent_hacks."
    ),
  query: z
    .string()
    .optional()
    .describe("Protocol name (protocol mode) or hack name fuzzy substring (hacks_search mode)."),
  chain: z
    .string()
    .optional()
    .describe("Chain filter for hacks_search (e.g. 'Ethereum', 'BSC', 'Bitcoin', 'Solana', 'Polygon', 'Arbitrum')."),
  technique: z
    .string()
    .optional()
    .describe("Attack vector substring (e.g. 'Private Key Compromised', 'Reentrancy', 'Oracle', 'Bridge', 'Multisig', 'Flash Loan')."),
  target_type: z
    .string()
    .optional()
    .describe("Target type filter (e.g. 'CEX', 'DeFi Protocol', 'Wallet', 'Bridge', 'NFT', 'Gaming')."),
  classification: z
    .string()
    .optional()
    .describe("Classification (e.g. 'Protocol Logic', 'Infrastructure', 'Smart Contract', 'Frontend', 'Governance')."),
  min_amount: z.number().optional().describe("Minimum hack USD amount filter."),
  start_date: z.string().optional().describe("YYYY-MM-DD lower bound for hacks_search."),
  end_date: z.string().optional().describe("YYYY-MM-DD upper bound for hacks_search."),
  bridge_only: z.boolean().optional().describe("Only show bridge-hack entries."),
  days: z.number().int().min(1).max(3650).optional().describe("Days back for recent_hacks (default 90)."),
  category: z
    .string()
    .optional()
    .describe("Category filter for top_tvl (e.g. 'Dexs', 'Lending', 'Liquid Staking', 'CEX', 'Bridges', 'Yield')."),
  sort: z.enum(["amount", "date"]).optional().describe("Sort key for hacks_search (default amount)."),
  limit: z.number().int().min(1).max(200).optional().describe("Max results."),
});

toolRegistry.register({
  name: "defillama_intel",
  description:
    "**DefiLlama free no-auth crypto/DeFi intelligence — uniquely covers structured DeFi-hack catalog (506+ entries since 2020) plus 7,400+ protocol metadata records.** Four modes: (1) **hacks_search** — query the only public structured catalog of every DeFi exploit, filterable by chain/technique/target/date/min_amount/bridge-only — each entry has attack-vector taxonomy (Protocol Logic / Infrastructure / Frontend / Governance), USD loss, recovered funds, target type, language. Top hacks include LuBian ($3.5B BTC private key brute-force, 2020), Bybit ($1.4B Ethereum multisig phishing, 2025-02), Ronin Bridge ($624M, 2022); (2) **recent_hacks** — operational feed of last N days; (3) **protocol** — fuzzy name → TVL, category, chains, twitter handle, github URLs, audit count + audit links — useful for crypto-team ER and protocol vetting; (4) **top_tvl** — biggest protocols by total value locked with optional category filter. Pairs with `onchain_tx_analysis` (forensic chain analysis on attacker addresses), `ens_resolve` (ENS → address), `wikidata_lookup` (founder/team identity). 1h in-memory cache.",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "defillama_intel",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "defillama_intel failed");
    return res.output;
  },
});
