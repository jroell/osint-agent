package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type OnchainTx struct {
	BlockTimestamp string  `json:"block_timestamp"`
	From           string  `json:"from"`
	To             string  `json:"to"`
	ValueETH       float64 `json:"value_eth"`
	GasPriceGwei   float64 `json:"gas_price_gwei"`
	Direction      string  `json:"direction"` // sent | received | self
	Hash           string  `json:"tx_hash,omitempty"`
}

type OnchainCounterparty struct {
	Address       string `json:"address"`
	TxCount       int    `json:"tx_count"`
	TotalValueETH float64 `json:"total_value_eth"`
	Direction     string `json:"direction"` // sent_to | received_from
}

type OnchainContract struct {
	ContractAddress string `json:"contract_address"`
	InteractionCount int    `json:"interaction_count"`
	KnownLabel      string `json:"known_label,omitempty"` // e.g. "Uniswap V2 Router", "OpenSea"
}

type OnchainTxAnalysisOutput struct {
	Address           string                `json:"address"`
	StartDate         string                `json:"start_date"`
	EndDate           string                `json:"end_date"`
	TxCount           int                   `json:"tx_count_in_window"`
	FirstTx           string                `json:"first_tx_in_window,omitempty"`
	LastTx            string                `json:"last_tx_in_window,omitempty"`
	TotalSentETH      float64               `json:"total_sent_eth"`
	TotalReceivedETH  float64               `json:"total_received_eth"`
	NetFlowETH        float64               `json:"net_flow_eth"`
	RecentTxs         []OnchainTx           `json:"recent_txs"`
	TopCounterparties []OnchainCounterparty `json:"top_counterparties"`
	TopContracts      []OnchainContract     `json:"top_contracts_interacted"`
	ContractInteractionRatio float64        `json:"contract_interaction_ratio"` // % of txs that called contracts
	HighlightFindings []string              `json:"highlight_findings"`
	Source            string                `json:"source"`
	TookMs            int64                 `json:"tookMs"`
	Note              string                `json:"note,omitempty"`
}

// Known Ethereum contract address labels — small curated list of high-impact ones.
var knownContractLabels = map[string]string{
	"0x7a250d5630b4cf539739df2c5dacb4c659f2488d": "Uniswap V2 Router",
	"0xe592427a0aece92de3edee1f18e0157c05861564": "Uniswap V3 Router",
	"0x68b3465833fb72a70ecdf485e0e4c7bd8665fc45": "Uniswap V3 Router 2",
	"0x00000000219ab540356cbb839cbe05303d7705fa": "ETH 2.0 Beacon Deposit",
	"0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2": "WETH (Wrapped Ether)",
	"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": "USDC",
	"0xdac17f958d2ee523a2206206994597c13d831ec7": "USDT",
	"0x6b175474e89094c44da98b954eedeac495271d0f": "DAI",
	"0x00000000006c3852cbef3e08e8df289169ede581": "Seaport (OpenSea)",
	"0x7f268357a8c2552623316e2562d90e642bb538e5": "OpenSea Wyvern V2",
	"0x39da41747a83aee658334415666f3ef92dd0d541": "Blur",
	"0x00000000000001ad428e4906ae43d8f9852d0dd6": "Seaport 1.4",
	"0xae0ee0a63a2ce6baeeffe56e7714fb4efe48d419": "ENS Public Resolver",
	"0x57f1887a8bf19b14fc0df6fd9b2acc9af147ea85": "ENS NameWrapper",
}

func ethValueToFloat(weiStr string) float64 {
	// Wei is a string due to BQ NUMERIC. 1 ETH = 10^18 wei. We can lose precision
	// at the high end but for analysis-grade this is fine.
	if weiStr == "" {
		return 0
	}
	if len(weiStr) <= 18 {
		var n float64
		fmt.Sscanf(weiStr, "%f", &n)
		return n / 1e18
	}
	// Use string slicing for big numbers
	intPart := weiStr[:len(weiStr)-18]
	decPart := weiStr[len(weiStr)-18:]
	combined := intPart + "." + decPart
	var n float64
	fmt.Sscanf(combined, "%f", &n)
	return n
}

func gweiToFloat(weiStr string) float64 {
	if weiStr == "" {
		return 0
	}
	var n float64
	fmt.Sscanf(weiStr, "%f", &n)
	return n / 1e9
}

// OnchainTxAnalysis queries the BigQuery Ethereum public dataset for a wallet
// address, returning recent transactions, top counterparties, top contracts,
// and aggregate stats over a date window.
//
// Pairs with iter-34's `ens_resolve` to complete the Web3 ER stack:
//   ens_resolve("vitalik.eth") → wallet 0xABC
//   onchain_tx_analysis("0xABC") → tx history, counterparties, contracts
//
// CAVEAT: queries the `transactions` table which is ~5TB total. We
// constrain by `block_timestamp` partition + address filter to keep cost
// reasonable (~5-15GB per query in 7-day window).
func OnchainTxAnalysis(ctx context.Context, input map[string]any) (*OnchainTxAnalysisOutput, error) {
	addr, _ := input["address"].(string)
	addr = strings.ToLower(strings.TrimSpace(addr))
	if addr == "" {
		return nil, errors.New("input.address required (Ethereum 0x address)")
	}
	if !strings.HasPrefix(addr, "0x") || len(addr) != 42 {
		return nil, errors.New("invalid Ethereum address (must be 0x followed by 40 hex chars)")
	}
	safeAddr := strings.ReplaceAll(addr, "'", "")

	daysBack := 30
	if v, ok := input["days_back"].(float64); ok && int(v) > 0 && int(v) <= 365 {
		daysBack = int(v)
	}
	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 200 {
		limit = int(v)
	}

	now := time.Now().UTC()
	endDate := now
	startDate := endDate.AddDate(0, 0, -(daysBack - 1))
	startStr := startDate.Format("2006-01-02")
	endStr := endDate.Format("2006-01-02")

	timeFilter := fmt.Sprintf(`block_timestamp >= TIMESTAMP('%s 00:00:00')
  AND block_timestamp <= TIMESTAMP('%s 23:59:59')`, startStr, endStr)

	start := time.Now()
	out := &OnchainTxAnalysisOutput{
		Address: addr, StartDate: startStr, EndDate: endStr,
		Source: "bigquery-public-data.crypto_ethereum",
	}

	// 1. Aggregate stats + recent txs
	statsSQL := fmt.Sprintf(`
SELECT
  COUNT(*) AS total_count,
  MIN(block_timestamp) AS first_tx,
  MAX(block_timestamp) AS last_tx,
  SUM(IF(LOWER(from_address) = '%s', SAFE_CAST(value AS NUMERIC), 0)) AS sent_wei,
  SUM(IF(LOWER(to_address) = '%s', SAFE_CAST(value AS NUMERIC), 0)) AS received_wei,
  COUNTIF(input != '0x' AND input IS NOT NULL) AS contract_call_count
FROM `+"`bigquery-public-data.crypto_ethereum.transactions`"+`
WHERE %s
  AND (LOWER(from_address) = '%s' OR LOWER(to_address) = '%s')`,
		safeAddr, safeAddr, timeFilter, safeAddr, safeAddr)
	stats, err := bqQuery(ctx, statsSQL, 1)
	if err != nil {
		return nil, fmt.Errorf("stats query: %w", err)
	}
	if len(stats) > 0 {
		s := stats[0]
		out.TxCount = parseBQInt(s["total_count"])
		if v, ok := s["first_tx"].(string); ok {
			out.FirstTx = v
		}
		if v, ok := s["last_tx"].(string); ok {
			out.LastTx = v
		}
		if v, ok := s["sent_wei"].(string); ok {
			out.TotalSentETH = ethValueToFloat(v)
		}
		if v, ok := s["received_wei"].(string); ok {
			out.TotalReceivedETH = ethValueToFloat(v)
		}
		out.NetFlowETH = round1(out.TotalReceivedETH - out.TotalSentETH)
		out.TotalSentETH = round1(out.TotalSentETH)
		out.TotalReceivedETH = round1(out.TotalReceivedETH)
		contractCalls := parseBQInt(s["contract_call_count"])
		if out.TxCount > 0 {
			out.ContractInteractionRatio = float64(contractCalls) / float64(out.TxCount)
		}
	}

	// 2. Recent txs
	txSQL := fmt.Sprintf(`
SELECT block_timestamp, from_address, to_address, value, gas_price, hash
FROM `+"`bigquery-public-data.crypto_ethereum.transactions`"+`
WHERE %s
  AND (LOWER(from_address) = '%s' OR LOWER(to_address) = '%s')
ORDER BY block_timestamp DESC LIMIT %d`, timeFilter, safeAddr, safeAddr, limit)
	txs, err := bqQuery(ctx, txSQL, limit)
	if err == nil {
		for _, r := range txs {
			tx := OnchainTx{}
			if v, ok := r["block_timestamp"].(string); ok {
				tx.BlockTimestamp = v
			}
			if v, ok := r["from_address"].(string); ok {
				tx.From = v
			}
			if v, ok := r["to_address"].(string); ok {
				tx.To = v
			}
			if v, ok := r["hash"].(string); ok {
				tx.Hash = v
			}
			if v, ok := r["value"].(string); ok {
				tx.ValueETH = ethValueToFloat(v)
			}
			if v, ok := r["gas_price"].(string); ok {
				tx.GasPriceGwei = round1(gweiToFloat(v))
			}
			tx.ValueETH = round1(tx.ValueETH * 1000) / 1000
			fl := strings.ToLower(tx.From)
			tl := strings.ToLower(tx.To)
			switch {
			case fl == addr && tl == addr:
				tx.Direction = "self"
			case fl == addr:
				tx.Direction = "sent"
			case tl == addr:
				tx.Direction = "received"
			}
			out.RecentTxs = append(out.RecentTxs, tx)
		}
	}

	// 3. Top counterparties
	cpSQL := fmt.Sprintf(`
SELECT counterparty, SUM(c) AS tx_count, SUM(v) AS total_wei, MAX(direction) AS direction FROM (
  SELECT
    LOWER(to_address) AS counterparty, COUNT(*) AS c, SUM(SAFE_CAST(value AS NUMERIC)) AS v,
    'sent_to' AS direction
  FROM `+"`bigquery-public-data.crypto_ethereum.transactions`"+`
  WHERE %s AND LOWER(from_address) = '%s' AND LOWER(to_address) != '%s'
  GROUP BY counterparty
  UNION ALL
  SELECT
    LOWER(from_address) AS counterparty, COUNT(*) AS c, SUM(SAFE_CAST(value AS NUMERIC)) AS v,
    'received_from' AS direction
  FROM `+"`bigquery-public-data.crypto_ethereum.transactions`"+`
  WHERE %s AND LOWER(to_address) = '%s' AND LOWER(from_address) != '%s'
  GROUP BY counterparty
)
GROUP BY counterparty ORDER BY tx_count DESC LIMIT 15`,
		timeFilter, safeAddr, safeAddr, timeFilter, safeAddr, safeAddr)
	cps, err := bqQuery(ctx, cpSQL, 15)
	if err == nil {
		for _, r := range cps {
			cp := OnchainCounterparty{}
			if v, ok := r["counterparty"].(string); ok {
				cp.Address = v
			}
			cp.TxCount = parseBQInt(r["tx_count"])
			if v, ok := r["total_wei"].(string); ok {
				cp.TotalValueETH = round1(ethValueToFloat(v) * 1000) / 1000
			}
			if v, ok := r["direction"].(string); ok {
				cp.Direction = v
			}
			out.TopCounterparties = append(out.TopCounterparties, cp)
		}
	}

	// 4. Top contracts (txs where input != "0x" — i.e. contract calls)
	contractsSQL := fmt.Sprintf(`
SELECT LOWER(to_address) AS contract, COUNT(*) AS c
FROM `+"`bigquery-public-data.crypto_ethereum.transactions`"+`
WHERE %s
  AND LOWER(from_address) = '%s'
  AND input != '0x'
  AND input IS NOT NULL
GROUP BY contract
ORDER BY c DESC LIMIT 10`, timeFilter, safeAddr)
	contracts, err := bqQuery(ctx, contractsSQL, 10)
	if err == nil {
		for _, r := range contracts {
			cn := OnchainContract{}
			if v, ok := r["contract"].(string); ok {
				cn.ContractAddress = v
				if lbl, ok := knownContractLabels[v]; ok {
					cn.KnownLabel = lbl
				}
			}
			cn.InteractionCount = parseBQInt(r["c"])
			out.TopContracts = append(out.TopContracts, cn)
		}
	}

	// Highlights
	highlights := []string{}
	highlights = append(highlights, fmt.Sprintf("%d txs in %s → %s window", out.TxCount, out.StartDate, out.EndDate))
	if out.TxCount > 0 {
		highlights = append(highlights, fmt.Sprintf("net flow: %.3f ETH (sent=%.3f received=%.3f)", out.NetFlowETH, out.TotalSentETH, out.TotalReceivedETH))
		highlights = append(highlights, fmt.Sprintf("contract-interaction ratio: %.0f%% (%.0f%% pure transfers)", out.ContractInteractionRatio*100, (1-out.ContractInteractionRatio)*100))
	}
	if len(out.TopContracts) > 0 {
		labeled := []string{}
		for _, c := range out.TopContracts {
			if c.KnownLabel != "" && len(labeled) < 3 {
				labeled = append(labeled, fmt.Sprintf("%s(×%d)", c.KnownLabel, c.InteractionCount))
			}
		}
		if len(labeled) > 0 {
			highlights = append(highlights, "known contracts used: "+strings.Join(labeled, ", "))
		}
	}
	if len(out.TopCounterparties) > 0 {
		top := out.TopCounterparties[0]
		highlights = append(highlights, fmt.Sprintf("top counterparty: %s (%d txs %s, %.3f ETH)", top.Address[:14]+"...", top.TxCount, top.Direction, top.TotalValueETH))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
