package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// =============================================================================
// shodan_search — REQUIRES SHODAN_API_KEY (https://account.shodan.io/billing)
// =============================================================================

type ShodanOutput struct {
	Query   string                   `json:"query"`
	Total   int                      `json:"total"`
	Matches []map[string]interface{} `json:"matches"`
	TookMs  int64                    `json:"tookMs"`
	Source  string                   `json:"source"`
}

func ShodanSearch(ctx context.Context, input map[string]any) (*ShodanOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (Shodan search syntax, e.g. \"hostname:example.com\" or \"port:22 country:US\")")
	}
	limit := 50
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	key := os.Getenv("SHODAN_API_KEY")
	if key == "" {
		return nil, errors.New("SHODAN_API_KEY env var required — Shodan search is paid (https://account.shodan.io/billing)")
	}
	start := time.Now()
	endpoint := "https://api.shodan.io/shodan/host/search?key=" + url.QueryEscape(key) +
		"&query=" + url.QueryEscape(q) +
		"&limit=" + fmt.Sprint(limit)
	body, err := httpGetJSON(ctx, endpoint, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("shodan: %w", err)
	}
	var resp struct {
		Total   int                      `json:"total"`
		Matches []map[string]interface{} `json:"matches"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("shodan parse: %w", err)
	}
	return &ShodanOutput{
		Query:   q,
		Total:   resp.Total,
		Matches: resp.Matches,
		TookMs:  time.Since(start).Milliseconds(),
		Source:  "shodan",
	}, nil
}

// =============================================================================
// censys_search — REQUIRES CENSYS_API_ID + CENSYS_API_SECRET (free 250 req/mo)
// =============================================================================

type CensysOutput struct {
	Query   string                   `json:"query"`
	Total   int                      `json:"total"`
	Hits    []map[string]interface{} `json:"hits"`
	TookMs  int64                    `json:"tookMs"`
	Source  string                   `json:"source"`
}

func CensysSearch(ctx context.Context, input map[string]any) (*CensysOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (Censys search syntax, e.g. \"services.tls.certificates.leaf_data.subject.common_name: example.com\")")
	}
	limit := 50
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	apiID := os.Getenv("CENSYS_API_ID")
	apiSecret := os.Getenv("CENSYS_API_SECRET")
	if apiID == "" || apiSecret == "" {
		return nil, errors.New("CENSYS_API_ID and CENSYS_API_SECRET env vars required (free tier: 250 queries/month, https://search.censys.io/account/api)")
	}
	start := time.Now()
	endpoint := fmt.Sprintf("https://search.censys.io/api/v2/hosts/search?q=%s&per_page=%d",
		url.QueryEscape(q), limit)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	auth := base64.StdEncoding.EncodeToString([]byte(apiID + ":" + apiSecret))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("censys: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("censys %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Result struct {
			Total int                      `json:"total"`
			Hits  []map[string]interface{} `json:"hits"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("censys parse: %w", err)
	}
	return &CensysOutput{
		Query:  q,
		Total:  parsed.Result.Total,
		Hits:   parsed.Result.Hits,
		TookMs: time.Since(start).Milliseconds(),
		Source: "censys",
	}, nil
}
