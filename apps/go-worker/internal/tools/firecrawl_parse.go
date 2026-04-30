package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FirecrawlParseOutput is the response.
type FirecrawlParseOutput struct {
	SourceURL    string         `json:"source_url,omitempty"`
	FileSize     int64          `json:"file_size_bytes,omitempty"`
	ContentType  string         `json:"content_type,omitempty"`
	NumPages     int            `json:"num_pages,omitempty"`
	Markdown     string         `json:"markdown,omitempty"`
	JSONOutput   map[string]any `json:"json_output,omitempty"`
	Summary      string         `json:"summary,omitempty"`
	HighlightFindings []string  `json:"highlight_findings"`
	Source       string         `json:"source"`
	TookMs       int64          `json:"tookMs"`
	Note         string         `json:"note,omitempty"`
}

// FirecrawlParse parses a PDF / Word doc / spreadsheet / RTF / HTML file
// via Firecrawl's `/v2/parse` endpoint. Free for low-volume use with
// FIRECRAWL_API_KEY.
//
// Why this matters for ER:
//   - Court filings, FOIA-released documents, financial reports, scientific
//     papers, leaked corporate documents are usually PDFs. Direct text
//     extraction (poppler/pdftotext) handles text-based PDFs but fails on
//     scanned/image PDFs.
//   - Firecrawl's Fire-PDF engine (Rust + neural document layout model)
//     auto-detects PDF type and chooses fast/auto/ocr extraction. Tables
//     get full markdown; formulas preserved in LaTeX; reading order
//     predicted neurally.
//   - Up to 50MB files supported. Apr 2026 release.
//
// Two input modes:
//   - file_url: tool fetches the file from URL and uploads to /parse
//   - structured_prompt: optional natural-language prompt to extract
//     structured JSON from the parsed text (combines parse + LLM extract
//     in one call)
//
// Use cases:
//   - Court filings (CourtListener provides PDF URLs)
//   - SEC EDGAR filings (PDF and DOC formats)
//   - FOIA releases
//   - Academic papers (arxiv URLs)
//   - Government documents
//   - Leaked corporate materials
func FirecrawlParse(ctx context.Context, input map[string]any) (*FirecrawlParseOutput, error) {
	apiKey := os.Getenv("FIRECRAWL_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("FIRECRAWL_API_KEY env var required")
	}
	fileURL, _ := input["file_url"].(string)
	fileURL = strings.TrimSpace(fileURL)
	if fileURL == "" {
		return nil, fmt.Errorf("input.file_url required (URL of PDF/DOC/XLSX/etc to parse)")
	}

	wantJSON := false
	structuredPrompt, _ := input["structured_prompt"].(string)
	structuredPrompt = strings.TrimSpace(structuredPrompt)
	if structuredPrompt != "" {
		wantJSON = true
	}
	wantSummary := false
	if v, ok := input["include_summary"].(bool); ok {
		wantSummary = v
	}

	out := &FirecrawlParseOutput{
		SourceURL: fileURL,
		Source:    "firecrawl.dev /v2/parse (Fire-PDF Rust engine)",
	}
	start := time.Now()

	// 1. Fetch the file
	fileCli := &http.Client{Timeout: 120 * time.Second}
	freq, _ := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
	freq.Header.Set("User-Agent", "osint-agent/0.1 (research)")
	fresp, err := fileCli.Do(freq)
	if err != nil {
		return nil, fmt.Errorf("fetch file: %w", err)
	}
	defer fresp.Body.Close()
	if fresp.StatusCode != 200 {
		return nil, fmt.Errorf("file fetch %d", fresp.StatusCode)
	}
	// Cap at 50MB (Firecrawl's limit)
	const maxBytes = 50 * 1024 * 1024
	fileBytes, err := io.ReadAll(io.LimitReader(fresp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if len(fileBytes) > maxBytes {
		return nil, fmt.Errorf("file too large (>50MB) — Firecrawl /parse limit")
	}
	out.FileSize = int64(len(fileBytes))
	out.ContentType = fresp.Header.Get("Content-Type")

	// 2. Build multipart upload to /parse
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// Filename derived from URL
	filename := filepath.Base(fileURL)
	if i := strings.Index(filename, "?"); i > 0 {
		filename = filename[:i]
	}
	if filename == "" || filename == "." || filename == "/" {
		filename = "file.pdf"
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(fileBytes); err != nil {
		return nil, err
	}
	// Options — v2 /parse expects format objects (with embedded prompt)
	formats := []any{"markdown"}
	if wantJSON {
		formats = append(formats, map[string]any{
			"type":   "json",
			"prompt": structuredPrompt,
		})
	}
	if wantSummary {
		formats = append(formats, "summary")
	}
	optsMap := map[string]any{"formats": formats}
	optsBytes, _ := json.Marshal(optsMap)
	if err := mw.WriteField("options", string(optsBytes)); err != nil {
		return nil, err
	}
	mw.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.firecrawl.dev/v2/parse", &buf)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("User-Agent", "osint-agent/0.1")

	pcli := &http.Client{Timeout: 240 * time.Second}
	resp, err := pcli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firecrawl parse: %w", err)
	}
	defer resp.Body.Close()
	rawResp, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("firecrawl %d: %s", resp.StatusCode, hfTruncate(string(rawResp), 300))
	}

	var parsed struct {
		Success bool `json:"success"`
		Data    struct {
			Markdown string                 `json:"markdown"`
			JSON     map[string]any         `json:"json"`
			Summary  string                 `json:"summary"`
			Metadata map[string]any         `json:"metadata"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rawResp, &parsed); err != nil {
		return nil, fmt.Errorf("firecrawl decode: %w", err)
	}
	if !parsed.Success {
		errMsg := parsed.Error
		if errMsg == "" {
			errMsg = hfTruncate(string(rawResp), 200)
		}
		return nil, fmt.Errorf("firecrawl parse failed: %s", errMsg)
	}

	out.Markdown = parsed.Data.Markdown
	out.JSONOutput = parsed.Data.JSON
	out.Summary = parsed.Data.Summary
	if n, ok := parsed.Data.Metadata["numPages"].(float64); ok {
		out.NumPages = int(n)
	}

	out.HighlightFindings = buildFirecrawlParseHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func buildFirecrawlParseHighlights(o *FirecrawlParseOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ parsed %s (%.2f MB, %d pages, %s)",
		o.SourceURL, float64(o.FileSize)/(1024*1024), o.NumPages, o.ContentType))
	hi = append(hi, fmt.Sprintf("📄 markdown size: %d chars", len(o.Markdown)))
	if o.Summary != "" {
		summ := o.Summary
		if len(summ) > 300 {
			summ = summ[:300] + "..."
		}
		hi = append(hi, "📋 summary: "+summ)
	}
	if len(o.JSONOutput) > 0 {
		hi = append(hi, fmt.Sprintf("🔍 %d structured fields extracted", len(o.JSONOutput)))
	}
	if len(o.Markdown) > 0 {
		// First 200 chars as preview
		preview := strings.Join(strings.Fields(o.Markdown), " ")
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		hi = append(hi, "first content: "+preview)
	}
	return hi
}
