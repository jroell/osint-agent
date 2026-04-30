package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// =============================================================================
// phone_numverify — REQUIRES NUMVERIFY_API_KEY (free 250 req/mo)
// =============================================================================

type NumverifyOutput struct {
	Valid               bool   `json:"valid"`
	Number              string `json:"number"`
	LocalFormat         string `json:"local_format,omitempty"`
	InternationalFormat string `json:"international_format,omitempty"`
	CountryPrefix       string `json:"country_prefix,omitempty"`
	CountryCode         string `json:"country_code,omitempty"`
	CountryName         string `json:"country_name,omitempty"`
	Location            string `json:"location,omitempty"`
	Carrier             string `json:"carrier,omitempty"`
	LineType            string `json:"line_type,omitempty"`
	Source              string `json:"source"`
	TookMs              int64  `json:"tookMs"`
}

func PhoneNumverify(ctx context.Context, input map[string]any) (*NumverifyOutput, error) {
	number, _ := input["number"].(string)
	number = strings.TrimSpace(number)
	if number == "" {
		return nil, errors.New("input.number required (E.164 format preferred, e.g. +14155552671)")
	}
	key := os.Getenv("NUMVERIFY_API_KEY")
	if key == "" {
		return nil, errors.New("NUMVERIFY_API_KEY env var required (free tier: 250 req/month, https://numverify.com/product)")
	}
	start := time.Now()
	endpoint := fmt.Sprintf("http://apilayer.net/api/validate?access_key=%s&number=%s",
		url.QueryEscape(key), url.QueryEscape(number))
	body, err := httpGetJSON(ctx, endpoint, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("numverify: %w", err)
	}
	var resp NumverifyOutput
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("numverify parse: %w", err)
	}
	resp.Source = "numverify.com"
	resp.TookMs = time.Since(start).Milliseconds()
	return &resp, nil
}

// =============================================================================
// intelx_search — REQUIRES INTELX_API_KEY (paid; some leak content public-search)
// =============================================================================

type IntelxOutput struct {
	Query    string                   `json:"query"`
	Results  []map[string]interface{} `json:"results"`
	Count    int                      `json:"count"`
	Source   string                   `json:"source"`
	TookMs   int64                    `json:"tookMs"`
}

func IntelxSearch(ctx context.Context, input map[string]any) (*IntelxOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (selector — email, domain, IP, hash, etc.)")
	}
	key := os.Getenv("INTELX_API_KEY")
	if key == "" {
		return nil, errors.New("INTELX_API_KEY env var required (paid, https://intelx.io/account?tab=developer)")
	}
	limit := 50
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	start := time.Now()
	// Intelligence X has a two-stage search: (1) start a search, get an ID, (2) poll for results.
	// For a single-call adapter we use the synchronous /intelligent/search.json endpoint
	// (smaller result cap but simpler).
	endpoint := "https://2.intelx.io/intelligent/search?k=" + url.QueryEscape(key)
	payload := map[string]interface{}{
		"term":   q,
		"buckets": []string{"leaks.public", "leaks.private", "darknet.tor", "pastes"},
		"lookuplevel": 0,
		"maxresults":  limit,
		"timeout":     5,
		"datefrom":    "",
		"dateto":      "",
		"sort":        4,
		"media":       0,
		"terminate":   []interface{}{},
	}
	body, err := openSanctionsPost(ctx, endpoint, payload) // reuses generic JSON-POST helper
	if err != nil {
		return nil, fmt.Errorf("intelx: %w", err)
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("intelx parse: %w", err)
	}
	// Fetch results.
	resultsURL := fmt.Sprintf("https://2.intelx.io/intelligent/search/result?id=%s&limit=%d&statistics=0&previewlines=8&k=%s",
		url.QueryEscape(resp.ID), limit, url.QueryEscape(key))
	rbody, err := httpGetJSON(ctx, resultsURL, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("intelx results: %w", err)
	}
	var rr struct {
		Records []map[string]interface{} `json:"records"`
	}
	if err := json.Unmarshal(rbody, &rr); err != nil {
		return nil, fmt.Errorf("intelx results parse: %w", err)
	}
	return &IntelxOutput{
		Query:   q,
		Results: rr.Records,
		Count:   len(rr.Records),
		Source:  "intelx.io",
		TookMs:  time.Since(start).Milliseconds(),
	}, nil
}

// =============================================================================
// dehashed_search — REQUIRES DEHASHED_API_KEY + DEHASHED_EMAIL (paid)
// =============================================================================

type DehashedOutput struct {
	Query   string                   `json:"query"`
	Total   int                      `json:"total"`
	Entries []map[string]interface{} `json:"entries"`
	Source  string                   `json:"source"`
	TookMs  int64                    `json:"tookMs"`
}

func DehashedSearch(ctx context.Context, input map[string]any) (*DehashedOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (e.g. \"email:victim@example.com\" or \"domain:example.com\")")
	}
	apiKey := os.Getenv("DEHASHED_API_KEY")
	apiEmail := os.Getenv("DEHASHED_EMAIL")
	if apiKey == "" || apiEmail == "" {
		return nil, errors.New("DEHASHED_API_KEY and DEHASHED_EMAIL env vars required (paid, https://dehashed.com/api)")
	}
	limit := 100
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	start := time.Now()
	endpoint := fmt.Sprintf("https://api.dehashed.com/search?query=%s&size=%d", url.QueryEscape(q), limit)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(apiEmail, apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dehashed: %w", err)
	}
	defer resp.Body.Close()
	const maxBody = 16 << 20
	body := make([]byte, 0, 64<<10)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
			if len(body) > maxBody {
				break
			}
		}
		if rerr != nil {
			break
		}
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("dehashed %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Total   int                      `json:"total"`
		Entries []map[string]interface{} `json:"entries"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("dehashed parse: %w", err)
	}
	return &DehashedOutput{
		Query:   q,
		Total:   parsed.Total,
		Entries: parsed.Entries,
		Source:  "dehashed.com",
		TookMs:  time.Since(start).Milliseconds(),
	}, nil
}
