package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// =============================================================================
// tavily_search — answer-synthesis search (REQUIRES TAVILY_API_KEY)
// =============================================================================

type TavilyOutput struct {
	Query   string                   `json:"query"`
	Answer  string                   `json:"answer,omitempty"`
	Results []map[string]interface{} `json:"results"`
	Count   int                      `json:"count"`
	Source  string                   `json:"source"`
	TookMs  int64                    `json:"tookMs"`
}

// TavilySearch is the agent-friendly Tavily search: returns a synthesized
// answer (when include_answer=true) plus the source URLs used. Designed for
// LLM consumption — orders of magnitude more useful than raw HTML scraping
// for "what is X?" questions.
func TavilySearch(ctx context.Context, input map[string]any) (*TavilyOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	key := os.Getenv("TAVILY_API_KEY")
	if key == "" {
		return nil, errors.New("TAVILY_API_KEY env var required (https://tavily.com)")
	}
	depth := "advanced"
	if v, ok := input["search_depth"].(string); ok && v != "" {
		depth = v
	}
	limit := 8
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	includeAnswer := true
	if v, ok := input["include_answer"].(bool); ok {
		includeAnswer = v
	}
	includeDomains, _ := input["include_domains"].([]interface{})
	excludeDomains, _ := input["exclude_domains"].([]interface{})

	start := time.Now()
	payload := map[string]interface{}{
		"api_key":        key,
		"query":          q,
		"search_depth":   depth,
		"max_results":    limit,
		"include_answer": includeAnswer,
	}
	if len(includeDomains) > 0 {
		payload["include_domains"] = includeDomains
	}
	if len(excludeDomains) > 0 {
		payload["exclude_domains"] = excludeDomains
	}
	body, _ := json.Marshal(payload)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("tavily %d: %s", resp.StatusCode, truncate(string(rb), 200))
	}
	var parsed struct {
		Answer  string                   `json:"answer"`
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return nil, fmt.Errorf("tavily parse: %w", err)
	}
	return &TavilyOutput{
		Query: q, Answer: parsed.Answer, Results: parsed.Results,
		Count: len(parsed.Results), Source: "api.tavily.com",
		TookMs: time.Since(start).Milliseconds(),
	}, nil
}

// =============================================================================
// perplexity_search — citation-grounded LLM answer (REQUIRES PERPLEXITY_API_KEY)
// =============================================================================

type PerplexityOutput struct {
	Query    string   `json:"query"`
	Answer   string   `json:"answer"`
	Citations []string `json:"citations,omitempty"`
	Model    string   `json:"model"`
	Source   string   `json:"source"`
	TookMs   int64    `json:"tookMs"`
}

// PerplexitySearch uses Perplexity's Sonar online models — they have
// real-time web access and return synthesized answers grounded in citations.
// Best for "explain who X is" or "what is the latest on Y" queries.
func PerplexitySearch(ctx context.Context, input map[string]any) (*PerplexityOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	key := os.Getenv("PERPLEXITY_API_KEY")
	if key == "" {
		return nil, errors.New("PERPLEXITY_API_KEY env var required (https://docs.perplexity.ai)")
	}
	model := "sonar"
	if v, ok := input["model"].(string); ok && v != "" {
		model = v
	}
	systemPrompt := "You are an OSINT analyst. Answer concisely with verified facts only. Cite sources."
	if v, ok := input["system"].(string); ok && v != "" {
		systemPrompt = v
	}

	start := time.Now()
	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": q},
		},
		"return_citations": true,
	})
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, "https://api.perplexity.ai/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("perplexity: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("perplexity %d: %s", resp.StatusCode, truncate(string(rb), 240))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Citations []string `json:"citations"`
		Model     string   `json:"model"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return nil, fmt.Errorf("perplexity parse: %w", err)
	}
	answer := ""
	if len(parsed.Choices) > 0 {
		answer = parsed.Choices[0].Message.Content
	}
	return &PerplexityOutput{
		Query: q, Answer: answer, Citations: parsed.Citations,
		Model: parsed.Model, Source: "api.perplexity.ai",
		TookMs: time.Since(start).Milliseconds(),
	}, nil
}

// =============================================================================
// grok_x_search — Grok with live X access (REQUIRES XAI_API_KEY)
// =============================================================================

type GrokOutput struct {
	Query   string `json:"query"`
	Answer  string `json:"answer"`
	Model   string `json:"model"`
	Source  string `json:"source"`
	TookMs  int64  `json:"tookMs"`
}

// GrokXSearch wraps the xAI Grok API. Grok has first-party access to live
// X (Twitter) data — the only credible programmatic path to current X content
// since snscrape died and the X API moved behind a $100+/mo paywall. Phrase
// queries as "find tweets from @username about <topic>" or "what is @username
// posting recently".
func GrokXSearch(ctx context.Context, input map[string]any) (*GrokOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	key := os.Getenv("XAI_API_KEY")
	if key == "" {
		return nil, errors.New("XAI_API_KEY env var required (https://x.ai/api). Grok has live X data access — the only credible path to current X content.")
	}
	model := "grok-4-latest"
	if v, ok := input["model"].(string); ok && v != "" {
		model = v
	}
	system := "You are an OSINT analyst with live access to X (Twitter). Answer the user's query by searching X content. Always cite specific tweets/users. If you cannot find information, say so explicitly — do not fabricate."
	if v, ok := input["system"].(string); ok && v != "" {
		system = v
	}

	start := time.Now()
	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": q},
		},
	})
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, "https://api.x.ai/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("grok: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("grok %d: %s", resp.StatusCode, truncate(string(rb), 240))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return nil, fmt.Errorf("grok parse: %w", err)
	}
	answer := ""
	if len(parsed.Choices) > 0 {
		answer = parsed.Choices[0].Message.Content
	}
	return &GrokOutput{
		Query: q, Answer: answer, Model: parsed.Model,
		Source: "api.x.ai (Grok with live X access)",
		TookMs: time.Since(start).Milliseconds(),
	}, nil
}
