package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type GitHubProfile struct {
	Login         string    `json:"login"`
	Type          string    `json:"type"`
	Name          string    `json:"name,omitempty"`
	Company       string    `json:"company,omitempty"`
	Email         string    `json:"email,omitempty"`
	Bio           string    `json:"bio,omitempty"`
	Location      string    `json:"location,omitempty"`
	Blog          string    `json:"blog,omitempty"`
	TwitterUser   string    `json:"twitter_username,omitempty"`
	PublicRepos   int       `json:"public_repos"`
	PublicGists   int       `json:"public_gists"`
	Followers     int       `json:"followers"`
	Following     int       `json:"following"`
	CreatedAt     string    `json:"created_at"`
	UpdatedAt     string    `json:"updated_at"`
	HTMLURL       string    `json:"html_url"`
	AvatarURL     string    `json:"avatar_url,omitempty"`
}

type GitHubRepoSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Stars       int    `json:"stars"`
	Language    string `json:"language,omitempty"`
	UpdatedAt   string `json:"updated_at"`
	IsFork      bool   `json:"is_fork"`
	HTMLURL     string `json:"html_url"`
}

type GitHubProfileOutput struct {
	Profile   GitHubProfile        `json:"profile"`
	TopRepos  []GitHubRepoSummary  `json:"top_repos"`
	RecentPush []GitHubRepoSummary `json:"recently_pushed"`
	TookMs    int64                `json:"tookMs"`
	Source    string               `json:"source"`
	Note      string               `json:"note,omitempty"`
}

// GitHubUserProfile fetches a user or organization's public GitHub profile
// and recent repos. GITHUB_TOKEN env var optional but strongly recommended
// (60 req/hr unauth → 5000 req/hr authenticated).
func GitHubUserProfile(ctx context.Context, input map[string]any) (*GitHubProfileOutput, error) {
	login, _ := input["login"].(string)
	login = strings.TrimSpace(login)
	if login == "" {
		return nil, errors.New("input.login required (GitHub user or org login)")
	}
	start := time.Now()
	token := os.Getenv("GITHUB_TOKEN")

	profileBody, err := githubGet(ctx, "https://api.github.com/users/"+login, token)
	if err != nil {
		return nil, fmt.Errorf("profile fetch: %w", err)
	}
	var profile GitHubProfile
	if err := json.Unmarshal(profileBody, &profile); err != nil {
		return nil, fmt.Errorf("profile parse: %w", err)
	}

	// Repos: 100 most-recently-updated.
	repoBody, err := githubGet(ctx, fmt.Sprintf("https://api.github.com/users/%s/repos?per_page=100&sort=updated&direction=desc", login), token)
	if err != nil {
		return nil, fmt.Errorf("repos fetch: %w", err)
	}
	var rawRepos []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Stars       int    `json:"stargazers_count"`
		Language    string `json:"language"`
		UpdatedAt   string `json:"updated_at"`
		PushedAt    string `json:"pushed_at"`
		Fork        bool   `json:"fork"`
		HTMLURL     string `json:"html_url"`
	}
	if err := json.Unmarshal(repoBody, &rawRepos); err != nil {
		return nil, fmt.Errorf("repos parse: %w", err)
	}
	repos := make([]GitHubRepoSummary, 0, len(rawRepos))
	for _, r := range rawRepos {
		repos = append(repos, GitHubRepoSummary{
			Name: r.Name, Description: r.Description, Stars: r.Stars,
			Language: r.Language, UpdatedAt: r.UpdatedAt, IsFork: r.Fork, HTMLURL: r.HTMLURL,
		})
	}
	// Top by stars (top 10).
	byStars := make([]GitHubRepoSummary, len(repos))
	copy(byStars, repos)
	sort.Slice(byStars, func(i, j int) bool { return byStars[i].Stars > byStars[j].Stars })
	if len(byStars) > 10 {
		byStars = byStars[:10]
	}
	// Recently pushed (top 10).
	if len(repos) > 10 {
		repos = repos[:10]
	}

	out := &GitHubProfileOutput{
		Profile:    profile,
		TopRepos:   byStars,
		RecentPush: repos,
		TookMs:     time.Since(start).Milliseconds(),
		Source:     "api.github.com",
	}
	if token == "" {
		out.Note = "unauthenticated (60 req/hr); set GITHUB_TOKEN for 5000 req/hr"
	}
	return out, nil
}

func githubGet(ctx context.Context, url, token string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body := make([]byte, 0, 64<<10)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
			if len(body) > 4<<20 {
				break
			}
		}
		if rerr != nil {
			break
		}
	}
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return nil, fmt.Errorf("github rate limited (%d) — set GITHUB_TOKEN", resp.StatusCode)
	}
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("github 404 (user/org not found?)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}
