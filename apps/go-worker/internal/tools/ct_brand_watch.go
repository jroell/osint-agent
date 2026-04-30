package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type CTBrandHit struct {
	Domain        string  `json:"domain"`
	CommonName    string  `json:"common_name"`
	Issuer        string  `json:"issuer"`
	IssuedAt      string  `json:"issued_at"`
	NotBefore     string  `json:"not_before"`
	NotAfter      string  `json:"not_after"`
	AgeHours      float64 `json:"age_hours"`
	LevDistance   int     `json:"lev_distance_from_brand"`
	ImpersonationScore int `json:"impersonation_score"` // 0-100
	Threat        string  `json:"threat"`              // critical | high | medium | low | benign
	Reason        string  `json:"reason"`
	CrtShID       int64   `json:"crt_sh_id"`
}

type CTBrandWatchOutput struct {
	Brand            string       `json:"brand"`
	WindowHours      int          `json:"window_hours"`
	Pattern          string       `json:"pattern_used"`
	TotalCerts       int          `json:"total_certs_examined"`
	NewlyIssued      int          `json:"newly_issued_in_window"`
	Hits             []CTBrandHit `json:"hits"`
	CriticalCount    int          `json:"critical_count"`
	HighCount        int          `json:"high_count"`
	MediumCount      int          `json:"medium_count"`
	BenignBrandOwned int          `json:"benign_brand_owned_count"` // certs that match the brand BUT belong to legit brand domains
	Source           string       `json:"source"`
	TookMs           int64        `json:"tookMs"`
	Note             string       `json:"note,omitempty"`
}

// levenshtein computes edit distance between two strings.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	for i := range prev {
		prev[i] = i
	}
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = minInt3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = curr
	}
	return prev[len(b)]
}

func minInt3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// extractBrandStem strips TLD from domain to compare the meaningful stem.
// e.g. "vurvey.app" → "vurvey", "anthropic-secure.io" → "anthropic-secure".
func extractBrandStem(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(domain, "*.")
	domain = strings.TrimPrefix(domain, "www.")
	parts := strings.Split(domain, ".")
	if len(parts) <= 2 {
		return parts[0]
	}
	// Take the SLD (second-level domain) as the brand stem.
	return parts[len(parts)-2]
}

// CTBrandWatch monitors Certificate Transparency logs for newly-issued
// certificates that visually resemble a target brand. Uses crt.sh's free
// JSON API with wildcard search.
//
// Algorithm:
//  1. Query crt.sh for `%<brand>%` matching certs.
//  2. Filter to certs issued within the watch window (default 168h = 7d).
//  3. For each matching cert, score impersonation likelihood:
//     - Lev distance from brand stem (small = high score)
//     - Cert age (hours-old = scarier than weeks-old)
//     - Suspicious-token-presence (login/secure/account/verify in subdomain)
//     - Whether the apex matches the legit brand (true = benign)
//  4. Rank threats: critical > high > medium > low > benign.
//
// Free, no API key — uses public crt.sh JSON. Subject to rate limiting at
// the source if abused; we use ?match=ILIKE for fuzzy SQL matching.
func CTBrandWatch(ctx context.Context, input map[string]any) (*CTBrandWatchOutput, error) {
	brand, _ := input["brand"].(string)
	brand = strings.TrimSpace(strings.ToLower(brand))
	if brand == "" {
		return nil, errors.New("input.brand required (e.g. 'vurvey' or 'anthropic')")
	}
	// If user gave a full domain, take the stem.
	if strings.Contains(brand, ".") {
		brand = extractBrandStem(brand)
	}

	windowHours := 168 // default 7 days
	if v, ok := input["window_hours"].(float64); ok && int(v) > 0 && int(v) <= 720 {
		windowHours = int(v)
	}
	limit := 200
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 2000 {
		limit = int(v)
	}
	// Allow caller to define which apex domains are "owned" by the brand
	// (so they're filtered out as benign).
	var ownedDomains []string
	if v, ok := input["owned_apexes"].([]any); ok {
		for _, x := range v {
			if s, ok := x.(string); ok {
				ownedDomains = append(ownedDomains, strings.ToLower(s))
			}
		}
	}

	start := time.Now()
	pattern := "%" + brand + "%"

	// crt.sh free JSON API. Notoriously flaky (502s, slow). Retry once on 5xx.
	endpoint := fmt.Sprintf("https://crt.sh/?q=%s&output=json&limit=%d", url.QueryEscape(pattern), limit)
	var body []byte
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second) // backoff
		}
		cctx, cancel := context.WithTimeout(ctx, 35*time.Second)
		req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
		req.Header.Set("User-Agent", "osint-agent/ct-brand-watch")
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("crt.sh fetch failed: %w", err)
			continue
		}
		body, _ = io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		cancel()
		if resp.StatusCode == 200 {
			lastErr = nil
			break
		}
		lastErr = fmt.Errorf("crt.sh status %d: %s", resp.StatusCode, truncate(string(body), 150))
	}
	if lastErr != nil {
		return nil, fmt.Errorf("crt.sh upstream error after retry: %w (crt.sh is notoriously flaky — try again in a few minutes)", lastErr)
	}

	var entries []struct {
		ID        int64  `json:"id"`
		EntryID   int64  `json:"entry_timestamp_id"`
		CommonName string `json:"common_name"`
		NameValue string `json:"name_value"` // newline-separated SANs
		Issuer    string `json:"issuer_name"`
		NotBefore string `json:"not_before"`
		NotAfter  string `json:"not_after"`
		EntryTimestamp string `json:"entry_timestamp"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("crt.sh response parse failed (got %d bytes): %w", len(body), err)
	}

	cutoff := time.Now().Add(-time.Duration(windowHours) * time.Hour)

	hits := []CTBrandHit{}
	criticalCount, highCount, mediumCount, benignCount := 0, 0, 0, 0
	seen := map[string]bool{}

	for _, e := range entries {
		// Each entry may have multiple SANs in name_value, newline-separated.
		domains := strings.Split(e.NameValue, "\n")
		for _, d := range domains {
			d = strings.ToLower(strings.TrimSpace(d))
			if d == "" {
				continue
			}
			// Dedupe at domain+cert-id level.
			key := fmt.Sprintf("%d|%s", e.ID, d)
			if seen[key] {
				continue
			}
			seen[key] = true

			// Parse cert issuance time.
			issuedAt, err := time.Parse("2006-01-02T15:04:05.999", e.EntryTimestamp)
			if err != nil {
				issuedAt, _ = time.Parse("2006-01-02T15:04:05", e.EntryTimestamp)
			}
			if issuedAt.IsZero() {
				issuedAt, _ = time.Parse("2006-01-02T15:04:05", e.NotBefore)
			}
			if issuedAt.IsZero() || issuedAt.Before(cutoff) {
				continue
			}

			ageHours := time.Since(issuedAt).Hours()
			stem := extractBrandStem(d)
			lev := levenshtein(stem, brand)

			// Score impersonation.
			score := 0
			reasons := []string{}

			// Lev distance: brand stem similarity
			if lev == 0 {
				score += 30
				reasons = append(reasons, "exact-stem-match")
			} else if lev <= 2 {
				score += 50
				reasons = append(reasons, fmt.Sprintf("lev=%d (very close)", lev))
			} else if lev <= 4 {
				score += 35
				reasons = append(reasons, fmt.Sprintf("lev=%d (close)", lev))
			} else {
				score += 10
			}

			// Cert age: fresh = scarier
			if ageHours <= 24 {
				score += 25
				reasons = append(reasons, fmt.Sprintf("issued %.1fh ago", ageHours))
			} else if ageHours <= 72 {
				score += 15
			} else if ageHours <= 168 {
				score += 5
			}

			// Suspicious-token tests in subdomain.
			suspiciousTokens := []string{"login", "secure", "account", "verify", "auth", "signin", "support", "billing", "admin", "portal", "update", "confirm", "manage"}
			low := strings.ToLower(d)
			for _, t := range suspiciousTokens {
				if strings.Contains(low, t) {
					score += 15
					reasons = append(reasons, "suspicious-token:"+t)
					break
				}
			}

			// Owned-apex check.
			isOwned := false
			for _, owned := range ownedDomains {
				if strings.HasSuffix(d, "."+owned) || d == owned {
					isOwned = true
					break
				}
			}
			if isOwned {
				score = 0
				reasons = []string{"owned-apex"}
				benignCount++
			}

			if score > 100 {
				score = 100
			}

			threat := "low"
			switch {
			case isOwned:
				threat = "benign"
			case score >= 70:
				threat = "critical"
				criticalCount++
			case score >= 50:
				threat = "high"
				highCount++
			case score >= 30:
				threat = "medium"
				mediumCount++
			}

			hits = append(hits, CTBrandHit{
				Domain:           d,
				CommonName:       e.CommonName,
				Issuer:           e.Issuer,
				IssuedAt:         e.EntryTimestamp,
				NotBefore:        e.NotBefore,
				NotAfter:         e.NotAfter,
				AgeHours:         round1(ageHours),
				LevDistance:      lev,
				ImpersonationScore: score,
				Threat:           threat,
				Reason:           strings.Join(reasons, ", "),
				CrtShID:          e.ID,
			})
		}
	}

	// Sort by impersonation score desc.
	sort.Slice(hits, func(i, j int) bool { return hits[i].ImpersonationScore > hits[j].ImpersonationScore })

	out := &CTBrandWatchOutput{
		Brand:            brand,
		WindowHours:      windowHours,
		Pattern:          pattern,
		TotalCerts:       len(entries),
		NewlyIssued:      len(hits),
		Hits:             hits,
		CriticalCount:    criticalCount,
		HighCount:        highCount,
		MediumCount:      mediumCount,
		BenignBrandOwned: benignCount,
		Source:           "crt.sh",
		TookMs:           time.Since(start).Milliseconds(),
	}
	if len(hits) == 0 {
		out.Note = fmt.Sprintf("No certs matching '%s' issued in last %dh. Try widening window_hours or check brand spelling.", brand, windowHours)
	} else if criticalCount > 0 {
		out.Note = fmt.Sprintf("⚠️  %d CRITICAL impersonation candidates issued in window — verify with favicon_pivot/tracker_extract immediately", criticalCount)
	}
	return out, nil
}

func round1(f float64) float64 {
	return float64(int64(f*10)) / 10
}
