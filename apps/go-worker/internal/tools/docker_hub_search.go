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

type DockerHubRepo struct {
	Name           string `json:"name"`
	Owner          string `json:"owner"`
	Description    string `json:"description,omitempty"`
	StarCount      int    `json:"star_count"`
	PullCount      int64  `json:"pull_count"`
	IsOfficial     bool   `json:"is_official"`
	IsAutomated    bool   `json:"is_automated"`
	URL            string `json:"url"`
	LeakSeverity   string `json:"leak_severity"`           // critical | high | medium | low
	LeakReason     string `json:"leak_reason,omitempty"`
}

type DockerHubSearchOutput struct {
	Query          string          `json:"query"`
	TotalCount     int             `json:"total_count"`
	Returned       int             `json:"returned"`
	Repos          []DockerHubRepo `json:"repos"`
	UniqueOwners   []string        `json:"unique_owners"`
	HighSeverity   int             `json:"high_severity_count"`
	MediumSeverity int             `json:"medium_severity_count"`
	OfficialCount  int             `json:"official_count"`
	Source         string          `json:"source"`
	TookMs         int64           `json:"tookMs"`
	Note           string          `json:"note,omitempty"`
}

// classifyDockerLeakSeverity rates a Docker Hub repo by likely-leak severity
// based on repo name signals. Critical = name contains words like internal/
// dev/staging/secret/ci/admin/private. Official = lowest risk.
func classifyDockerLeakSeverity(name, owner string, isOfficial bool) (severity, reason string) {
	if isOfficial {
		return "low", "official Docker Hub image — by design publicly released"
	}
	low := strings.ToLower(name)
	criticalTokens := []string{"-internal", "_internal", "/internal-", ".internal", "-secret", "-private", "-prod", "-production"}
	highTokens := []string{"-dev", "-staging", "-stg", "-qa", "-test", "-ci", "-jenkins", "-build", "-admin", "-debug", "-poc", "-experimental"}
	mediumTokens := []string{"-config", "-tools", "-scripts", "-utils", "-base", "-runner", "-deploy"}

	for _, t := range criticalTokens {
		if strings.Contains(low, t) {
			return "critical", "image name suggests private/internal artifact: " + t
		}
	}
	for _, t := range highTokens {
		if strings.Contains(low, t) {
			return "high", "image name suggests internal-tier artifact: " + t
		}
	}
	for _, t := range mediumTokens {
		if strings.Contains(low, t) {
			return "medium", "image name suggests deploy/tooling artifact: " + t
		}
	}
	return "low", ""
}

// DockerHubSearch queries Docker Hub's public v2 search API. Companies push
// internal microservice images, dev/staging variants, and CI tooling images
// to Docker Hub publicly all the time — often with credentials embedded in
// ENV layers, internal CA certs as files, or reference to private container
// registries that reveal infrastructure layout.
//
// Strategy:
//   - Query Docker Hub API (free, no key)
//   - Auto-classify each result by leak severity based on name patterns
//     (internal/, dev/, staging/, secret/, prod/ etc → high severity)
//   - Sort critical-severity results first
//   - Pair with `git_secrets` style follow-up (`docker pull <repo>` and
//     scan layers — outside this tool's scope, but the URL is returned)
func DockerHubSearch(ctx context.Context, input map[string]any) (*DockerHubSearchOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (e.g. brand name, internal hostname stem, or specific image name)")
	}
	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	start := time.Now()
	endpoint := fmt.Sprintf("https://hub.docker.com/v2/search/repositories/?query=%s&page_size=%d",
		url.QueryEscape(q), limit)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/docker-hub-search")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dockerhub fetch failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("dockerhub status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		Count   int `json:"count"`
		Results []struct {
			RepoName        string `json:"repo_name"`
			ShortDesc       string `json:"short_description"`
			StarCount       int    `json:"star_count"`
			PullCount       int64  `json:"pull_count"`
			RepoOwner       string `json:"repo_owner"`
			IsAutomated     bool   `json:"is_automated"`
			IsOfficial      bool   `json:"is_official"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("dockerhub parse: %w", err)
	}

	out := &DockerHubSearchOutput{
		Query:      q,
		TotalCount: parsed.Count,
		Returned:   len(parsed.Results),
		Source:     "hub.docker.com",
	}
	ownerSet := map[string]bool{}

	for _, r := range parsed.Results {
		// repo_name is "owner/name" for non-official; "name" for official
		owner := r.RepoOwner
		name := r.RepoName
		if owner == "" && strings.Contains(name, "/") {
			parts := strings.SplitN(name, "/", 2)
			owner = parts[0]
		}
		// Build URL
		var imgURL string
		if strings.Contains(name, "/") {
			imgURL = "https://hub.docker.com/r/" + name
		} else {
			imgURL = "https://hub.docker.com/_/" + name
		}
		severity, reason := classifyDockerLeakSeverity(name, owner, r.IsOfficial)

		repo := DockerHubRepo{
			Name:         name,
			Owner:        owner,
			Description:  r.ShortDesc,
			StarCount:    r.StarCount,
			PullCount:    r.PullCount,
			IsOfficial:   r.IsOfficial,
			IsAutomated:  r.IsAutomated,
			URL:          imgURL,
			LeakSeverity: severity,
			LeakReason:   reason,
		}
		out.Repos = append(out.Repos, repo)
		if owner != "" {
			ownerSet[owner] = true
		}
		if r.IsOfficial {
			out.OfficialCount++
		}
		switch severity {
		case "critical", "high":
			out.HighSeverity++
		case "medium":
			out.MediumSeverity++
		}
	}

	// Sort critical/high severity first, then by pull count
	severityRank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(out.Repos, func(i, j int) bool {
		ra, rb := severityRank[out.Repos[i].LeakSeverity], severityRank[out.Repos[j].LeakSeverity]
		if ra != rb {
			return ra < rb
		}
		return out.Repos[i].PullCount > out.Repos[j].PullCount
	})

	for o := range ownerSet {
		out.UniqueOwners = append(out.UniqueOwners, o)
	}
	sort.Strings(out.UniqueOwners)
	out.TookMs = time.Since(start).Milliseconds()

	if out.Returned == 0 {
		out.Note = "No Docker Hub repos matched. Query may be too specific. Try the org's brand name as a substring."
	} else if out.HighSeverity > 0 {
		out.Note = fmt.Sprintf("⚠️  %d high/critical-severity repos found — pull and scan layers (`docker pull <name>`, then dive .env / config files / RUN history)", out.HighSeverity)
	}
	return out, nil
}
