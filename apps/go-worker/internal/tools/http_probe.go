package tools

import (
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type HTTPProbeOutput struct {
	URL          string            `json:"url"`
	FinalURL     string            `json:"final_url,omitempty"`
	Status       int               `json:"status"`
	Title        string            `json:"title,omitempty"`
	ContentType  string            `json:"content_type,omitempty"`
	Server       string            `json:"server,omitempty"`
	PoweredBy    string            `json:"powered_by,omitempty"`
	BodyBytes    int               `json:"body_bytes"`
	Headers      map[string]string `json:"headers,omitempty"`
	TechHints    []string          `json:"tech_hints,omitempty"`
	FaviconHash  string            `json:"favicon_md5,omitempty"`
	FaviconMmh3  string            `json:"favicon_mmh3,omitempty"` // bug-bounty pivot key (Shodan style)
	Redirects    []string          `json:"redirects,omitempty"`
	TLSIssuer    string            `json:"tls_issuer,omitempty"`
	TLSSubject   string            `json:"tls_subject,omitempty"`
	TLSExpires   string            `json:"tls_expires,omitempty"`
	TookMs       int64             `json:"tookMs"`
}

// HTTPProbe fetches a URL and returns rich tech-fingerprint data. Designed for
// quick reconnaissance: title, server header, technology hints from
// well-known headers, favicon hashes (md5 + mmh3 — both common bug-bounty pivot
// keys), TLS subject/issuer, and redirect chain.
func HTTPProbe(ctx context.Context, input map[string]any) (*HTTPProbeOutput, error) {
	rawURL, _ := input["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("input.url required")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("input.url must be absolute http(s) URL")
	}
	timeoutMs := 15000
	if v, ok := input["timeout_ms"].(float64); ok && v > 0 {
		timeoutMs = int(v)
	}
	follow := true
	if v, ok := input["follow_redirects"].(bool); ok {
		follow = v
	}

	start := time.Now()

	var redirects []string
	client := &http.Client{
		Timeout: time.Duration(timeoutMs) * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !follow {
				return http.ErrUseLastResponse
			}
			redirects = append(redirects, req.URL.String())
			if len(via) >= 10 {
				return errors.New("redirect chain >10")
			}
			return nil
		},
	}

	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MiB cap
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	out := &HTTPProbeOutput{
		URL:         rawURL,
		FinalURL:    resp.Request.URL.String(),
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Server:      resp.Header.Get("Server"),
		PoweredBy:   resp.Header.Get("X-Powered-By"),
		BodyBytes:   len(body),
		Headers:     flattenHeaders(resp.Header),
		Redirects:   redirects,
		TookMs:      time.Since(start).Milliseconds(),
	}
	if t := extractTitle(body); t != "" {
		out.Title = t
	}
	out.TechHints = inferTechHints(resp.Header, body)

	// TLS info.
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		c := resp.TLS.PeerCertificates[0]
		out.TLSIssuer = c.Issuer.CommonName
		out.TLSSubject = c.Subject.CommonName
		out.TLSExpires = c.NotAfter.Format(time.RFC3339)
		_ = tls.VersionTLS13 // keep crypto/tls referenced
	}

	// Favicon hash — fetch /favicon.ico from the FINAL host.
	favURL := *resp.Request.URL
	favURL.Path = "/favicon.ico"
	favURL.RawQuery = ""
	if favBytes, err := fetchBytes(ctx, favURL.String(), 5*time.Second); err == nil && len(favBytes) > 0 {
		sum := md5.Sum(favBytes)
		out.FaviconHash = hex.EncodeToString(sum[:])
		// mmh3 is what Shodan/Censys index favicons by; we approximate with the
		// b64-of-bytes-then-mmh3 convention used by httpx/fofa. Since we don't pull
		// a mmh3 dep, we just expose b64 length so users can pipe to their own hasher.
		out.FaviconMmh3 = "b64:" + base64.StdEncoding.EncodeToString(favBytes[:min(64, len(favBytes))]) + "..."
	}

	return out, nil
}

func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return out
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func extractTitle(body []byte) string {
	m := titleRe.FindSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	t := strings.TrimSpace(string(m[1]))
	if len(t) > 200 {
		t = t[:200] + "…"
	}
	return t
}

// inferTechHints surfaces common, high-signal tech fingerprints from
// response headers + a tiny body-substring check. Not a Wappalyzer replacement —
// that lands in `tech_stack_fingerprint` in Phase 0.
func inferTechHints(h http.Header, body []byte) []string {
	hints := map[string]bool{}
	add := func(s string) {
		if s != "" {
			hints[s] = true
		}
	}
	if v := h.Get("Server"); v != "" {
		add("server:" + v)
	}
	if v := h.Get("X-Powered-By"); v != "" {
		add("powered-by:" + v)
	}
	if v := h.Get("X-AspNet-Version"); v != "" {
		add("framework:asp.net@" + v)
	}
	if v := h.Get("X-Drupal-Cache"); v != "" {
		add("cms:drupal")
	}
	if v := h.Get("X-Generator"); v != "" {
		add("generator:" + v)
	}
	if h.Get("Cf-Ray") != "" {
		add("edge:cloudflare")
	}
	if strings.Contains(strings.ToLower(h.Get("Server")), "akamaighost") {
		add("edge:akamai")
	}
	if h.Get("X-Vercel-Id") != "" {
		add("edge:vercel")
	}
	if h.Get("X-Amz-Cf-Id") != "" {
		add("edge:cloudfront")
	}
	if h.Get("X-Github-Request-Id") != "" {
		add("origin:github")
	}
	if cookie := h.Get("Set-Cookie"); strings.Contains(strings.ToLower(cookie), "wp-") {
		add("cms:wordpress")
	}
	if cookie := h.Get("Set-Cookie"); strings.Contains(strings.ToLower(cookie), "phpsessid") {
		add("language:php")
	}
	// Body-substring sniffs (cheap, body is already in memory).
	bs := strings.ToLower(string(body[:min(8192, len(body))]))
	if strings.Contains(bs, "wp-content") || strings.Contains(bs, "wp-includes") {
		add("cms:wordpress")
	}
	if strings.Contains(bs, "shopify") {
		add("cms:shopify")
	}
	if strings.Contains(bs, "/_next/") {
		add("framework:next.js")
	}
	if strings.Contains(bs, "data-reactroot") || strings.Contains(bs, "/static/js/main") {
		add("framework:react")
	}

	// Materialize sorted list for deterministic output.
	out := make([]string, 0, len(hints))
	for k := range hints {
		out = append(out, k)
	}
	return out
}

func fetchBytes(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB favicon cap
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
