import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  mode: z
    .enum(["search", "coin_detail", "top_markets"])
    .optional()
    .describe(
      "search: query → coins/exchanges/categories. coin_detail: by CoinGecko coin id → full record. top_markets: top N by market cap. Auto-detects: coin_id present → coin_detail, query → search, else → top_markets."
    ),
  query: z.string().optional().describe("Free-text query (search mode)."),
  coin_id: z
    .string()
    .optional()
    .describe(
      "CoinGecko coin id (e.g. 'bitcoin', 'ethereum', 'solana', 'usd-coin'). Use search mode first to resolve a name to its CoinGecko id."
    ),
  category: z
    .string()
    .optional()
    .describe("Category filter for top_markets (e.g. 'layer-1', 'defi', 'meme-token', 'stablecoins')."),
  limit: z.number().int().min(1).max(250).optional().describe("Max results (default 10 search / 25 markets)."),
});

toolRegistry.register({
  name: "coingecko_search",
  description:
    "**CoinGecko — crypto market data, free no-auth (rate limit ~10-30 req/min on free tier).** Closes the crypto-OSINT chain alongside `defillama_intel` (TVL/hacks/protocol metadata), `onchain_tx_analysis` (Etherscan-style chain forensics), and `ens_resolve`. Three modes: (1) **search** — query → matching coins (with market_cap_rank + thumbnails) + exchanges + categories. (2) **coin_detail** — by coin id → full record: market data (price USD, market cap, 24h/7d/30d % change, total volume, ATH date + ATH change %, ATL), supply (circulating/total/max), cross-platform identity (homepage, whitepaper PDF, twitter handle, github repos, subreddit, official forum, blockchain explorer URLs), developer activity (stars, forks, PRs merged, contributors, commits last 4 weeks — proxies for project liveness), hashing algorithm, genesis date, country origin. Tested Bitcoin → $76,156, market cap $1.53T, 73k GitHub stars, 11,215 PRs merged, 108 contributors. (3) **top_markets** — top N by market cap with optional category filter (layer-1, defi, meme-token, stablecoins, etc.).",
  inputSchema: input,
  costMillicredits: 1,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(),
      tenantId: ctx.tenantId,
      userId: ctx.userId,
      tool: "coingecko_search",
      input: i,
      timeoutMs: 30_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "coingecko_search failed");
    return res.output;
  },
});
