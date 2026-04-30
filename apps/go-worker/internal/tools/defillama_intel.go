package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefillamaIntel queries DefiLlama's free no-auth public API for DeFi /
// crypto-protocol intelligence:
//
//   - "hacks_search"  : query the structured DeFi-hacks catalog (~500 entries
//                       since 2020), filterable by chain, technique,
//                       target type, date range, min amount, returned-funds
//                       status. The only public dataset that catalogs every
//                       public DeFi exploit with attack-vector taxonomy.
//   - "recent_hacks"  : last N days of hacks (operational threat feed).
//   - "protocol"      : look up a protocol by fuzzy name → full metadata
//                       (chains, TVL, category, twitter handle, github,
//                       audits count, audit links, hallmarks/timeline).
//   - "top_tvl"       : top protocols by current total value locked, with
//                       optional category filter ("Dexs", "Lending",
//                       "Liquid Staking", "CEX", "Bridges", etc).
//
// Free, no auth. Both endpoints (api.llama.fi/hacks, /protocols) return
// plain JSON. We cache both lists in process memory for 1h since they
// change at most a few times per day.

type DefiHack struct {
	Name           string   `json:"name"`
	Date           int64    `json:"date_unix,omitempty"`
	DateStr        string   `json:"date,omitempty"`
	Classification string   `json:"classification,omitempty"`
	Technique      string   `json:"technique,omitempty"`
	Amount         float64  `json:"amount_usd"`
	AmountFmt      string   `json:"amount_formatted,omitempty"` // "$1.4B" / "$624M"
	Chain          []string `json:"chain,omitempty"`
	BridgeHack     bool     `json:"bridge_hack,omitempty"`
	TargetType     string   `json:"target_type,omitempty"`
	Source         string   `json:"source,omitempty"`
	ReturnedFunds  *float64 `json:"returned_funds,omitempty"`
	Language       string   `json:"language,omitempty"`
	DefillamaID    *int64   `json:"defillama_id,omitempty"`
}

type DefiProtocol struct {
	ID           string   `json:"id,omitempty"`
	Name         string   `json:"name"`
	Slug         string   `json:"slug,omitempty"`
	Category     string   `json:"category,omitempty"`
	Chain        string   `json:"chain,omitempty"`
	Chains       []string `json:"chains,omitempty"`
	TVL          float64  `json:"tvl_usd"`
	TVLFormatted string   `json:"tvl_formatted,omitempty"`
	Twitter      string   `json:"twitter,omitempty"`
	Github       []string `json:"github,omitempty"`
	URL          string   `json:"url,omitempty"`
	Description  string   `json:"description,omitempty"`
	AuditCount   string   `json:"audits,omitempty"`
	AuditLinks   []string `json:"audit_links,omitempty"`
	AuditNote    string   `json:"audit_note,omitempty"`
	Hallmarks    []any    `json:"hallmarks,omitempty"`
	Symbol       string   `json:"token_symbol,omitempty"`
	Logo         string   `json:"logo,omitempty"`
}

type DefillamaIntelOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query,omitempty"`
	Hacks             []DefiHack     `json:"hacks,omitempty"`
	Protocols         []DefiProtocol `json:"protocols,omitempty"`

	// Aggregations
	TotalHacks        int            `json:"total_hacks_in_catalog,omitempty"`
	TotalProtocols    int            `json:"total_protocols_in_catalog,omitempty"`
	HacksReturned     int            `json:"hacks_returned,omitempty"`
	ProtocolsReturned int            `json:"protocols_returned,omitempty"`
	TotalLossUSD      float64        `json:"total_loss_usd,omitempty"`
	TotalRecoveredUSD float64        `json:"total_recovered_usd,omitempty"`
	UniqueChains      []string       `json:"unique_chains,omitempty"`
	UniqueTechniques  []string       `json:"unique_techniques,omitempty"`

	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
	Note              string         `json:"note,omitempty"`
}

// Caches
var (
	defiHacksCache     []DefiHack
	defiHacksLoaded    time.Time
	defiHacksMu        sync.RWMutex
	defiProtocolsCache []DefiProtocol
	defiProtoLoaded    time.Time
	defiProtoMu        sync.RWMutex
)

const defiCacheTTL = 1 * time.Hour

func defiLoadHacks(ctx context.Context) ([]DefiHack, error) {
	defiHacksMu.RLock()
	if defiHacksCache != nil && time.Since(defiHacksLoaded) < defiCacheTTL {
		c := defiHacksCache
		defiHacksMu.RUnlock()
		return c, nil
	}
	defiHacksMu.RUnlock()

	defiHacksMu.Lock()
	defer defiHacksMu.Unlock()
	if defiHacksCache != nil && time.Since(defiHacksLoaded) < defiCacheTTL {
		return defiHacksCache, nil
	}

	cli := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.llama.fi/hacks", nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hacks fetch: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("hacks HTTP %d", resp.StatusCode)
	}
	var raw []struct {
		Name           string   `json:"name"`
		Date           int64    `json:"date"`
		Classification string   `json:"classification"`
		Technique      string   `json:"technique"`
		Amount         *float64 `json:"amount"`
		Chain          []string `json:"chain"`
		BridgeHack     bool     `json:"bridgeHack"`
		TargetType     string   `json:"targetType"`
		Source         string   `json:"source"`
		ReturnedFunds  *float64 `json:"returnedFunds"`
		DefillamaID    *int64   `json:"defillamaId"`
		Language       string   `json:"language"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("hacks decode: %w", err)
	}
	out := make([]DefiHack, 0, len(raw))
	for _, h := range raw {
		amt := 0.0
		if h.Amount != nil {
			amt = *h.Amount
		}
		hk := DefiHack{
			Name:           h.Name,
			Date:           h.Date,
			Classification: h.Classification,
			Technique:      h.Technique,
			Amount:         amt,
			AmountFmt:      formatUSD(amt),
			Chain:          h.Chain,
			BridgeHack:     h.BridgeHack,
			TargetType:     h.TargetType,
			Source:         h.Source,
			ReturnedFunds:  h.ReturnedFunds,
			Language:       h.Language,
			DefillamaID:    h.DefillamaID,
		}
		if h.Date > 0 {
			hk.DateStr = time.Unix(h.Date, 0).UTC().Format("2006-01-02")
		}
		out = append(out, hk)
	}
	defiHacksCache = out
	defiHacksLoaded = time.Now()
	return out, nil
}

func defiLoadProtocols(ctx context.Context) ([]DefiProtocol, error) {
	defiProtoMu.RLock()
	if defiProtocolsCache != nil && time.Since(defiProtoLoaded) < defiCacheTTL {
		c := defiProtocolsCache
		defiProtoMu.RUnlock()
		return c, nil
	}
	defiProtoMu.RUnlock()

	defiProtoMu.Lock()
	defer defiProtoMu.Unlock()
	if defiProtocolsCache != nil && time.Since(defiProtoLoaded) < defiCacheTTL {
		return defiProtocolsCache, nil
	}

	cli := &http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.llama.fi/protocols", nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("protocols fetch: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("protocols HTTP %d", resp.StatusCode)
	}
	var raw []struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		Slug       string   `json:"slug"`
		Category   string   `json:"category"`
		Chain      string   `json:"chain"`
		Chains     []string `json:"chains"`
		TVL        float64  `json:"tvl"`
		Twitter    string   `json:"twitter"`
		URL        string   `json:"url"`
		Description string  `json:"description"`
		Audits     string   `json:"audits"`
		AuditNote  string   `json:"audit_note"`
		AuditLinks []string `json:"audit_links"`
		Hallmarks  []any    `json:"hallmarks"`
		Symbol     string   `json:"symbol"`
		Logo       string   `json:"logo"`
		Github     []string `json:"github"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("protocols decode: %w", err)
	}
	out := make([]DefiProtocol, 0, len(raw))
	for _, p := range raw {
		out = append(out, DefiProtocol{
			ID:           p.ID,
			Name:         p.Name,
			Slug:         p.Slug,
			Category:     p.Category,
			Chain:        p.Chain,
			Chains:       p.Chains,
			TVL:          p.TVL,
			TVLFormatted: formatUSD(p.TVL),
			Twitter:      p.Twitter,
			Github:       p.Github,
			URL:          p.URL,
			Description:  p.Description,
			AuditCount:   p.Audits,
			AuditLinks:   p.AuditLinks,
			AuditNote:    p.AuditNote,
			Hallmarks:    p.Hallmarks,
			Symbol:       p.Symbol,
			Logo:         p.Logo,
		})
	}
	defiProtocolsCache = out
	defiProtoLoaded = time.Now()
	return out, nil
}

func DefillamaIntel(ctx context.Context, input map[string]any) (*DefillamaIntelOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["min_amount"]; ok {
			mode = "hacks_search"
		} else if _, ok := input["category"]; ok {
			mode = "top_tvl"
		} else if _, ok := input["query"]; ok {
			mode = "protocol"
		} else {
			mode = "recent_hacks"
		}
	}

	out := &DefillamaIntelOutput{
		Mode:   mode,
		Source: "api.llama.fi",
	}
	start := time.Now()

	switch mode {
	case "hacks_search":
		hacks, err := defiLoadHacks(ctx)
		if err != nil {
			return nil, err
		}
		out.TotalHacks = len(hacks)
		filtered := filterHacks(hacks, input)
		// Sort by amount desc by default
		sortBy, _ := input["sort"].(string)
		switch strings.ToLower(sortBy) {
		case "date":
			sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Date > filtered[j].Date })
		default:
			sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Amount > filtered[j].Amount })
		}
		limit := 25
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 200 {
			limit = int(l)
		}
		if len(filtered) > limit {
			out.Note = fmt.Sprintf("returning top %d of %d matches", limit, len(filtered))
			filtered = filtered[:limit]
		}
		out.Hacks = filtered
		out.HacksReturned = len(filtered)

	case "recent_hacks":
		hacks, err := defiLoadHacks(ctx)
		if err != nil {
			return nil, err
		}
		out.TotalHacks = len(hacks)
		days := 90
		if d, ok := input["days"].(float64); ok && d > 0 && d <= 3650 {
			days = int(d)
		}
		out.Query = fmt.Sprintf("last %d days", days)
		cutoff := time.Now().AddDate(0, 0, -days).Unix()
		filtered := []DefiHack{}
		for _, h := range hacks {
			if h.Date >= cutoff {
				filtered = append(filtered, h)
			}
		}
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Date > filtered[j].Date })
		limit := 50
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 200 {
			limit = int(l)
		}
		if len(filtered) > limit {
			out.Note = fmt.Sprintf("returning %d of %d hacks in window", limit, len(filtered))
			filtered = filtered[:limit]
		}
		out.Hacks = filtered
		out.HacksReturned = len(filtered)

	case "protocol":
		q, _ := input["query"].(string)
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, fmt.Errorf("input.query required for protocol mode")
		}
		out.Query = q
		protocols, err := defiLoadProtocols(ctx)
		if err != nil {
			return nil, err
		}
		out.TotalProtocols = len(protocols)
		qLower := strings.ToLower(q)
		ranked := []struct {
			p     DefiProtocol
			score int
		}{}
		for _, p := range protocols {
			n := strings.ToLower(p.Name)
			s := strings.ToLower(p.Slug)
			score := 0
			switch {
			case n == qLower || s == qLower:
				score = 100
			case strings.HasPrefix(n, qLower) || strings.HasPrefix(s, qLower):
				score = 80
			case strings.Contains(n, qLower) || strings.Contains(s, qLower):
				score = 50
			}
			if score > 0 {
				ranked = append(ranked, struct {
					p     DefiProtocol
					score int
				}{p, score})
			}
		}
		sort.SliceStable(ranked, func(i, j int) bool {
			if ranked[i].score != ranked[j].score {
				return ranked[i].score > ranked[j].score
			}
			return ranked[i].p.TVL > ranked[j].p.TVL
		})
		limit := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			limit = int(l)
		}
		for i, r := range ranked {
			if i >= limit {
				break
			}
			out.Protocols = append(out.Protocols, r.p)
		}
		out.ProtocolsReturned = len(out.Protocols)

	case "top_tvl":
		protocols, err := defiLoadProtocols(ctx)
		if err != nil {
			return nil, err
		}
		out.TotalProtocols = len(protocols)
		category, _ := input["category"].(string)
		category = strings.TrimSpace(category)
		filtered := []DefiProtocol{}
		for _, p := range protocols {
			if category != "" && !strings.EqualFold(p.Category, category) {
				continue
			}
			filtered = append(filtered, p)
		}
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].TVL > filtered[j].TVL })
		limit := 20
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 100 {
			limit = int(l)
		}
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
		out.Protocols = filtered
		out.ProtocolsReturned = len(filtered)
		if category != "" {
			out.Query = "category=" + category
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: hacks_search, recent_hacks, protocol, top_tvl", mode)
	}

	// Aggregations
	chainSet := map[string]struct{}{}
	techSet := map[string]struct{}{}
	for _, h := range out.Hacks {
		out.TotalLossUSD += h.Amount
		if h.ReturnedFunds != nil {
			out.TotalRecoveredUSD += *h.ReturnedFunds
		}
		for _, c := range h.Chain {
			chainSet[c] = struct{}{}
		}
		if h.Technique != "" {
			techSet[h.Technique] = struct{}{}
		}
	}
	for c := range chainSet {
		out.UniqueChains = append(out.UniqueChains, c)
	}
	sort.Strings(out.UniqueChains)
	for t := range techSet {
		out.UniqueTechniques = append(out.UniqueTechniques, t)
	}
	sort.Strings(out.UniqueTechniques)

	out.HighlightFindings = buildDefillamaHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func filterHacks(hacks []DefiHack, input map[string]any) []DefiHack {
	out := []DefiHack{}
	chainFilter, _ := input["chain"].(string)
	techFilter, _ := input["technique"].(string)
	targetFilter, _ := input["target_type"].(string)
	nameFilter, _ := input["query"].(string)
	classFilter, _ := input["classification"].(string)

	chainFilter = strings.ToLower(chainFilter)
	techFilter = strings.ToLower(techFilter)
	targetFilter = strings.ToLower(targetFilter)
	nameFilter = strings.ToLower(nameFilter)
	classFilter = strings.ToLower(classFilter)

	minAmount := 0.0
	if v, ok := input["min_amount"].(float64); ok && v > 0 {
		minAmount = v
	}
	startTs := int64(0)
	endTs := int64(0)
	if s, ok := input["start_date"].(string); ok && s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			startTs = t.Unix()
		}
	}
	if s, ok := input["end_date"].(string); ok && s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			endTs = t.Unix()
		}
	}
	bridgeOnly := false
	if v, ok := input["bridge_only"].(bool); ok {
		bridgeOnly = v
	}

	for _, h := range hacks {
		if minAmount > 0 && h.Amount < minAmount {
			continue
		}
		if startTs > 0 && h.Date < startTs {
			continue
		}
		if endTs > 0 && h.Date > endTs {
			continue
		}
		if bridgeOnly && !h.BridgeHack {
			continue
		}
		if techFilter != "" && !strings.Contains(strings.ToLower(h.Technique), techFilter) {
			continue
		}
		if targetFilter != "" && !strings.Contains(strings.ToLower(h.TargetType), targetFilter) {
			continue
		}
		if classFilter != "" && !strings.Contains(strings.ToLower(h.Classification), classFilter) {
			continue
		}
		if nameFilter != "" && !strings.Contains(strings.ToLower(h.Name), nameFilter) {
			continue
		}
		if chainFilter != "" {
			match := false
			for _, c := range h.Chain {
				if strings.Contains(strings.ToLower(c), chainFilter) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, h)
	}
	return out
}

func formatUSD(amount float64) string {
	switch {
	case amount >= 1e9:
		return fmt.Sprintf("$%.2fB", amount/1e9)
	case amount >= 1e6:
		return fmt.Sprintf("$%.1fM", amount/1e6)
	case amount >= 1e3:
		return fmt.Sprintf("$%.1fK", amount/1e3)
	case amount > 0:
		return fmt.Sprintf("$%.0f", amount)
	default:
		return ""
	}
}

func buildDefillamaHighlights(o *DefillamaIntelOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "hacks_search":
		hi = append(hi, fmt.Sprintf("✓ %d hacks match filters (out of %d catalog total)", o.HacksReturned, o.TotalHacks))
		hi = append(hi, fmt.Sprintf("  total loss: %s · total recovered: %s", formatUSD(o.TotalLossUSD), formatUSD(o.TotalRecoveredUSD)))
		if len(o.UniqueChains) > 0 {
			topChains := o.UniqueChains
			suffix := ""
			if len(topChains) > 5 {
				topChains = topChains[:5]
				suffix = fmt.Sprintf(" … +%d more", len(o.UniqueChains)-5)
			}
			hi = append(hi, fmt.Sprintf("  unique chains: %s%s", strings.Join(topChains, ", "), suffix))
		}
		for i, h := range o.Hacks {
			if i >= 8 {
				break
			}
			ret := ""
			if h.ReturnedFunds != nil && *h.ReturnedFunds > 0 {
				ret = fmt.Sprintf(" [recovered %s]", formatUSD(*h.ReturnedFunds))
			}
			chains := strings.Join(h.Chain, ",")
			hi = append(hi, fmt.Sprintf("  • [%s] %s — %s on %s — %s%s", h.DateStr, h.Name, h.AmountFmt, chains, h.Technique, ret))
		}

	case "recent_hacks":
		hi = append(hi, fmt.Sprintf("✓ %d hacks in %s (out of %d catalog total)", o.HacksReturned, o.Query, o.TotalHacks))
		hi = append(hi, fmt.Sprintf("  cumulative loss: %s · recovered: %s", formatUSD(o.TotalLossUSD), formatUSD(o.TotalRecoveredUSD)))
		for i, h := range o.Hacks {
			if i >= 10 {
				break
			}
			chains := strings.Join(h.Chain, ",")
			hi = append(hi, fmt.Sprintf("  • [%s] %s — %s on %s — %s", h.DateStr, h.Name, h.AmountFmt, chains, h.Technique))
		}

	case "protocol":
		hi = append(hi, fmt.Sprintf("✓ %d protocol matches for '%s' (catalog %d total)", o.ProtocolsReturned, o.Query, o.TotalProtocols))
		for i, p := range o.Protocols {
			if i >= 6 {
				break
			}
			chainStr := p.Chain
			if len(p.Chains) > 1 {
				chainStr = strings.Join(p.Chains, "/")
			}
			gh := ""
			if len(p.Github) > 0 {
				gh = " · gh:" + strings.Join(p.Github, ",")
			}
			tw := ""
			if p.Twitter != "" {
				tw = " · @" + p.Twitter
			}
			audits := ""
			if p.AuditCount != "" {
				audits = " · audits:" + p.AuditCount
			}
			hi = append(hi, fmt.Sprintf("  • %s [%s] — TVL %s — %s%s%s%s", p.Name, p.Category, p.TVLFormatted, chainStr, tw, gh, audits))
		}

	case "top_tvl":
		query := "(all categories)"
		if o.Query != "" {
			query = "(" + o.Query + ")"
		}
		hi = append(hi, fmt.Sprintf("✓ Top %d protocols by TVL %s", o.ProtocolsReturned, query))
		for i, p := range o.Protocols {
			if i >= 10 {
				break
			}
			tw := ""
			if p.Twitter != "" {
				tw = " · @" + p.Twitter
			}
			hi = append(hi, fmt.Sprintf("  [%2d] %s [%s] — TVL %s on %s%s", i+1, p.Name, p.Category, p.TVLFormatted, p.Chain, tw))
		}
	}
	return hi
}
