package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type CTEntry struct {
	IssuerName string `json:"issuer_name,omitempty"`
	NameValue  string `json:"name_value"`
	NotBefore  string `json:"not_before,omitempty"`
	NotAfter   string `json:"not_after,omitempty"`
	EntryID    int64  `json:"id,omitempty"`
}

type CTOutput struct {
	Domain     string    `json:"domain"`
	Subdomains []string  `json:"subdomains"`         // deduped, sorted
	Count      int       `json:"count"`              // unique subdomains
	Entries    []CTEntry `json:"entries,omitempty"`  // raw cert rows (capped)
	TookMs     int64     `json:"tookMs"`
	Source     string    `json:"source"`             // "crt.sh" | "certspotter" | "crt.sh+certspotter"
	Note       string    `json:"note,omitempty"`     // populated when fallback fired
}

// CertTransparency queries crt.sh for all logged certificates whose SAN/CN matches
// `%.<domain>` (wildcard, includes subdomains). Returns a deduped subdomain list
// plus capped raw cert rows for forensics.
func CertTransparency(ctx context.Context, input map[string]any) (*CTOutput, error) {
	domain, _ := input["domain"].(string)
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return nil, errors.New("input.domain required")
	}
	includeRaw, _ := input["include_raw"].(bool)
	maxRaw := 500
	if v, ok := input["max_raw"].(float64); ok && v > 0 {
		maxRaw = int(v)
	}

	start := time.Now()

	// Primary: crt.sh — best historical depth, but frequently 502/slow.
	primaryRows, primaryErr := ctQueryCrtSh(ctx, domain)

	// Fallback: certspotter — far more reliable than crt.sh, smaller historical
	// window. Triggered when crt.sh fails OR returns nothing.
	var fallbackSubs []string
	var fallbackErr error
	usedFallback := false
	if primaryErr != nil || len(primaryRows) == 0 {
		usedFallback = true
		fallbackSubs, fallbackErr = ctQueryCertSpotter(ctx, domain)
	}
	if primaryErr != nil && fallbackErr != nil {
		return nil, fmt.Errorf("both CT sources failed — crt.sh: %v ; certspotter: %v", primaryErr, fallbackErr)
	}

	seen := map[string]struct{}{}
	for _, r := range primaryRows {
		for _, line := range strings.Split(r.NameValue, "\n") {
			s := strings.ToLower(strings.TrimSpace(line))
			s = strings.TrimPrefix(s, "*.")
			if s == "" || (!strings.HasSuffix(s, "."+domain) && s != domain) {
				continue
			}
			seen[s] = struct{}{}
		}
	}
	for _, s := range fallbackSubs {
		seen[s] = struct{}{}
	}
	subs := make([]string, 0, len(seen))
	for s := range seen {
		subs = append(subs, s)
	}
	sort.Strings(subs)

	out := &CTOutput{
		Domain:     domain,
		Subdomains: subs,
		Count:      len(subs),
		TookMs:     time.Since(start).Milliseconds(),
	}
	switch {
	case primaryErr == nil && !usedFallback:
		out.Source = "crt.sh"
	case primaryErr == nil && usedFallback:
		out.Source = "crt.sh+certspotter"
	case primaryErr != nil && fallbackErr == nil:
		out.Source = "certspotter (crt.sh failed)"
		out.Note = fmt.Sprintf("crt.sh upstream error (%v) — fell back to certspotter", primaryErr)
	}

	if includeRaw {
		rows := primaryRows
		if len(rows) > maxRaw {
			rows = rows[:maxRaw]
		}
		out.Entries = make([]CTEntry, 0, len(rows))
		for _, r := range rows {
			out.Entries = append(out.Entries, CTEntry{
				IssuerName: r.IssuerName, NameValue: r.NameValue,
				NotBefore: r.NotBefore, NotAfter: r.NotAfter, EntryID: r.EntryID,
			})
		}
	}
	return out, nil
}

type crtshRow struct {
	IssuerName string `json:"issuer_name"`
	NameValue  string `json:"name_value"`
	NotBefore  string `json:"not_before"`
	NotAfter   string `json:"not_after"`
	EntryID    int64  `json:"id"`
}

func ctQueryCrtSh(ctx context.Context, domain string) ([]crtshRow, error) {
	q := url.QueryEscape("%." + domain)
	endpoint := fmt.Sprintf("https://crt.sh/?q=%s&output=json", q)
	body, err := httpGetJSON(ctx, endpoint, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var rows []crtshRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("parse (%d bytes): %w", len(body), err)
	}
	return rows, nil
}

// ctQueryCertSpotter — managed CT log monitor with a stable JSON API.
// Free, no key, returns DNS names for all certs issued for the domain
// (and subdomains when include_subdomains=true).
func ctQueryCertSpotter(ctx context.Context, domain string) ([]string, error) {
	endpoint := fmt.Sprintf(
		"https://api.certspotter.com/v1/issuances?domain=%s&include_subdomains=true&expand=dns_names",
		url.QueryEscape(domain),
	)
	body, err := httpGetJSON(ctx, endpoint, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var entries []struct {
		DNSNames []string `json:"dns_names"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("certspotter parse: %w", err)
	}
	seen := map[string]struct{}{}
	for _, e := range entries {
		for _, name := range e.DNSNames {
			n := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(name, "*.")))
			if n == "" {
				continue
			}
			if n == domain || strings.HasSuffix(n, "."+domain) {
				seen[n] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	return out, nil
}
