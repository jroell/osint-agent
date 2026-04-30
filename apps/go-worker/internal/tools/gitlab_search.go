package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type GitLabSearchResult struct {
	ProjectID    int    `json:"project_id,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`
	Path         string `json:"path,omitempty"`
	URL          string `json:"url,omitempty"`
	StartLine    int    `json:"start_line,omitempty"`
	Excerpt      string `json:"excerpt,omitempty"`
	Severity     string `json:"severity"`
	SeverityReason string `json:"severity_reason,omitempty"`
}

type GitLabSearchOutput struct {
	Query         string               `json:"query"`
	Scope         string               `json:"scope"`
	TotalResults  int                  `json:"total_results"`
	Results       []GitLabSearchResult `json:"results"`
	UniqueProjects []string            `json:"unique_projects"`
	HighSeverity  int                  `json:"high_severity_count"`
	Source        string               `json:"source"`
	TookMs        int64                `json:"tookMs"`
	Note          string               `json:"note,omitempty"`
}

// GitLabSearch queries gitlab.com's public search API for code/projects/users
// matching a query. Free, no key required for public-only search; setting
// GITLAB_TOKEN unlocks the full code-content search (requires a basic
// gitlab.com account; tokens are free).
//
// Use cases:
//   - Find leaked URLs / config / secrets in public GitLab repos
//   - Map an org's GitLab presence (gitlab.com profile, public projects)
//   - Complement github_code_search for orgs that prefer GitLab
//
// Severity classification mirrors github_code_search.
func GitLabSearch(ctx context.Context, input map[string]any) (*GitLabSearchOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	scope, _ := input["scope"].(string)
	if scope == "" {
		scope = "blobs" // code search
	}
	limit := 20
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	apiKey := os.Getenv("GITLAB_TOKEN")
	if apiKey == "" {
		// gitlab.com search/blobs requires auth — fall back to project search
		// which is public-accessible.
		if scope == "blobs" {
			scope = "projects"
		}
	}

	start := time.Now()
	endpoint := fmt.Sprintf("https://gitlab.com/api/v4/search?scope=%s&search=%s&per_page=%d",
		scope, url.QueryEscape(q), limit)

	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/gitlab-search")
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("PRIVATE-TOKEN", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab fetch failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 401 {
		return &GitLabSearchOutput{
			Query: q, Scope: scope, Source: "gitlab.com",
			Note: "GitLab returned 401 Unauthorized. Set GITLAB_TOKEN env var (free signup at gitlab.com → Settings → Access Tokens with read_api scope). GitLab tightened public search access in 2024.",
		}, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("gitlab status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	out := &GitLabSearchOutput{
		Query: q, Scope: scope, Source: "gitlab.com",
	}
	projectSet := map[string]bool{}

	switch scope {
	case "projects":
		var projects []struct {
			ID            int    `json:"id"`
			Name          string `json:"name"`
			NameWithNS    string `json:"name_with_namespace"`
			Path          string `json:"path"`
			PathWithNS    string `json:"path_with_namespace"`
			Description   string `json:"description"`
			WebURL        string `json:"web_url"`
			DefaultBranch string `json:"default_branch"`
			LastActivity  string `json:"last_activity_at"`
			StarCount     int    `json:"star_count"`
			ForksCount    int    `json:"forks_count"`
		}
		if err := json.Unmarshal(body, &projects); err != nil {
			return nil, fmt.Errorf("gitlab projects parse: %w", err)
		}
		for _, p := range projects {
			sev, reason := classifyCodeMatch(p.PathWithNS)
			out.Results = append(out.Results, GitLabSearchResult{
				ProjectID:      p.ID,
				ProjectName:    p.NameWithNS,
				Path:           p.PathWithNS,
				URL:            p.WebURL,
				Excerpt:        truncate(p.Description, 200),
				Severity:       sev,
				SeverityReason: reason,
			})
			projectSet[p.PathWithNS] = true
			if sev == "critical" || sev == "high" {
				out.HighSeverity++
			}
		}
	case "blobs":
		var blobs []struct {
			Basename  string `json:"basename"`
			Data      string `json:"data"`
			Path      string `json:"path"`
			Filename  string `json:"filename"`
			ProjectID int    `json:"project_id"`
			Ref       string `json:"ref"`
			Startline int    `json:"startline"`
		}
		if err := json.Unmarshal(body, &blobs); err != nil {
			return nil, fmt.Errorf("gitlab blobs parse: %w", err)
		}
		for _, b := range blobs {
			path := b.Path
			if path == "" {
				path = b.Filename
			}
			sev, reason := classifyCodeMatch(path)
			out.Results = append(out.Results, GitLabSearchResult{
				ProjectID:      b.ProjectID,
				Path:           path,
				StartLine:      b.Startline,
				Excerpt:        truncate(b.Data, 250),
				Severity:       sev,
				SeverityReason: reason,
			})
			if sev == "critical" || sev == "high" {
				out.HighSeverity++
			}
		}
	}

	out.TotalResults = len(out.Results)
	rank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(out.Results, func(i, j int) bool {
		ra, rb := rank[out.Results[i].Severity], rank[out.Results[j].Severity]
		return ra < rb
	})

	for p := range projectSet {
		out.UniqueProjects = append(out.UniqueProjects, p)
	}
	sort.Strings(out.UniqueProjects)

	if apiKey == "" {
		out.Note = "GITLAB_TOKEN not set — using public projects-only scope. Set token (free signup) for code-content search."
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
