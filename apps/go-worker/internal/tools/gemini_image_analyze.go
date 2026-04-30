package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GeminiImageAnalyzeOutput is the response.
type GeminiImageAnalyzeOutput struct {
	Prompt            string   `json:"prompt"`
	ImageURLs         []string `json:"image_urls,omitempty"`
	ImageCount        int      `json:"image_count"`
	ImageMimeTypes    []string `json:"image_mime_types,omitempty"`
	ImageSizesBytes   []int    `json:"image_sizes_bytes,omitempty"`
	Model             string   `json:"model"`
	AnswerText        string   `json:"answer_text"`
	HighlightFindings []string `json:"highlight_findings"`
	Source            string   `json:"source"`
	TookMs            int64    `json:"tookMs"`
}

// GeminiImageAnalyze passes one or more images to Gemini's multimodal endpoint
// for visual OSINT. Uses inline_data (base64) since file_data only accepts
// Files API URIs (gs:// or files/...).
//
// Why this matters for ER:
//   - Distinct from `geo_vision` (geo-only) and `reverse_image` (similarity
//     search) and `exif` (metadata only): Gemini reasons about the *content*
//     of an image — landmarks, faces (with safety constraints), text/OCR,
//     scene context, document layout, biological identification.
//   - Multi-image input enables comparison: "do these two photos show the
//     same person?" / "are these two screenshots from the same web page at
//     different times?" / "which of these is the original and which is a
//     manipulated copy?"
//   - Document images (court filing scans, ID photos, screenshots of
//     leaked chats) often contain critical text that pure OCR misses
//     because of layout/handwriting/redactions; Gemini handles all of
//     these in one call.
//
// Use cases:
//   - Landmark / location identification from a vacation photo (geo-OSINT)
//   - Document OCR + structured extraction from scanned PDFs/images
//   - Compare a known-good company logo against a phishing-page screenshot
//   - Verify whether a screenshot has been manipulated
//   - Identify species/objects/vehicles in an image
//   - Read partial / blurred / handwritten text in a leaked image
//
// REQUIRES GOOGLE_AI_API_KEY (or GEMINI_API_KEY).
func GeminiImageAnalyze(ctx context.Context, input map[string]any) (*GeminiImageAnalyzeOutput, error) {
	prompt, _ := input["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("input.prompt required (e.g. 'What is in this image?')")
	}

	// Accept image_url (single) OR image_urls (array)
	urls := []string{}
	if single, ok := input["image_url"].(string); ok && strings.TrimSpace(single) != "" {
		urls = append(urls, strings.TrimSpace(single))
	}
	if arr, ok := input["image_urls"].([]any); ok {
		for _, u := range arr {
			if s, ok := u.(string); ok && strings.TrimSpace(s) != "" {
				urls = append(urls, strings.TrimSpace(s))
			}
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("input.image_url (string) or input.image_urls (array) required")
	}
	if len(urls) > 8 {
		return nil, fmt.Errorf("max 8 images per call (got %d)", len(urls))
	}

	model, _ := input["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		model = "gemini-3.1-pro-preview"
	}

	out := &GeminiImageAnalyzeOutput{
		Prompt:     prompt,
		ImageURLs:  urls,
		ImageCount: len(urls),
		Model:      model,
		Source:     "ai.google.dev/gemini-api (multimodal inline_data)",
	}
	start := time.Now()

	// Step 1: fetch each image, base64-encode
	cli := &http.Client{Timeout: 60 * time.Second}
	parts := []map[string]any{}
	for _, u := range urls {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; osint-agent/0.1)")
		req.Header.Set("Accept", "image/*")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", u, err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20)) // 20MB cap per image
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("fetch %s status %d", u, resp.StatusCode)
		}
		mime := resp.Header.Get("Content-Type")
		if i := strings.Index(mime, ";"); i >= 0 {
			mime = strings.TrimSpace(mime[:i])
		}
		if mime == "" || !strings.HasPrefix(mime, "image/") {
			// Sniff from extension as fallback
			low := strings.ToLower(u)
			switch {
			case strings.HasSuffix(low, ".png"):
				mime = "image/png"
			case strings.HasSuffix(low, ".jpg") || strings.HasSuffix(low, ".jpeg"):
				mime = "image/jpeg"
			case strings.HasSuffix(low, ".webp"):
				mime = "image/webp"
			case strings.HasSuffix(low, ".gif"):
				mime = "image/gif"
			default:
				mime = "image/jpeg" // best guess
			}
		}
		out.ImageMimeTypes = append(out.ImageMimeTypes, mime)
		out.ImageSizesBytes = append(out.ImageSizesBytes, len(body))
		b64 := base64.StdEncoding.EncodeToString(body)
		parts = append(parts, map[string]any{
			"inline_data": map[string]any{
				"mime_type": mime,
				"data":      b64,
			},
		})
	}
	parts = append(parts, map[string]any{"text": prompt})

	// Step 2: build request
	body := map[string]any{
		"contents": []any{map[string]any{"parts": parts}},
	}
	bodyBytes, _ := json.Marshal(body)

	apiKey, err := geminiAPIKey()
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1")

	gemCli := &http.Client{Timeout: 240 * time.Second}
	resp, err := gemCli.Do(req)
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

	out.AnswerText = extractGeminiText(&parsed)

	out.HighlightFindings = []string{
		fmt.Sprintf("✓ Gemini %s analyzed %d image(s) (%d chars in answer)", model, out.ImageCount, len(out.AnswerText)),
	}
	for i, u := range urls {
		out.HighlightFindings = append(out.HighlightFindings,
			fmt.Sprintf("  img %d (%.1fKB %s): %s", i+1, float64(out.ImageSizesBytes[i])/1024.0, out.ImageMimeTypes[i], u))
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
