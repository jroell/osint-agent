package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	wappalyzer "github.com/projectdiscovery/wappalyzergo"
)

type TechStackOutput struct {
	URL          string                                  `json:"url"`
	FinalURL     string                                  `json:"final_url,omitempty"`
	Status       int                                     `json:"status"`
	Technologies map[string]wappalyzer.AppInfo `json:"technologies"`
	Categories   []string                                `json:"categories,omitempty"`
	TookMs       int64                                   `json:"tookMs"`
	Source       string                                  `json:"source"`
}

// TechStackFingerprint runs ProjectDiscovery's wappalyzergo against a URL.
// Returns a structured tech-stack mapping (CMS, frameworks, analytics, CDNs,
// programming languages, web servers, etc.) sourced from the open Wappalyzer
// fingerprint database (~3000 technologies, headers + body + cookie + meta heuristics).
func TechStackFingerprint(ctx context.Context, input map[string]any) (*TechStackOutput, error) {
	rawURL, _ := input["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("input.url required")
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil, errors.New("input.url must be absolute http(s)")
	}
	timeoutMs := 15000
	if v, ok := input["timeout_ms"].(float64); ok && v > 0 {
		timeoutMs = int(v)
	}

	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")

	client := &http.Client{Timeout: time.Duration(timeoutMs) * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	wap, err := wappalyzer.New()
	if err != nil {
		return nil, fmt.Errorf("wappalyzer init: %w", err)
	}
	techs := wap.FingerprintWithInfo(resp.Header, body)
	cats := map[string]struct{}{}
	for _, info := range techs {
		for _, c := range info.Categories {
			cats[c] = struct{}{}
		}
	}
	catList := make([]string, 0, len(cats))
	for c := range cats {
		catList = append(catList, c)
	}

	return &TechStackOutput{
		URL:          rawURL,
		FinalURL:     resp.Request.URL.String(),
		Status:       resp.StatusCode,
		Technologies: techs,
		Categories:   catList,
		TookMs:       time.Since(start).Milliseconds(),
		Source:       "wappalyzergo (PD)",
	}, nil
}
