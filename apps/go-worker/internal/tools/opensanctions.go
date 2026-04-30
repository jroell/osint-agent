package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type OSEntity struct {
	ID         string                   `json:"id"`
	Caption    string                   `json:"caption"`
	Schema     string                   `json:"schema"`
	Datasets   []string                 `json:"datasets,omitempty"`
	Topics     []string                 `json:"topics,omitempty"`
	Properties map[string]interface{}   `json:"properties,omitempty"`
	Score      float64                  `json:"score,omitempty"`
	Match      bool                     `json:"match,omitempty"`
}

type OpenSanctionsOutput struct {
	Query   string      `json:"query"`
	Schema  string      `json:"schema"`
	Total   int         `json:"total"`
	Results []OSEntity  `json:"results"`
	Source  string      `json:"source"`
	TookMs  int64       `json:"tookMs"`
	Note    string      `json:"note,omitempty"`
}

// OpenSanctionsScreen runs a name through OpenSanctions's matching API,
// returning sanctioned entities, PEPs, and crime/regulatory enforcement
// matches. As of 2024 OpenSanctions retired anonymous API access — an
// OPENSANCTIONS_API_KEY is required for both /search and /match (free tier
// available at https://www.opensanctions.org/api/).
func OpenSanctionsScreen(ctx context.Context, input map[string]any) (*OpenSanctionsOutput, error) {
	name, _ := input["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("input.name required (person or company name to screen)")
	}
	apiKey := os.Getenv("OPENSANCTIONS_API_KEY")
	if apiKey == "" {
		return nil, errors.New("OPENSANCTIONS_API_KEY env var required (anonymous access was retired in 2024; free tier at https://www.opensanctions.org/api/)")
	}
	schema := "Thing"
	if v, ok := input["schema"].(string); ok && v != "" {
		schema = v
	}
	dataset := "default"
	if v, ok := input["dataset"].(string); ok && v != "" {
		dataset = v
	}

	start := time.Now()
	out := &OpenSanctionsOutput{
		Query:  name,
		Schema: schema,
		Source: "opensanctions.org",
	}
	matchURL := fmt.Sprintf("https://api.opensanctions.org/match/%s?api_key=%s",
		dataset, apiKey)
	payload := map[string]interface{}{
		"queries": map[string]interface{}{
			"q1": map[string]interface{}{
				"schema":     schema,
				"properties": map[string][]string{"name": {name}},
			},
		},
	}
	raw, err := openSanctionsPost(ctx, matchURL, payload)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Responses map[string]struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Results []OSEntity `json:"results"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("opensanctions parse: %w", err)
	}
	if r, ok := resp.Responses["q1"]; ok {
		out.Total = r.Total.Value
		out.Results = r.Results
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func openSanctionsPost(ctx context.Context, url string, payload interface{}) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	const maxBody = 16 << 20
	out := make([]byte, 0, 64<<10)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if len(out) > maxBody {
				break
			}
		}
		if rerr != nil {
			break
		}
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("opensanctions %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
	return out, nil
}

