package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type GHOrgInfo struct {
	Login           string `json:"login"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Blog            string `json:"blog,omitempty"`
	Email           string `json:"email,omitempty"`
	TwitterUsername string `json:"twitter_username,omitempty"`
	Location        string `json:"location,omitempty"`
	Company         string `json:"company,omitempty"`
	PublicRepos     int    `json:"public_repos"`
	Followers       int    `json:"followers"`
	Following       int    `json:"following"`
	CreatedAt       string `json:"created_at,omitempty"`
	IsVerified      bool   `json:"is_verified,omitempty"`
}

type GHRepoSummary struct {
	Name           string `json:"name"`
	FullName       string `json:"full_name"`
	Description    string `json:"description,omitempty"`
	Language       string `json:"language,omitempty"`
	Stars          int    `json:"stargazers_count"`
	Forks          int    `json:"forks_count"`
	OpenIssues     int    `json:"open_issues_count"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	PushedAt       string `json:"pushed_at,omitempty"`
	HTMLURL        string `json:"html_url"`
	Topics         []string `json:"topics,omitempty"`
	Archived       bool   `json:"archived,omitempty"`
	IsFork         bool   `json:"fork,omitempty"`
	License        string `json:"license,omitempty"`
}

type GHMemberSummary struct {
	Login     string `json:"login"`
	URL       string `json:"html_url"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type GHOrgIntelOutput struct {
	Org              *GHOrgInfo                `json:"org_info"`
	TopReposByStars  []GHRepoSummary           `json:"top_repos_by_stars"`
	RecentlyActive   []GHRepoSummary           `json:"recently_active_repos"`
	LanguageStats    map[string]int            `json:"language_stats"`
	TopLanguages     []string                  `json:"top_languages"`
	PublicMembers    []GHMemberSummary         `json:"public_members,omitempty"`
	MemberCount      int                       `json:"member_count"`
	OldestRepo       string                    `json:"oldest_repo_year,omitempty"`
	NewestRepo       string                    `json:"newest_repo_iso,omitempty"`
	HighlightFindings []string                 `json:"highlight_findings"`
	Source           string                    `json:"source"`
	TookMs           int64                     `json:"tookMs"`
	Errors           map[string]string         `json:"errors,omitempty"`
}

// GitHubOrgIntel fans out parallel calls to multiple GitHub API endpoints
// for an organization:
//   - GET /orgs/{org}              → org metadata (name, blog, location)
//   - GET /orgs/{org}/repos         → top repos by stars + by recent push
//   - GET /orgs/{org}/public_members → visible team members
// Aggregates language stats across repos, identifies oldest/newest repos.
//
// Auth via GITHUB_TOKEN unlocks higher rate limits + private-repo visibility
// for orgs the token has access to (we never expose private data — purely
// public OSINT).
//
// Pairs with `github_code_search` (find leaks) + `github_emails` (extract
// commit author emails). Together = complete GitHub OSINT.
func GitHubOrgIntel(ctx context.Context, input map[string]any) (*GHOrgIntelOutput, error) {
	org, _ := input["org"].(string)
	org = strings.TrimSpace(org)
	if org == "" {
		return nil, errors.New("input.org required (e.g. 'anthropics')")
	}

	includeMembers := true
	if v, ok := input["include_members"].(bool); ok {
		includeMembers = v
	}
	maxRepos := 30
	if v, ok := input["max_repos"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		maxRepos = int(v)
	}

	apiKey := os.Getenv("GITHUB_TOKEN")
	start := time.Now()
	out := &GHOrgIntelOutput{
		Source: "github.com/api",
		LanguageStats: map[string]int{},
		Errors: map[string]string{},
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	// 1. Org info
	wg.Add(1)
	go func() {
		defer wg.Done()
		info, err := ghFetchOrgInfo(ctx, org, apiKey)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			out.Errors["org_info"] = err.Error()
			return
		}
		out.Org = info
	}()

	// 2. Top repos by stars
	wg.Add(1)
	go func() {
		defer wg.Done()
		repos, err := ghFetchOrgRepos(ctx, org, "stars", maxRepos, apiKey)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			out.Errors["repos_by_stars"] = err.Error()
			return
		}
		out.TopReposByStars = repos
	}()

	// 3. Recent activity
	wg.Add(1)
	go func() {
		defer wg.Done()
		repos, err := ghFetchOrgRepos(ctx, org, "pushed", 15, apiKey)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			out.Errors["recent_repos"] = err.Error()
			return
		}
		out.RecentlyActive = repos
	}()

	// 4. Public members
	if includeMembers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			members, err := ghFetchPublicMembers(ctx, org, apiKey)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Errors["members"] = err.Error()
				return
			}
			out.PublicMembers = members
			out.MemberCount = len(members)
		}()
	}

	wg.Wait()

	// Aggregate language stats from top repos
	for _, r := range out.TopReposByStars {
		if r.Language != "" {
			out.LanguageStats[r.Language]++
		}
	}
	type langKV struct {
		Lang  string
		Count int
	}
	var langs []langKV
	for k, v := range out.LanguageStats {
		langs = append(langs, langKV{k, v})
	}
	sort.Slice(langs, func(i, j int) bool { return langs[i].Count > langs[j].Count })
	for _, l := range langs[:minInt(5, len(langs))] {
		out.TopLanguages = append(out.TopLanguages, fmt.Sprintf("%s (%d)", l.Lang, l.Count))
	}

	// Oldest / newest repo
	if len(out.TopReposByStars) > 0 {
		oldest, newest := out.TopReposByStars[0].UpdatedAt, out.TopReposByStars[0].UpdatedAt
		for _, r := range out.TopReposByStars {
			if r.UpdatedAt < oldest {
				oldest = r.UpdatedAt
			}
			if r.UpdatedAt > newest {
				newest = r.UpdatedAt
			}
		}
		if len(oldest) >= 4 {
			out.OldestRepo = oldest[:4]
		}
		out.NewestRepo = newest
	}

	// Highlights
	highlights := []string{}
	if out.Org != nil {
		highlights = append(highlights, fmt.Sprintf("@%s — %d public repos, %d followers, joined %s",
			out.Org.Login, out.Org.PublicRepos, out.Org.Followers,
			out.Org.CreatedAt))
		if out.Org.Blog != "" {
			highlights = append(highlights, fmt.Sprintf("blog: %s", out.Org.Blog))
		}
		if out.Org.TwitterUsername != "" {
			highlights = append(highlights, fmt.Sprintf("twitter: @%s", out.Org.TwitterUsername))
		}
	}
	if len(out.TopReposByStars) > 0 {
		top := out.TopReposByStars[0]
		highlights = append(highlights, fmt.Sprintf("flagship repo: %s (%d⭐) — %s", top.Name, top.Stars, top.Language))
	}
	if len(out.TopLanguages) > 0 {
		highlights = append(highlights, "tech stack: "+strings.Join(out.TopLanguages, ", "))
	}
	if out.MemberCount > 0 {
		highlights = append(highlights, fmt.Sprintf("%d publicly-visible members", out.MemberCount))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func ghFetchOrgInfo(ctx context.Context, org, apiKey string) (*GHOrgInfo, error) {
	endpoint := "https://api.github.com/orgs/" + org
	body, status, err := ghAPIGet(ctx, endpoint, apiKey)
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, fmt.Errorf("org %s not found", org)
	}
	if status != 200 {
		return nil, fmt.Errorf("github status %d: %s", status, truncate(string(body), 200))
	}
	var info GHOrgInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func ghFetchOrgRepos(ctx context.Context, org, sortBy string, perPage int, apiKey string) ([]GHRepoSummary, error) {
	// /repos endpoint accepts: created, updated, pushed, full_name; not "stars" — fetch all and sort.
	apiSort := "updated"
	if sortBy == "pushed" {
		apiSort = "pushed"
	}
	endpoint := fmt.Sprintf("https://api.github.com/orgs/%s/repos?sort=%s&per_page=%d&type=public", org, apiSort, perPage)
	body, status, err := ghAPIGet(ctx, endpoint, apiKey)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("status %d", status)
	}
	var repos []GHRepoSummary
	type repoRaw struct {
		Name     string   `json:"name"`
		FullName string   `json:"full_name"`
		Description string `json:"description"`
		Language string   `json:"language"`
		Stars    int      `json:"stargazers_count"`
		Forks    int      `json:"forks_count"`
		Issues   int      `json:"open_issues_count"`
		UpdatedAt string  `json:"updated_at"`
		PushedAt string   `json:"pushed_at"`
		HTMLURL  string   `json:"html_url"`
		Topics   []string `json:"topics"`
		Archived bool     `json:"archived"`
		Fork     bool     `json:"fork"`
		License  *struct {
			Key string `json:"key"`
		} `json:"license"`
	}
	var raw []repoRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	for _, r := range raw {
		lic := ""
		if r.License != nil {
			lic = r.License.Key
		}
		repos = append(repos, GHRepoSummary{
			Name: r.Name, FullName: r.FullName, Description: truncate(r.Description, 200),
			Language: r.Language, Stars: r.Stars, Forks: r.Forks, OpenIssues: r.Issues,
			UpdatedAt: r.UpdatedAt, PushedAt: r.PushedAt, HTMLURL: r.HTMLURL,
			Topics: r.Topics, Archived: r.Archived, IsFork: r.Fork, License: lic,
		})
	}
	if sortBy == "stars" {
		sort.Slice(repos, func(i, j int) bool { return repos[i].Stars > repos[j].Stars })
	} else if sortBy == "pushed" {
		sort.Slice(repos, func(i, j int) bool { return repos[i].PushedAt > repos[j].PushedAt })
	}
	if len(repos) > perPage {
		repos = repos[:perPage]
	}
	return repos, nil
}

func ghFetchPublicMembers(ctx context.Context, org, apiKey string) ([]GHMemberSummary, error) {
	endpoint := fmt.Sprintf("https://api.github.com/orgs/%s/public_members?per_page=50", org)
	body, status, err := ghAPIGet(ctx, endpoint, apiKey)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("status %d", status)
	}
	var raw []struct {
		Login     string `json:"login"`
		HTMLURL   string `json:"html_url"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := []GHMemberSummary{}
	for _, m := range raw {
		out = append(out, GHMemberSummary{Login: m.Login, URL: m.HTMLURL, AvatarURL: m.AvatarURL})
	}
	return out, nil
}

func ghAPIGet(ctx context.Context, url, apiKey string) ([]byte, int, error) {
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "osint-agent/github-org-intel")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if apiKey != "" {
		req.Header.Set("Authorization", "token "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return body, resp.StatusCode, nil
}
