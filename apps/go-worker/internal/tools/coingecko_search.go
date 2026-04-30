package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CoinGeckoSearch wraps CoinGecko's free no-auth public API.
// Closes the crypto-OSINT chain alongside `defillama_intel` (TVL/hacks/
// protocol metadata), `onchain_tx_analysis` (Etherscan-style chain
// forensics), and `ens_resolve` (ENS → address).
//
// Three modes:
//
//   - "search"        : query → matching coins (with market_cap_rank +
//                        thumbnails) + exchanges + categories.
//   - "coin_detail"   : by CoinGecko coin ID (e.g. "bitcoin", "ethereum")
//                        → full record: market data (price, market cap,
//                        24h change, total volume, ATH/ATL), links
//                        (homepage, whitepaper, twitter, github,
//                        subreddit), developer activity (stars, forks,
//                        PRs merged, contributors — useful for "is this
//                        project actually active?"), hashing algorithm,
//                        genesis date, country of origin.
//   - "top_markets"   : top N by market cap with optional category filter.

type CoinGeckoMatch struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	MarketCapRank int    `json:"market_cap_rank,omitempty"`
	Thumb         string `json:"thumb,omitempty"`
}

type CoinGeckoCoinDetail struct {
	ID                 string   `json:"id"`
	Symbol             string   `json:"symbol"`
	Name               string   `json:"name"`
	HashingAlgorithm   string   `json:"hashing_algorithm,omitempty"`
	GenesisDate        string   `json:"genesis_date,omitempty"`
	CountryOrigin      string   `json:"country_origin,omitempty"`
	Categories         []string `json:"categories,omitempty"`
	Description        string   `json:"description_excerpt,omitempty"`

	// Market data
	PriceUSD           float64 `json:"price_usd,omitempty"`
	MarketCapUSD       float64 `json:"market_cap_usd,omitempty"`
	MarketCapRank      int     `json:"market_cap_rank,omitempty"`
	TotalVolumeUSD     float64 `json:"total_volume_usd,omitempty"`
	ATHUSD             float64 `json:"ath_usd,omitempty"`
	ATHDate            string  `json:"ath_date,omitempty"`
	ATHChangePct       float64 `json:"ath_change_pct,omitempty"`
	ATLUSD             float64 `json:"atl_usd,omitempty"`
	ATLDate            string  `json:"atl_date,omitempty"`
	PriceChange24hPct  float64 `json:"price_change_24h_pct,omitempty"`
	PriceChange7dPct   float64 `json:"price_change_7d_pct,omitempty"`
	PriceChange30dPct  float64 `json:"price_change_30d_pct,omitempty"`
	CirculatingSupply  float64 `json:"circulating_supply,omitempty"`
	TotalSupply        float64 `json:"total_supply,omitempty"`
	MaxSupply          float64 `json:"max_supply,omitempty"`

	// Cross-platform links (canonical web identity for the project)
	Homepage           string   `json:"homepage,omitempty"`
	Whitepaper         string   `json:"whitepaper,omitempty"`
	Twitter            string   `json:"twitter,omitempty"`
	GitHubRepos        []string `json:"github_repos,omitempty"`
	Subreddit          string   `json:"subreddit,omitempty"`
	OfficialForumURL   string   `json:"official_forum_url,omitempty"`
	BlockchainSites    []string `json:"blockchain_explorer_urls,omitempty"`

	// Developer activity (public GitHub signals)
	GithubForks        int `json:"github_forks,omitempty"`
	GithubStars        int `json:"github_stars,omitempty"`
	GithubSubscribers  int `json:"github_subscribers,omitempty"`
	TotalIssues        int `json:"github_total_issues,omitempty"`
	ClosedIssues       int `json:"github_closed_issues,omitempty"`
	PullRequestsMerged int `json:"pull_requests_merged,omitempty"`
	Contributors       int `json:"contributors,omitempty"`
	Commits4w          int `json:"commits_last_4_weeks,omitempty"`
}

type CoinGeckoMarketRow struct {
	Rank              int     `json:"market_cap_rank"`
	ID                string  `json:"id"`
	Symbol            string  `json:"symbol"`
	Name              string  `json:"name"`
	PriceUSD          float64 `json:"price_usd"`
	MarketCapUSD      float64 `json:"market_cap_usd"`
	TotalVolumeUSD    float64 `json:"total_volume_usd,omitempty"`
	PriceChange24hPct float64 `json:"price_change_24h_pct,omitempty"`
	ATHChangePct      float64 `json:"ath_change_pct,omitempty"`
}

type CoinGeckoSearchOutput struct {
	Mode              string                 `json:"mode"`
	Query             string                 `json:"query,omitempty"`
	Returned          int                    `json:"returned"`

	Coins             []CoinGeckoMatch       `json:"coins,omitempty"`
	Detail            *CoinGeckoCoinDetail   `json:"detail,omitempty"`
	Markets           []CoinGeckoMarketRow   `json:"markets,omitempty"`
	Categories        []string               `json:"categories,omitempty"`
	Exchanges         []string               `json:"exchanges,omitempty"`

	HighlightFindings []string               `json:"highlight_findings"`
	Source            string                 `json:"source"`
	TookMs            int64                  `json:"tookMs"`
	Note              string                 `json:"note,omitempty"`
}

func CoinGeckoSearch(ctx context.Context, input map[string]any) (*CoinGeckoSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["coin_id"]; ok {
			mode = "coin_detail"
		} else if _, ok := input["query"]; ok {
			mode = "search"
		} else {
			mode = "top_markets"
		}
	}

	out := &CoinGeckoSearchOutput{
		Mode:   mode,
		Source: "api.coingecko.com/api/v3",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "search":
		query, _ := input["query"].(string)
		query = strings.TrimSpace(query)
		if query == "" {
			return nil, fmt.Errorf("input.query required for search mode")
		}
		out.Query = query
		params := url.Values{}
		params.Set("query", query)
		body, err := coingeckoGet(ctx, cli, "https://api.coingecko.com/api/v3/search?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Coins []struct {
				ID            string `json:"id"`
				Name          string `json:"name"`
				Symbol        string `json:"symbol"`
				MarketCapRank int    `json:"market_cap_rank"`
				Thumb         string `json:"thumb"`
			} `json:"coins"`
			Categories []struct {
				Name string `json:"name"`
			} `json:"categories"`
			Exchanges []struct {
				Name string `json:"name"`
			} `json:"exchanges"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("coingecko search decode: %w", err)
		}
		limit := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 25 {
			limit = int(l)
		}
		for i, c := range raw.Coins {
			if i >= limit {
				break
			}
			out.Coins = append(out.Coins, CoinGeckoMatch{
				ID: c.ID, Name: c.Name, Symbol: c.Symbol,
				MarketCapRank: c.MarketCapRank, Thumb: c.Thumb,
			})
		}
		for i, cat := range raw.Categories {
			if i >= 5 {
				break
			}
			out.Categories = append(out.Categories, cat.Name)
		}
		for i, ex := range raw.Exchanges {
			if i >= 5 {
				break
			}
			out.Exchanges = append(out.Exchanges, ex.Name)
		}
		out.Returned = len(out.Coins)

	case "coin_detail":
		coinID, _ := input["coin_id"].(string)
		coinID = strings.TrimSpace(coinID)
		if coinID == "" {
			return nil, fmt.Errorf("input.coin_id required (e.g. 'bitcoin', 'ethereum')")
		}
		out.Query = coinID
		params := url.Values{}
		params.Set("localization", "false")
		params.Set("tickers", "false")
		params.Set("community_data", "false")
		params.Set("developer_data", "true")
		params.Set("sparkline", "false")
		body, err := coingeckoGet(ctx, cli, fmt.Sprintf("https://api.coingecko.com/api/v3/coins/%s?%s", url.PathEscape(coinID), params.Encode()))
		if err != nil {
			return nil, err
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("coingecko detail decode: %w", err)
		}
		d := &CoinGeckoCoinDetail{
			ID:               gtString(raw, "id"),
			Symbol:           gtString(raw, "symbol"),
			Name:             gtString(raw, "name"),
			HashingAlgorithm: gtString(raw, "hashing_algorithm"),
			GenesisDate:      gtString(raw, "genesis_date"),
			CountryOrigin:    gtString(raw, "country_origin"),
		}
		// Description (excerpt)
		if desc, ok := raw["description"].(map[string]any); ok {
			if en := gtString(desc, "en"); en != "" {
				d.Description = hfTruncate(en, 400)
			}
		}
		// Categories
		if cats, ok := raw["categories"].([]any); ok {
			for _, c := range cats {
				if s, ok := c.(string); ok && s != "" {
					d.Categories = append(d.Categories, s)
				}
			}
		}
		// Links
		if links, ok := raw["links"].(map[string]any); ok {
			if hp, ok := links["homepage"].([]any); ok && len(hp) > 0 {
				if s, ok := hp[0].(string); ok {
					d.Homepage = s
				}
			}
			if wp, ok := links["whitepaper"].(string); ok {
				d.Whitepaper = wp
			}
			d.Twitter = gtString(links, "twitter_screen_name")
			d.Subreddit = gtString(links, "subreddit_url")
			d.OfficialForumURL = gtString(links, "official_forum_url")
			if bs, ok := links["blockchain_site"].([]any); ok {
				for _, x := range bs {
					if s, ok := x.(string); ok && s != "" {
						d.BlockchainSites = append(d.BlockchainSites, s)
						if len(d.BlockchainSites) >= 4 {
							break
						}
					}
				}
			}
			if repos, ok := links["repos_url"].(map[string]any); ok {
				if gh, ok := repos["github"].([]any); ok {
					for _, x := range gh {
						if s, ok := x.(string); ok && s != "" {
							d.GitHubRepos = append(d.GitHubRepos, s)
							if len(d.GitHubRepos) >= 5 {
								break
							}
						}
					}
				}
			}
		}
		// Market data
		if md, ok := raw["market_data"].(map[string]any); ok {
			d.PriceUSD = coingeckoUSDFloat(md, "current_price")
			d.MarketCapUSD = coingeckoUSDFloat(md, "market_cap")
			d.MarketCapRank = gtInt(md, "market_cap_rank")
			d.TotalVolumeUSD = coingeckoUSDFloat(md, "total_volume")
			d.ATHUSD = coingeckoUSDFloat(md, "ath")
			d.ATHDate = coingeckoUSDString(md, "ath_date")
			d.ATHChangePct = coingeckoUSDFloat(md, "ath_change_percentage")
			d.ATLUSD = coingeckoUSDFloat(md, "atl")
			d.ATLDate = coingeckoUSDString(md, "atl_date")
			d.PriceChange24hPct = gtFloat(md, "price_change_percentage_24h")
			d.PriceChange7dPct = gtFloat(md, "price_change_percentage_7d")
			d.PriceChange30dPct = gtFloat(md, "price_change_percentage_30d")
			d.CirculatingSupply = gtFloat(md, "circulating_supply")
			d.TotalSupply = gtFloat(md, "total_supply")
			d.MaxSupply = gtFloat(md, "max_supply")
		}
		// Developer data
		if dev, ok := raw["developer_data"].(map[string]any); ok {
			d.GithubForks = gtInt(dev, "forks")
			d.GithubStars = gtInt(dev, "stars")
			d.GithubSubscribers = gtInt(dev, "subscribers")
			d.TotalIssues = gtInt(dev, "total_issues")
			d.ClosedIssues = gtInt(dev, "closed_issues")
			d.PullRequestsMerged = gtInt(dev, "pull_requests_merged")
			d.Contributors = gtInt(dev, "pull_request_contributors")
			d.Commits4w = gtInt(dev, "commit_count_4_weeks")
		}
		out.Detail = d
		out.Returned = 1

	case "top_markets":
		limit := 25
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 250 {
			limit = int(l)
		}
		category, _ := input["category"].(string)
		params := url.Values{}
		params.Set("vs_currency", "usd")
		params.Set("order", "market_cap_desc")
		params.Set("per_page", fmt.Sprintf("%d", limit))
		params.Set("page", "1")
		params.Set("sparkline", "false")
		params.Set("price_change_percentage", "24h")
		if category != "" {
			params.Set("category", category)
		}
		out.Query = fmt.Sprintf("top %d by market cap", limit)
		if category != "" {
			out.Query += " (category=" + category + ")"
		}
		body, err := coingeckoGet(ctx, cli, "https://api.coingecko.com/api/v3/coins/markets?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw []map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("coingecko markets decode: %w", err)
		}
		for _, r := range raw {
			out.Markets = append(out.Markets, CoinGeckoMarketRow{
				Rank:              gtInt(r, "market_cap_rank"),
				ID:                gtString(r, "id"),
				Symbol:            gtString(r, "symbol"),
				Name:              gtString(r, "name"),
				PriceUSD:          gtFloat(r, "current_price"),
				MarketCapUSD:      gtFloat(r, "market_cap"),
				TotalVolumeUSD:    gtFloat(r, "total_volume"),
				PriceChange24hPct: gtFloat(r, "price_change_percentage_24h"),
				ATHChangePct:      gtFloat(r, "ath_change_percentage"),
			})
		}
		out.Returned = len(out.Markets)

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search, coin_detail, top_markets", mode)
	}

	out.HighlightFindings = buildCoinGeckoHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func coingeckoUSDFloat(m map[string]any, key string) float64 {
	if v, ok := m[key].(map[string]any); ok {
		return gtFloat(v, "usd")
	}
	return 0
}

func coingeckoUSDString(m map[string]any, key string) string {
	if v, ok := m[key].(map[string]any); ok {
		return gtString(v, "usd")
	}
	return ""
}

func coingeckoGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coingecko: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 429 {
		return nil, fmt.Errorf("coingecko rate-limited (free tier ~10-30 req/min)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("coingecko HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildCoinGeckoHighlights(o *CoinGeckoSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search":
		hi = append(hi, fmt.Sprintf("✓ %d coin matches for '%s'", o.Returned, o.Query))
		for i, c := range o.Coins {
			if i >= 5 {
				break
			}
			rank := ""
			if c.MarketCapRank > 0 {
				rank = fmt.Sprintf(" (rank #%d)", c.MarketCapRank)
			}
			hi = append(hi, fmt.Sprintf("  • %s [%s]%s — id: %s", c.Name, strings.ToUpper(c.Symbol), rank, c.ID))
		}
		if len(o.Categories) > 0 {
			hi = append(hi, "  matching categories: "+strings.Join(o.Categories, " · "))
		}
		if len(o.Exchanges) > 0 {
			hi = append(hi, "  matching exchanges: "+strings.Join(o.Exchanges, " · "))
		}

	case "coin_detail":
		if o.Detail == nil {
			break
		}
		d := o.Detail
		hi = append(hi, fmt.Sprintf("✓ %s [%s] — rank #%d", d.Name, strings.ToUpper(d.Symbol), d.MarketCapRank))
		hi = append(hi, fmt.Sprintf("  💰 price: %s · market cap: %s · 24h vol: %s", formatUSD(d.PriceUSD), formatUSD(d.MarketCapUSD), formatUSD(d.TotalVolumeUSD)))
		hi = append(hi, fmt.Sprintf("  📈 24h: %+.2f%% · 7d: %+.2f%% · 30d: %+.2f%%", d.PriceChange24hPct, d.PriceChange7dPct, d.PriceChange30dPct))
		if d.ATHUSD > 0 {
			hi = append(hi, fmt.Sprintf("  📊 ATH: %s on %s (%+.2f%% from current)", formatUSD(d.ATHUSD), d.ATHDate[:10], d.ATHChangePct))
		}
		if d.GenesisDate != "" {
			hi = append(hi, fmt.Sprintf("  genesis: %s · hashing: %s", d.GenesisDate, d.HashingAlgorithm))
		}
		if d.Twitter != "" || d.Homepage != "" || len(d.GitHubRepos) > 0 {
			parts := []string{}
			if d.Twitter != "" {
				parts = append(parts, "@"+d.Twitter)
			}
			if d.Homepage != "" {
				parts = append(parts, "🌐 "+d.Homepage)
			}
			if len(d.GitHubRepos) > 0 {
				parts = append(parts, "📦 "+d.GitHubRepos[0])
			}
			hi = append(hi, "  links: "+strings.Join(parts, " · "))
		}
		if d.Whitepaper != "" {
			hi = append(hi, "  whitepaper: "+d.Whitepaper)
		}
		if d.GithubStars > 0 || d.PullRequestsMerged > 0 {
			hi = append(hi, fmt.Sprintf("  👨‍💻 dev activity: %s ⭐ · %d 🍴 · %d PRs merged · %d contributors · %d commits last 4w",
				fmtThousands(d.GithubStars), d.GithubForks, d.PullRequestsMerged, d.Contributors, d.Commits4w))
		}
		if len(d.Categories) > 0 {
			cats := d.Categories
			if len(cats) > 5 {
				cats = cats[:5]
			}
			hi = append(hi, "  categories: "+strings.Join(cats, " · "))
		}

	case "top_markets":
		hi = append(hi, fmt.Sprintf("✓ %s", o.Query))
		for i, m := range o.Markets {
			if i >= 10 {
				break
			}
			arrow := "→"
			if m.PriceChange24hPct > 0 {
				arrow = "📈"
			} else if m.PriceChange24hPct < 0 {
				arrow = "📉"
			}
			hi = append(hi, fmt.Sprintf("  [%2d] %s [%s] %s · %s · %s 24h %+.2f%%",
				m.Rank, m.Name, strings.ToUpper(m.Symbol), formatUSD(m.PriceUSD), formatUSD(m.MarketCapUSD), arrow, m.PriceChange24hPct))
		}
	}
	return hi
}
