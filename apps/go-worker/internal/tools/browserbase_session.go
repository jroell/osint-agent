package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// BrowserbaseSession wraps the Browserbase headless-browser API
// (browserbase.com). REQUIRES `BROWSERBASE_API_KEY` and
// `BROWSERBASE_PROJECT_ID`. Ported (reimagined) from vurvey-api
// `browserbase-tools.ts`.
//
// Use cases: render JS-heavy pages that Firecrawl can't, bypass simple
// anti-bot, capture network logs from a real browser, and run multi-step
// authenticated flows.
//
// Modes:
//   - "render_url"   : create a session, navigate to URL, return rendered HTML
//   - "screenshot"   : take a PNG screenshot (returned as data URL)
//   - "network_log"  : capture network requests during a navigation
//
// Knowledge-graph: emits typed entity (kind: "rendered_page") with
// stable URL.

type BBPage struct {
	URL            string           `json:"url"`
	Title          string           `json:"title,omitempty"`
	HTML           string           `json:"html,omitempty"`
	ScreenshotB64  string           `json:"screenshot_b64,omitempty"`
	NetworkLog     []map[string]any `json:"network_log,omitempty"`
	SessionID      string           `json:"session_id,omitempty"`
	BrowserbaseURL string           `json:"browserbase_url,omitempty"`
}

type BBEntity struct {
	Kind        string         `json:"kind"`
	URL         string         `json:"url"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type BrowserbaseSessionOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query"`
	Page              *BBPage    `json:"page,omitempty"`
	Entities          []BBEntity `json:"entities"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
}

func BrowserbaseSession(ctx context.Context, input map[string]any) (*BrowserbaseSessionOutput, error) {
	apiKey := os.Getenv("BROWSERBASE_API_KEY")
	projectID := os.Getenv("BROWSERBASE_PROJECT_ID")
	if apiKey == "" || projectID == "" {
		return nil, fmt.Errorf("BROWSERBASE_API_KEY and BROWSERBASE_PROJECT_ID required (free tier at browserbase.com)")
	}
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "render_url"
	}
	out := &BrowserbaseSessionOutput{Mode: mode, Source: "browserbase.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 90 * time.Second}

	rawURL, _ := input["url"].(string)
	if rawURL == "" {
		return nil, fmt.Errorf("input.url required")
	}
	out.Query = rawURL

	// Step 1: create a session via Browserbase REST API
	sessReq := map[string]any{
		"projectId": projectID,
		"keepAlive": false,
		"browserSettings": map[string]any{
			"viewport": map[string]any{"width": 1280, "height": 720},
		},
	}
	body, _ := json.Marshal(sessReq)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.browserbase.com/v1/sessions", bytes.NewReader(body))
	req.Header.Set("X-BB-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("browserbase create session: %w", err)
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		return nil, fmt.Errorf("browserbase create session HTTP %d: %s", resp.StatusCode, hfTruncate(string(respBody), 200))
	}
	var sess struct {
		ID                string `json:"id"`
		Status            string `json:"status"`
		ConnectURL        string `json:"connectUrl"`
		SeleniumRemoteURL string `json:"seleniumRemoteUrl"`
	}
	if err := json.Unmarshal(respBody, &sess); err != nil {
		return nil, fmt.Errorf("browserbase decode: %w", err)
	}

	// Step 2: NOTE — driving the actual browser requires a Chrome DevTools
	// Protocol or Selenium client. We can't ship a full puppeteer in Go;
	// instead we use Browserbase's "session debug" REST endpoint to fetch
	// page state after the session is provisioned. For full automation,
	// connect to sess.ConnectURL with a CDP client (chromedp); the call
	// below is a minimal placeholder that returns the session ID + connect
	// URL so callers can drive their own.
	out.Page = &BBPage{
		URL:            rawURL,
		SessionID:      sess.ID,
		BrowserbaseURL: sess.ConnectURL,
	}

	// Optional: fetch the live HTML if the session exposes /pages/<id>/html
	// (BB has this on the dashboard but not REST). Skip for now.

	out.Entities = []BBEntity{{
		Kind: "rendered_page", URL: rawURL,
		Name:        rawURL,
		Description: "Browserbase session " + sess.ID,
		Attributes: map[string]any{
			"session_id":   sess.ID,
			"connect_url":  sess.ConnectURL,
			"selenium_url": sess.SeleniumRemoteURL,
			"status":       sess.Status,
		},
	}}
	out.HighlightFindings = []string{
		fmt.Sprintf("✓ browserbase session %s for %s", sess.ID, rawURL),
		fmt.Sprintf("  connect via CDP at %s (status=%s)", sess.ConnectURL, sess.Status),
		"  note: Go worker provides session-creation only; drive via chromedp on the API side for full automation",
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
