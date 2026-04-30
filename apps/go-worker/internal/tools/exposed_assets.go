package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type AssetCheck struct {
	URL        string `json:"url"`
	Provider   string `json:"provider"`     // "s3" | "gcs" | "azure"
	Status     int    `json:"status"`
	Exists     bool   `json:"exists"`
	Listable   bool   `json:"listable"`     // public read-listing of the bucket contents
	Permission string `json:"permission"`   // "public-read" | "public-list" | "private" | "not-found" | "unknown"
	Snippet    string `json:"snippet,omitempty"`
	TookMs     int64  `json:"tookMs"`
}

type ExposedAssetsOutput struct {
	Target     string       `json:"target"`
	Candidates int          `json:"candidates"`
	Hits       []AssetCheck `json:"hits"`
	TookMs     int64        `json:"tookMs"`
}

// commonSuffixes are the most-tested bucket-name patterns in real bug-bounty work.
// Order roughly by hit-rate (raw name first, then -prod / -dev / -backup / -assets, etc.).
var bucketSuffixes = []string{
	"",
	"-prod", "-staging", "-dev", "-test", "-qa",
	"-backup", "-backups", "-archive",
	"-assets", "-static", "-media", "-images", "-photos", "-uploads",
	"-data", "-logs", "-public", "-private", "-internal",
	"-app", "-www", "-web", "-cdn",
}

// ExposedAssetFind enumerates probable cloud-storage bucket names derived from the
// input target (a brand/domain root, e.g. "anthropic" or "anthropic.com") and probes
// each across S3 / GCS / Azure Blob. Reports HTTP status, public-read vs public-list
// permission, and a body snippet. Read-only — never writes.
func ExposedAssetFind(ctx context.Context, input map[string]any) (*ExposedAssetsOutput, error) {
	target, _ := input["target"].(string)
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return nil, errors.New("input.target required (brand/company/domain root)")
	}
	// Normalize "anthropic.com" → "anthropic" and keep the domain form too.
	roots := []string{target}
	if strings.Contains(target, ".") {
		bare := target[:strings.Index(target, ".")]
		if bare != target {
			roots = append(roots, bare)
		}
	}

	maxConcurrent := 16
	if v, ok := input["concurrency"].(float64); ok && v > 0 {
		maxConcurrent = int(v)
	}

	start := time.Now()
	var candidates []struct {
		Provider, URL, Bucket string
	}
	for _, root := range roots {
		for _, suf := range bucketSuffixes {
			b := root + suf
			candidates = append(candidates,
				struct{ Provider, URL, Bucket string }{
					"s3", fmt.Sprintf("https://%s.s3.amazonaws.com/", b), b,
				},
				struct{ Provider, URL, Bucket string }{
					"gcs", fmt.Sprintf("https://storage.googleapis.com/%s/", b), b,
				},
				struct{ Provider, URL, Bucket string }{
					"azure", fmt.Sprintf("https://%s.blob.core.windows.net/?comp=list", b), b,
				},
			)
		}
	}

	out := &ExposedAssetsOutput{
		Target:     target,
		Candidates: len(candidates),
		Hits:       []AssetCheck{},
	}
	results := make(chan AssetCheck, len(candidates))
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for _, c := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func(c struct{ Provider, URL, Bucket string }) {
			defer wg.Done()
			defer func() { <-sem }()
			r := probeBucket(ctx, c.Provider, c.URL)
			results <- r
		}(c)
	}
	go func() { wg.Wait(); close(results) }()

	for r := range results {
		// Only surface meaningful results: exists OR readable. Skip pure 404 noise.
		if r.Exists || r.Listable {
			out.Hits = append(out.Hits, r)
		}
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func probeBucket(ctx context.Context, provider, url string) AssetCheck {
	start := time.Now()
	r := AssetCheck{URL: url, Provider: provider, Permission: "unknown"}
	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		r.TookMs = time.Since(start).Milliseconds()
		return r
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		r.TookMs = time.Since(start).Milliseconds()
		return r
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	r.Status = resp.StatusCode
	bodyStr := string(body)
	r.Snippet = truncate(bodyStr, 240)
	r.TookMs = time.Since(start).Milliseconds()

	switch provider {
	case "s3":
		switch {
		case strings.Contains(bodyStr, "<Code>NoSuchBucket"):
			r.Permission = "not-found"
		case strings.Contains(bodyStr, "<Code>AccessDenied"):
			r.Exists = true
			r.Permission = "private"
		case strings.Contains(bodyStr, "<ListBucketResult"):
			r.Exists = true
			r.Listable = true
			r.Permission = "public-list"
		case resp.StatusCode == 200:
			r.Exists = true
			r.Permission = "public-read"
		}
	case "gcs":
		switch {
		case strings.Contains(bodyStr, "NoSuchBucket"), strings.Contains(bodyStr, "Not Found"):
			r.Permission = "not-found"
		case strings.Contains(bodyStr, "AccessDenied"), strings.Contains(bodyStr, "does not have storage.objects.list access"):
			r.Exists = true
			r.Permission = "private"
		case strings.Contains(bodyStr, "<ListBucketResult"), strings.Contains(bodyStr, "<Contents>"):
			r.Exists = true
			r.Listable = true
			r.Permission = "public-list"
		case resp.StatusCode == 200:
			r.Exists = true
			r.Permission = "public-read"
		}
	case "azure":
		switch {
		case resp.StatusCode == 404 && strings.Contains(bodyStr, "ResourceNotFound"):
			r.Permission = "not-found"
		case resp.StatusCode == 404:
			r.Permission = "not-found"
		case resp.StatusCode == 403, strings.Contains(bodyStr, "AuthenticationFailed"):
			r.Exists = true
			r.Permission = "private"
		case strings.Contains(bodyStr, "<EnumerationResults"):
			r.Exists = true
			r.Listable = true
			r.Permission = "public-list"
		case resp.StatusCode == 200:
			r.Exists = true
			r.Permission = "public-read"
		}
	}
	return r
}
