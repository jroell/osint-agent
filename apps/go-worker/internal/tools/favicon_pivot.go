package tools

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
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

type FaviconPivotOutput struct {
	InputURL      string `json:"input_url"`
	FaviconURL    string `json:"favicon_url"`
	FaviconBytes  int    `json:"favicon_bytes"`
	FaviconStatus int    `json:"favicon_status"`
	HashMD5       string `json:"hash_md5"`
	HashSHA256    string `json:"hash_sha256"`
	HashMMH3      int32  `json:"hash_mmh3_fofa"`     // signed int32 — what Shodan/FOFA/ZoomEye index by
	HashMMH3Hex   string `json:"hash_mmh3_fofa_hex"` // unsigned hex form for some tools
	// Cross-platform pivot queries — paste any of these into the platform's
	// search UI/API to find every other site sharing this favicon.
	ShodanQuery   string `json:"shodan_query"`
	FOFAQuery     string `json:"fofa_query"`
	ZoomEyeQuery  string `json:"zoomeye_query"`
	CensysQuery   string `json:"censys_query"`
	URLScanQuery  string `json:"urlscan_query"`
	// Auto-pivot results from urlscan (free tier: hash:<sha256>).
	URLScanPivots []URLScanHit `json:"urlscan_pivots,omitempty"`
	URLScanCount  int          `json:"urlscan_count"`
	UniqueDomains int          `json:"unique_domains"`
	UniqueIPs     int          `json:"unique_ips"`
	UniqueASNs    int          `json:"unique_asns"`
	Source        string       `json:"source"`
	TookMs        int64        `json:"tookMs"`
	Note          string       `json:"note,omitempty"`
}

// FaviconPivot is the canonical bug-bounty / OSINT entity-resolution primitive:
// fetch a site's favicon, compute the FOFA-style mmh3 hash, then pivot through
// urlscan.io's archive to find every other site serving the same favicon.
//
// Why this works: favicons rarely change. When the same favicon appears on
// many hosts, those hosts almost always share infrastructure: a hidden origin
// behind a CDN, a brand-related subdomain, a phishing site copying a target's
// favicon, or a forgotten staging environment.
//
// The mmh3 hash returned here is *exactly* what Shodan, FOFA, ZoomEye, and
// Censys all index by — paste the included `shodan_query` into any of those
// platforms' search interfaces for cross-source pivots.
//
// HTML <link rel="icon"> autodiscovery is attempted first; falls back to
// /favicon.ico if no link element is found.
func FaviconPivot(ctx context.Context, input map[string]any) (*FaviconPivotOutput, error) {
	rawURL, _ := input["url"].(string)
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("input.url required (target site URL)")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("input.url must be absolute http(s)")
	}
	pivotLimit := 50
	if v, ok := input["pivot_limit"].(float64); ok && v > 0 {
		pivotLimit = int(v)
		if pivotLimit > 200 {
			pivotLimit = 200
		}
	}

	start := time.Now()

	// Step 1: discover favicon URL by fetching HTML and parsing <link rel="icon">.
	// Fall back to /favicon.ico if no link element is found.
	favURL, err := discoverFaviconURL(ctx, rawURL)
	if err != nil || favURL == "" {
		// Fallback: <host>/favicon.ico
		favURL = u.Scheme + "://" + u.Host + "/favicon.ico"
	}

	// Step 2: fetch the favicon bytes.
	body, status, err := fetchFavicon(ctx, favURL)
	if err != nil {
		return nil, fmt.Errorf("favicon fetch %s: %w", favURL, err)
	}
	if status != 200 || len(body) < 8 {
		return nil, fmt.Errorf("favicon fetch %s returned %d (%d bytes)", favURL, status, len(body))
	}

	// Step 3: compute hashes. mmh3 uses FOFA's quirk: base64 with line wrapping
	// matching Python's base64.encodebytes (76 chars per line + trailing newline).
	md5sum := md5.Sum(body)
	shaSum := sha256.Sum256(body)
	fofaInput := pythonBase64EncodeBytes(body)
	mmh3 := murmurHash3_32([]byte(fofaInput), 0)

	sha256Hex := hex.EncodeToString(shaSum[:])
	out := &FaviconPivotOutput{
		InputURL:      rawURL,
		FaviconURL:    favURL,
		FaviconBytes:  len(body),
		FaviconStatus: status,
		HashMD5:       hex.EncodeToString(md5sum[:]),
		HashSHA256:    sha256Hex,
		HashMMH3:      mmh3,
		HashMMH3Hex:   fmt.Sprintf("%08x", uint32(mmh3)),
		// Each platform indexes favicons by mmh3 (FOFA's int32 form). Paste any
		// of these into that platform's search to find related infrastructure.
		ShodanQuery:  fmt.Sprintf("http.favicon.hash:%d", mmh3),
		FOFAQuery:    fmt.Sprintf("icon_hash=\"%d\"", mmh3),
		ZoomEyeQuery: fmt.Sprintf("iconhash:\"%08x\"", uint32(mmh3)),
		CensysQuery:  fmt.Sprintf("services.http.response.favicons.murmur_hash: %d", mmh3),
		// urlscan free-tier search — by SHA256, the canonical resource hash.
		URLScanQuery: fmt.Sprintf("hash:%s", sha256Hex),
		Source:       "favicon-mmh3 + urlscan SHA256 pivot",
	}

	// Step 4: auto-pivot via urlscan_search using SHA256 (free-tier compatible).
	pivotInput := map[string]any{
		"query": fmt.Sprintf("hash:%s", sha256Hex),
		"size":  float64(pivotLimit),
	}
	pivotResult, perr := URLScanSearch(ctx, pivotInput)
	if perr == nil && pivotResult != nil {
		out.URLScanPivots = pivotResult.Results
		out.URLScanCount = pivotResult.Total
		out.UniqueDomains = pivotResult.UniqueDomains
		out.UniqueIPs = pivotResult.UniqueIPs
		out.UniqueASNs = pivotResult.UniqueASNs
		if pivotResult.Note != "" {
			out.Note = pivotResult.Note
		}
	} else if perr != nil {
		out.Note = "urlscan pivot failed: " + perr.Error()
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// linkRelRe — relaxed regex matching <link rel="…icon…" href="…">.
var linkRelRe = regexp.MustCompile(`(?is)<link[^>]+rel\s*=\s*["']?[^"'>]*icon[^"'>]*["']?[^>]+href\s*=\s*["']([^"']+)["']`)

// linkRelHrefFirstRe — for cases where href comes before rel.
var linkRelHrefFirstRe = regexp.MustCompile(`(?is)<link[^>]+href\s*=\s*["']([^"']+)["'][^>]+rel\s*=\s*["']?[^"'>]*icon[^"'>]*["']?`)

func discoverFaviconURL(ctx context.Context, pageURL string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}

	var match []byte
	if m := linkRelRe.FindSubmatch(body); len(m) >= 2 {
		match = m[1]
	} else if m := linkRelHrefFirstRe.FindSubmatch(body); len(m) >= 2 {
		match = m[1]
	}
	if match == nil {
		return "", nil
	}

	// Resolve relative URL against the final URL after redirects.
	base := resp.Request.URL
	rel, err := url.Parse(string(match))
	if err != nil {
		return "", err
	}
	return base.ResolveReference(rel).String(), nil
}

func fetchFavicon(ctx context.Context, favURL string) ([]byte, int, error) {
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, favURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "image/x-icon,image/png,image/svg+xml,image/*,*/*;q=0.5")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// pythonBase64EncodeBytes mimics Python's base64.encodebytes — standard b64
// with line wrapping every 76 chars and a trailing newline. This is what
// FOFA / Shodan / ZoomEye / Censys all hash to compute the favicon mmh3.
func pythonBase64EncodeBytes(data []byte) string {
	b64 := base64.StdEncoding.EncodeToString(data)
	var buf strings.Builder
	for i := 0; i < len(b64); i += 76 {
		end := i + 76
		if end > len(b64) {
			end = len(b64)
		}
		buf.WriteString(b64[i:end])
		buf.WriteByte('\n')
	}
	return buf.String()
}

// murmurHash3_32 is a Go implementation of the 32-bit Murmur3 hash used by
// FOFA, Shodan, ZoomEye, and Censys for favicon indexing. Returns signed int32
// (matches what these platforms store).
func murmurHash3_32(data []byte, seed uint32) int32 {
	const (
		c1 uint32 = 0xcc9e2d51
		c2 uint32 = 0x1b873593
	)
	h1 := seed
	length := len(data)
	nblocks := length / 4

	// body
	for i := 0; i < nblocks; i++ {
		k1 := uint32(data[i*4]) |
			uint32(data[i*4+1])<<8 |
			uint32(data[i*4+2])<<16 |
			uint32(data[i*4+3])<<24
		k1 *= c1
		k1 = (k1 << 15) | (k1 >> 17)
		k1 *= c2
		h1 ^= k1
		h1 = (h1 << 13) | (h1 >> 19)
		h1 = h1*5 + 0xe6546b64
	}

	// tail
	var k1 uint32
	tail := data[nblocks*4:]
	switch len(tail) & 3 {
	case 3:
		k1 ^= uint32(tail[2]) << 16
		fallthrough
	case 2:
		k1 ^= uint32(tail[1]) << 8
		fallthrough
	case 1:
		k1 ^= uint32(tail[0])
		k1 *= c1
		k1 = (k1 << 15) | (k1 >> 17)
		k1 *= c2
		h1 ^= k1
	}

	// finalization
	h1 ^= uint32(length)
	h1 ^= h1 >> 16
	h1 *= 0x85ebca6b
	h1 ^= h1 >> 13
	h1 *= 0xc2b2ae35
	h1 ^= h1 >> 16

	return int32(h1)
}
