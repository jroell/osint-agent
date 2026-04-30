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

// GeminiCitation is one grounding source.
type GeminiCitation struct {
	URI   string `json:"uri"`
	Title string `json:"title,omitempty"`
}

// GeminiSearchOutput is the search-grounded response.
type GeminiSearchOutput struct {
	Query             string           `json:"query"`
	Model             string           `json:"model"`
	AnswerText        string           `json:"answer_text"`
	SearchQueries     []string         `json:"search_queries_used,omitempty"`
	Citations         []GeminiCitation `json:"citations,omitempty"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source            string           `json:"source"`
	TookMs            int64            `json:"tookMs"`
}

// GeminiURLOutput is the url-context response.
type GeminiURLOutput struct {
	URLs              []string `json:"urls"`
	Prompt            string   `json:"prompt"`
	Model             string   `json:"model"`
	AnswerText        string   `json:"answer_text"`
	HighlightFindings []string `json:"highlight_findings"`
	Source            string   `json:"source"`
	TookMs            int64    `json:"tookMs"`
}

// GeminiYouTubeOutput is the native YouTube understanding response.
type GeminiYouTubeOutput struct {
	VideoURL          string   `json:"video_url"`
	Prompt            string   `json:"prompt"`
	Model             string   `json:"model"`
	MediaResolution   string   `json:"media_resolution,omitempty"`
	AnswerText        string   `json:"answer_text"`
	HighlightFindings []string `json:"highlight_findings"`
	Source            string   `json:"source"`
	TookMs            int64    `json:"tookMs"`
}

// helper: pick API key with fallback
func geminiAPIKey() (string, error) {
	if k := os.Getenv("GOOGLE_AI_API_KEY"); k != "" {
		return k, nil
	}
	if k := os.Getenv("GEMINI_API_KEY"); k != "" {
		return k, nil
	}
	if k := os.Getenv("GOOGLE_AI_API_KEY_BACKUP"); k != "" {
		return k, nil
	}
	return "", fmt.Errorf("GOOGLE_AI_API_KEY (or GEMINI_API_KEY) env var required")
}

// raw response shape
type geminiResp struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		GroundingMetadata struct {
			WebSearchQueries []string `json:"webSearchQueries"`
			GroundingChunks  []struct {
				Web struct {
					URI   string `json:"uri"`
					Title string `json:"title"`
				} `json:"web"`
			} `json:"groundingChunks"`
		} `json:"groundingMetadata"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func geminiCall(ctx context.Context, model string, body map[string]any) (*geminiResp, error) {
	apiKey, err := geminiAPIKey()
	if err != nil {
		return nil, err
	}
	bodyBytes, _ := json.Marshal(body)
	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")
	cli := &http.Client{Timeout: 240 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("gemini %d: %s", resp.StatusCode, hfTruncate(string(rawBody), 400))
	}
	var parsed geminiResp
	if err := json.Unmarshal(rawBody, &parsed); err != nil {
		return nil, fmt.Errorf("gemini decode: %w", err)
	}
	if parsed.Error.Message != "" {
		return nil, fmt.Errorf("gemini api: %s", parsed.Error.Message)
	}
	return &parsed, nil
}

func extractGeminiText(resp *geminiResp) string {
	if len(resp.Candidates) == 0 {
		return ""
	}
	parts := resp.Candidates[0].Content.Parts
	out := ""
	for _, p := range parts {
		out += p.Text
	}
	return out
}

// =====================================================================
// GeminiSearchGrounded — Gemini + Google Search tool with citations
// =====================================================================
//
// Uses the google_search built-in tool. Gemini issues N web searches
// from the prompt, reads the results, and answers with citations.
// Returns raw answer text + the search queries Gemini ran + the
// grounding URLs (citations).
//
// Why this matters for ER:
//   - Distinct from tavily_search / google_news_recent / firecrawl_search:
//     Gemini synthesizes across multiple sources and returns a single
//     coherent narrative answer with citations.
//   - The web search queries Gemini USED reveal what it searched —
//     useful for transparent reasoning trail.
//   - Strong for any "what's the latest on X" / "tell me about Y"
//     question where you want both an answer and verifiable sources.
func GeminiSearchGrounded(ctx context.Context, input map[string]any) (*GeminiSearchOutput, error) {
	prompt, _ := input["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("input.prompt required")
	}
	model, _ := input["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		model = "gemini-3.1-pro-preview"
	}
	out := &GeminiSearchOutput{
		Query:  prompt,
		Model:  model,
		Source: "ai.google.dev/gemini-api (google_search tool)",
	}
	start := time.Now()

	body := map[string]any{
		"contents": []any{map[string]any{
			"parts": []any{map[string]any{"text": prompt}},
		}},
		"tools": []any{map[string]any{"google_search": map[string]any{}}},
	}
	resp, err := geminiCall(ctx, model, body)
	if err != nil {
		return nil, err
	}
	out.AnswerText = extractGeminiText(resp)
	if len(resp.Candidates) > 0 {
		gm := resp.Candidates[0].GroundingMetadata
		out.SearchQueries = gm.WebSearchQueries
		for _, ch := range gm.GroundingChunks {
			out.Citations = append(out.Citations, GeminiCitation{URI: ch.Web.URI, Title: ch.Web.Title})
		}
	}
	out.HighlightFindings = []string{
		fmt.Sprintf("✓ Gemini %s answered '%s' in %d chars", model, prompt, len(out.AnswerText)),
	}
	if len(out.SearchQueries) > 0 {
		out.HighlightFindings = append(out.HighlightFindings,
			fmt.Sprintf("🔎 %d Google searches executed: %s", len(out.SearchQueries), strings.Join(out.SearchQueries, " | ")))
	}
	if len(out.Citations) > 0 {
		out.HighlightFindings = append(out.HighlightFindings,
			fmt.Sprintf("📚 %d grounding citations", len(out.Citations)))
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// =====================================================================
// GeminiURLContext — Gemini fetches and analyzes any URL(s)
// =====================================================================
//
// Uses the url_context built-in tool. Gemini fetches up to 20 URLs
// (PDFs, HTML, public files), reads them, and answers a question about
// them. Strong for: "summarize this PDF", "extract these fields from
// this article", "compare these two competitor pages".
//
// Why this matters for ER:
//   - Single-call analysis across multiple URLs without manual download.
//   - Handles PDFs natively (no firecrawl_parse needed for simple cases).
//   - Strong for cross-document analysis: extract names from a court
//     filing PDF, compare press releases from two companies, etc.
func GeminiURLContext(ctx context.Context, input map[string]any) (*GeminiURLOutput, error) {
	prompt, _ := input["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("input.prompt required")
	}
	urlsArg, _ := input["urls"].([]any)
	urls := []string{}
	for _, u := range urlsArg {
		if s, ok := u.(string); ok && strings.TrimSpace(s) != "" {
			urls = append(urls, strings.TrimSpace(s))
		}
	}
	// Also accept single url string
	if singleURL, ok := input["url"].(string); ok && strings.TrimSpace(singleURL) != "" {
		urls = append(urls, strings.TrimSpace(singleURL))
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("input.urls (array) or input.url (string) required")
	}
	model, _ := input["model"].(string)
	if model == "" {
		model = "gemini-3.1-pro-preview"
	}
	out := &GeminiURLOutput{
		URLs:   urls,
		Prompt: prompt,
		Model:  model,
		Source: "ai.google.dev/gemini-api (url_context tool)",
	}
	start := time.Now()

	// Build prompt mentioning the URLs (Gemini fetches them via url_context)
	urlList := strings.Join(urls, "\n")
	fullPrompt := fmt.Sprintf("%s\n\nURLs to analyze:\n%s", prompt, urlList)
	body := map[string]any{
		"contents": []any{map[string]any{
			"parts": []any{map[string]any{"text": fullPrompt}},
		}},
		"tools": []any{map[string]any{"url_context": map[string]any{}}},
	}
	resp, err := geminiCall(ctx, model, body)
	if err != nil {
		return nil, err
	}
	out.AnswerText = extractGeminiText(resp)
	out.HighlightFindings = []string{
		fmt.Sprintf("✓ Gemini %s analyzed %d URLs (%d chars in answer)", model, len(urls), len(out.AnswerText)),
	}
	for _, u := range urls {
		out.HighlightFindings = append(out.HighlightFindings, "  url: "+u)
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// =====================================================================
// GeminiYouTubeUnderstanding — native YouTube video Q&A (no transcript needed)
// =====================================================================
//
// Gemini natively ingests YouTube videos via fileData.fileUri — no
// transcript extraction needed. Can answer questions about: visual
// content (who appears, what's on screen, slides), audio (who is
// speaking, accent, music), and combined (e.g. "who is in this picture
// shown at 5:30?"). For long videos (>30 min) use mediaResolution=low
// to fit within the 1M context window.
//
// Why this matters for ER:
//   - youtube_transcript only gives text. Gemini can answer:
//     - "Who else is in this video besides the host?"
//     - "What city's skyline appears at 12:30?"
//     - "What slides are shown at 17:00?"
//     - "Is this person speaking in English with a British accent?"
//   - For OSINT, video-visual-and-audio understanding > pure transcript.
//   - mediaResolution=low extends to ~5-hour videos but loses fine
//     visual detail. Default=high for short videos (<10 min).
func GeminiYouTubeUnderstanding(ctx context.Context, input map[string]any) (*GeminiYouTubeOutput, error) {
	videoURL, _ := input["video_url"].(string)
	videoURL = strings.TrimSpace(videoURL)
	if videoURL == "" {
		return nil, fmt.Errorf("input.video_url required (must be a YouTube URL)")
	}
	prompt, _ := input["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("input.prompt required (e.g. 'Summarize this video' or 'Who is the speaker?')")
	}
	model, _ := input["model"].(string)
	if model == "" {
		model = "gemini-3.1-pro-preview"
	}
	mediaResolution, _ := input["media_resolution"].(string)
	mediaResolution = strings.ToLower(strings.TrimSpace(mediaResolution))
	if mediaResolution == "" {
		mediaResolution = "low" // safer default for long videos
	}
	switch mediaResolution {
	case "low", "medium", "default":
	default:
		return nil, fmt.Errorf("media_resolution must be one of: low, medium, default")
	}

	out := &GeminiYouTubeOutput{
		VideoURL:        videoURL,
		Prompt:          prompt,
		Model:           model,
		MediaResolution: mediaResolution,
		Source:          "ai.google.dev/gemini-api (native YouTube ingestion)",
	}
	start := time.Now()

	parts := []any{
		map[string]any{
			"file_data": map[string]any{
				"file_uri": videoURL,
			},
		},
		map[string]any{"text": prompt},
	}
	body := map[string]any{
		"contents": []any{map[string]any{"parts": parts}},
		"generationConfig": map[string]any{
			"mediaResolution": "MEDIA_RESOLUTION_" + strings.ToUpper(mediaResolution),
		},
	}
	resp, err := geminiCall(ctx, model, body)
	if err != nil {
		return nil, err
	}
	out.AnswerText = extractGeminiText(resp)
	out.HighlightFindings = []string{
		fmt.Sprintf("✓ Gemini %s analyzed %s with media_resolution=%s (%d chars)", model, videoURL, mediaResolution, len(out.AnswerText)),
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
