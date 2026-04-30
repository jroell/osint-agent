package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type PostmanWorkspaceHit struct {
	Name              string  `json:"name"`
	Slug              string  `json:"slug"`
	ID                string  `json:"id"`
	URL               string  `json:"url"`
	PublisherName     string  `json:"publisher_name"`
	PublisherHandle   string  `json:"publisher_handle"`
	PublisherVerified bool    `json:"publisher_verified"`
	PublisherType     string  `json:"publisher_type"` // team | user
	CollectionCount   int     `json:"collection_count"`
	APICount          int     `json:"api_count"`
	WatcherCount      int     `json:"watcher_count"`
	ForkCount         int     `json:"fork_count"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
	LastActivity      string  `json:"last_activity"`
	Description       string  `json:"description,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	Score             float64 `json:"score"`
	ExactMatchInName  bool    `json:"exact_match_in_name"`
	ExactMatchInDesc  bool    `json:"exact_match_in_description"`
	LeakSeverity      string  `json:"leak_severity"`     // critical | high | medium | low
	LeakReason        string  `json:"leak_reason,omitempty"`
}

type PostmanPublicSearchOutput struct {
	Query             string                `json:"query"`
	TotalReturned     int                   `json:"total_returned"`
	ExactMatches      int                   `json:"exact_matches"`
	HighValueHits     int                   `json:"high_value_hits"`
	UnverifiedPublishers int                `json:"unverified_publisher_count"`
	Hits              []PostmanWorkspaceHit `json:"hits"`
	UniquePublishers  []string              `json:"unique_publishers"`
	Source            string                `json:"source"`
	TookMs            int64                 `json:"tookMs"`
	Note              string                `json:"note,omitempty"`
}

// PostmanPublicSearch queries Postman's public-search proxy for workspaces
// matching a query string. Companies routinely leak internal API collections
// to public Postman workspaces — these workspaces frequently contain:
//   - Hardcoded Bearer tokens in Authorization headers
//   - Internal API hostnames (api-staging.target.com, admin-api.target.com)
//   - Partner API keys committed into example requests
//   - Sensitive endpoint paths with example request/response bodies
//
// This is the OSINT goldmine no one talks about: ~10M public workspaces
// indexed only by Postman's internal search; invisible to Google/Tavily/etc.
//
// Strategy:
//  1. POST to www.postman.com/_api/ws/proxy with the public-workspace index.
//  2. Parse workspace[] response.
//  3. Post-filter for query-string presence in name/description (avoid
//     fuzzy false positives like "vurvey" matching "SurveyMonkey").
//  4. Score leak severity by:
//     - Exact match in workspace name → high (someone literally named the
//       workspace after target)
//     - "internal", "admin", "staging", "private" tokens in name/desc → critical
//     - Verified publisher → low (legit company workspace, not a leak)
//     - Recent updates → high (active leakage)
func PostmanPublicSearch(ctx context.Context, input map[string]any) (*PostmanPublicSearchOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (e.g. 'api.target.com', a brand name, or an internal hostname)")
	}
	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}
	exactMatchOnly := false
	if v, ok := input["exact_match_only"].(bool); ok {
		exactMatchOnly = v
	}

	start := time.Now()

	// Build proxy request body.
	// IMPORTANT: do NOT set mergeEntities=true; it flips the response shape
	// from data.workspace[] to a flat data[] array of mixed entity types.
	innerBody := map[string]any{
		"queryIndices":  []string{"collaboration.workspace"},
		"queryText":     q,
		"size":          limit,
		"from":          0,
		"requestOrigin": "srp",
		"domain":        "public",
	}
	outerBody, _ := json.Marshal(map[string]any{
		"service": "search",
		"method":  "POST",
		"path":    "/search-all",
		"body":    innerBody,
	})

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodPost, "https://www.postman.com/_api/ws/proxy", bytes.NewReader(outerBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; osint-agent/postman-search)")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("postman proxy fetch failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("postman status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		Data struct {
			Workspace []struct {
				Score           float64 `json:"score"`
				NormalizedScore float64 `json:"normalizedScore"`
				Document        struct {
					ID                 string   `json:"id"`
					Slug               string   `json:"slug"`
					Name               string   `json:"name"`
					NameRaw            string   `json:"nameRaw"`
					Description        string   `json:"description"`
					EntityType         string   `json:"entityType"`
					VisibilityStatus   string   `json:"visibilityStatus"`
					CreatedAt          string   `json:"createdAt"`
					UpdatedAt          string   `json:"updatedAt"`
					LastActivityTime   string   `json:"lastActivityTime"`
					PublisherName      string   `json:"publisherName"`
					PublisherHandle    string   `json:"publisherHandle"`
					PublisherType      string   `json:"publisherType"`
					IsPublisherVerified bool    `json:"isPublisherVerified"`
					CollectionCount    int      `json:"collectionCount"`
					APICount           int      `json:"apiCount"`
					WatcherCount       int      `json:"watcherCount"`
					ForkCount          int      `json:"forkCount"`
					Tags               []string `json:"tags"`
				} `json:"document"`
			} `json:"workspace"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("postman response parse failed: %w", err)
	}

	out := &PostmanPublicSearchOutput{
		Query:  q,
		Source: "postman.com/_api/ws/proxy",
	}
	publisherSet := map[string]bool{}
	qLow := strings.ToLower(q)

	suspiciousTokens := []string{"internal", "admin", "staging", "dev", "private", "test", "draft", "sandbox", "old"}

	for _, ws := range parsed.Data.Workspace {
		d := ws.Document
		nameLow := strings.ToLower(d.Name)
		descLow := strings.ToLower(d.Description)

		exactInName := strings.Contains(nameLow, qLow)
		exactInDesc := strings.Contains(descLow, qLow)

		if exactMatchOnly && !exactInName && !exactInDesc {
			continue
		}

		// Score leak severity. Verified publishers (Postman-curated, official
		// company-team workspaces) are capped at lower severities — they're
		// the OPPOSITE of leaks. Real leaks come from random unverified user
		// workspaces with internal/admin/staging tokens.
		severity := "low"
		reasons := []string{}
		if exactInName {
			severity = "high"
			reasons = append(reasons, "exact match in workspace name")
		} else if exactInDesc {
			severity = "medium"
			reasons = append(reasons, "exact match in description only")
		}

		for _, tok := range suspiciousTokens {
			if strings.Contains(nameLow, tok) || strings.Contains(descLow, tok) {
				if d.IsPublisherVerified {
					reasons = append(reasons, "suspicious token in legit verified workspace: "+tok+" (likely false positive)")
				} else {
					severity = "critical"
					reasons = append(reasons, "suspicious token: "+tok)
				}
				break
			}
		}

		if d.IsPublisherVerified {
			// Cap verified publishers at "medium" — they're official company
			// workspaces, not accidental leaks.
			if severity == "critical" {
				severity = "medium"
			}
			if severity == "high" {
				severity = "low"
			}
			reasons = append(reasons, "verified publisher → capped (likely legit company workspace)")
		}

		desc := d.Description
		if len(desc) > 600 {
			desc = desc[:600] + "…"
		}

		hit := PostmanWorkspaceHit{
			Name:              d.Name,
			Slug:              d.Slug,
			ID:                d.ID,
			URL:               fmt.Sprintf("https://www.postman.com/%s/workspace/%s/", d.PublisherHandle, d.Slug),
			PublisherName:     d.PublisherName,
			PublisherHandle:   d.PublisherHandle,
			PublisherVerified: d.IsPublisherVerified,
			PublisherType:     d.PublisherType,
			CollectionCount:   d.CollectionCount,
			APICount:          d.APICount,
			WatcherCount:      d.WatcherCount,
			ForkCount:         d.ForkCount,
			CreatedAt:         d.CreatedAt,
			UpdatedAt:         d.UpdatedAt,
			LastActivity:      d.LastActivityTime,
			Description:       desc,
			Tags:              d.Tags,
			Score:             ws.Score,
			ExactMatchInName:  exactInName,
			ExactMatchInDesc:  exactInDesc,
			LeakSeverity:      severity,
			LeakReason:        strings.Join(reasons, "; "),
		}
		out.Hits = append(out.Hits, hit)
		publisherSet[d.PublisherHandle] = true
		if exactInName || exactInDesc {
			out.ExactMatches++
		}
		if severity == "critical" || severity == "high" {
			out.HighValueHits++
		}
		if !d.IsPublisherVerified {
			out.UnverifiedPublishers++
		}
	}

	// Sort by leak severity (critical/high first), then by score
	severityRank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(out.Hits, func(i, j int) bool {
		ra, rb := severityRank[out.Hits[i].LeakSeverity], severityRank[out.Hits[j].LeakSeverity]
		if ra != rb {
			return ra < rb
		}
		return out.Hits[i].Score > out.Hits[j].Score
	})

	for p := range publisherSet {
		out.UniquePublishers = append(out.UniquePublishers, p)
	}
	sort.Strings(out.UniquePublishers)
	out.TotalReturned = len(out.Hits)
	out.TookMs = time.Since(start).Milliseconds()

	if out.TotalReturned == 0 {
		out.Note = "No workspaces matched. Try a less specific query (brand name vs full URL), or set exact_match_only=false to widen."
	} else if out.HighValueHits > 0 {
		out.Note = fmt.Sprintf("⚠️  %d high/critical-severity workspace(s) found — fetch the workspace URL to inspect for leaked credentials in collection requests.", out.HighValueHits)
	}
	return out, nil
}
