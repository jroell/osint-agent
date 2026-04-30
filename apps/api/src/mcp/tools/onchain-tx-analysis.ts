import { z } from "zod";
import { toolRegistry } from "./instance";
import { callGoWorker } from "../../workers/go-client";

const input = z.object({
  address: z.string().regex(/^0x[a-fA-F0-9]{40}$/, "Must be a valid 0x Ethereum address"),
  days_back: z.number().int().min(1).max(365).default(30),
  limit: z.number().int().min(1).max(200).default(25),
});

toolRegistry.register({
  name: "onchain_tx_analysis",
  description:
    "**Web3 ER chain completion** — pairs with `ens_resolve` for the full Web3 stack. Given an Ethereum address, queries `bigquery-public-data.crypto_ethereum.transactions` for: recent txs (sent/received with values+gas), top counterparties (who they transact with most), top contracts interacted (DeFi protocols/NFT marketplaces — labels for Uniswap/USDC/USDT/DAI/Seaport/ENS/Blur), aggregate stats (sent/received/net flow ETH, contract-interaction ratio). Composes: ens_resolve('vitalik.eth') → 0xABC → onchain_tx_analysis('0xABC') → 'this wallet uses Uniswap v3 daily, holds USDC, transacts with these counterparties'. Free via BigQuery.",
  inputSchema: input,
  costMillicredits: 5,
  handler: async (i, ctx) => {
    const res = await callGoWorker({
      requestId: crypto.randomUUID(), tenantId: ctx.tenantId, userId: ctx.userId,
      tool: "onchain_tx_analysis", input: i, timeoutMs: 120_000,
    });
    if (!res.ok) throw new Error(res.error?.message ?? "onchain_tx_analysis failed");
    return res.output;
  },
});
