package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// GitHubAdvancedSearch wraps three GitHub Search API surfaces that aren't
// currently covered by github_code_search:
//
//   - "commits"  : keyword across all public commit MESSAGES (different from
//                  code content). Returns SHA + commit message + author
//                  name+email+date + committer + repo + commit URL. Critical
//                  for ER: use `author-email:foo@bar.com` to enumerate every
//                  repo a person has ever committed to (across the entirety
//                  of public GitHub), or `author-name:"Jane Doe"` for fuzzy
//                  name matching, or free-text in commit messages.
//   - "issues"   : keyword across all public issues + pull requests. Returns
//                  title + author + state + creation date + body excerpt +
//                  comments count + labels + repo + URL. Use cases: find
//                  vulnerability disclosures, find people complaining about
//                  a product, find a target's public-affairs activity.
//                  GitHub treats issues+PRs uniformly on this endpoint.
//   - "users"    : keyword across all public user profiles. Returns login +
//                  type (User/Org) + URL + score. Use `in:login`, `in:name`,
//                  `in:email`, `location:`, `language:`, `followers:>N`,
//                  `repos:>N` qualifiers to filter.
//
// All three require a GITHUB_TOKEN env var (any classic or fine-grained
// token with public_repo scope works). Without a token the rate limit is
// 10 req/min and result quality degrades; with one it's 30 req/min.
//
// Pairs with: github_code_search (code content), github_commit_emails
// (per-user email scrape), github_user_profile (full profile fetch),
// github_org_intel (org metadata).

type GHCommitItem struct {
	SHA            string `json:"sha"`
	Message        string `json:"message"`
	AuthorName     string `json:"author_name"`
	AuthorEmail    string `json:"author_email"`
	AuthorDate     string `json:"author_date"`
	CommitterName  string `json:"committer_name,omitempty"`
	CommitterEmail string `json:"committer_email,omitempty"`
	CommitterDate  string `json:"committer_date,omitempty"`
	Repository     string `json:"repository"`
	URL            string `json:"url"`
}

type GHIssueItem struct {
	Title      string   `json:"title"`
	State      string   `json:"state"`
	Author     string   `json:"author"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
	Repository string   `json:"repository"`
	IsPR       bool     `json:"is_pull_request"`
	Comments   int      `json:"comments,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Body       string   `json:"body_excerpt,omitempty"`
	URL        string   `json:"url"`
}

type GHUserItem struct {
	Login   string  `json:"login"`
	Type    string  `json:"type"`
	URL     string  `json:"url"`
	Score   float64 `json:"score,omitempty"`
}

type GitHubAdvancedSearchOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query"`
	TotalCount        int            `json:"total_count"`
	Returned          int            `json:"returned"`

	Commits           []GHCommitItem `json:"commits,omitempty"`
	Issues            []GHIssueItem  `json:"issues,omitempty"`
	Users             []GHUserItem   `json:"users,omitempty"`

	UniqueAuthorEmails []string      `json:"unique_author_emails,omitempty"`
	UniqueRepos        []string      `json:"unique_repos,omitempty"`

	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
	Note              string         `json:"note,omitempty"`
	RateLimit         string         `json:"rate_limit,omitempty"`
}

// GitHubAdvancedSearch is the main entry point.
func GitHubAdvancedSearch(ctx context.Context, input map[string]any) (*GitHubAdvancedSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "commits"
	}
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, fmt.Errorf("input.query required")
	}

	limit := 20
	if l, ok := input["limit"].(float64); ok && l > 0 && l <= 100 {
		limit = int(l)
	}
	sortField, _ := input["sort"].(string)
	order, _ := input["order"].(string) // "asc" or "desc"
	if order == "" {
		order = "desc"
	}

	token := os.Getenv("GITHUB_TOKEN")
	out := &GitHubAdvancedSearchOutput{
		Mode:   mode,
		Query:  q,
		Source: "api.github.com/search/" + mode,
	}
	if token == "" {
		out.Note = "No GITHUB_TOKEN — rate-limited to 10 req/min and search quality may degrade. Set token for full access."
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "commits":
		err := ghSearchCommits(ctx, cli, token, q, sortField, order, limit, out)
		if err != nil {
			return nil, err
		}
	case "issues":
		err := ghSearchIssues(ctx, cli, token, q, sortField, order, limit, out)
		if err != nil {
			return nil, err
		}
	case "users":
		err := ghSearchUsers(ctx, cli, token, q, sortField, order, limit, out)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: commits, issues, users", mode)
	}

	out.HighlightFindings = buildGHASHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func ghBuildSearchURL(endpoint, q, sortField, order string, perPage int) string {
	params := url.Values{}
	params.Set("q", q)
	params.Set("per_page", fmt.Sprintf("%d", perPage))
	if sortField != "" {
		params.Set("sort", sortField)
		params.Set("order", order)
	}
	return "https://api.github.com/search/" + endpoint + "?" + params.Encode()
}

func ghDoRequest(ctx context.Context, cli *http.Client, urlStr, token, accept string) ([]byte, http.Header, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "osint-agent/1.0")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 422 {
		// 422 means GitHub couldn't parse the query — surface the message
		var errResp struct{ Message string `json:"message"` }
		_ = json.Unmarshal(body, &errResp)
		return nil, resp.Header, fmt.Errorf("GitHub API 422: %s — check query syntax (use qualifiers like author-email:, author:, repo:, language:)", errResp.Message)
	}
	if resp.StatusCode != 200 {
		return nil, resp.Header, fmt.Errorf("GitHub API HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, resp.Header, nil
}

func ghRateLimitInfo(h http.Header) string {
	rem := h.Get("X-RateLimit-Remaining")
	limit := h.Get("X-RateLimit-Limit")
	reset := h.Get("X-RateLimit-Reset")
	if rem == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s remaining (resets at unix:%s)", rem, limit, reset)
}

func ghSearchCommits(ctx context.Context, cli *http.Client, token, q, sortField, order string, limit int, out *GitHubAdvancedSearchOutput) error {
	urlStr := ghBuildSearchURL("commits", q, sortField, order, limit)
	body, hdr, err := ghDoRequest(ctx, cli, urlStr, token, "application/vnd.github.cloak-preview+json")
	if err != nil {
		return fmt.Errorf("commits search: %w", err)
	}
	out.RateLimit = ghRateLimitInfo(hdr)

	var raw struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name  string `json:"name"`
					Email string `json:"email"`
					Date  string `json:"date"`
				} `json:"author"`
				Committer struct {
					Name  string `json:"name"`
					Email string `json:"email"`
					Date  string `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			HTMLURL string `json:"html_url"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	out.TotalCount = raw.TotalCount
	emails := map[string]struct{}{}
	repos := map[string]struct{}{}
	for _, it := range raw.Items {
		c := GHCommitItem{
			SHA:            it.SHA,
			Message:        hfTruncate(it.Commit.Message, 200),
			AuthorName:     it.Commit.Author.Name,
			AuthorEmail:    it.Commit.Author.Email,
			AuthorDate:     it.Commit.Author.Date,
			CommitterName:  it.Commit.Committer.Name,
			CommitterEmail: it.Commit.Committer.Email,
			CommitterDate:  it.Commit.Committer.Date,
			Repository:     it.Repository.FullName,
			URL:            it.HTMLURL,
		}
		out.Commits = append(out.Commits, c)
		if c.AuthorEmail != "" {
			emails[c.AuthorEmail] = struct{}{}
		}
		if c.CommitterEmail != "" && c.CommitterEmail != c.AuthorEmail {
			emails[c.CommitterEmail] = struct{}{}
		}
		if c.Repository != "" {
			repos[c.Repository] = struct{}{}
		}
	}
	for e := range emails {
		out.UniqueAuthorEmails = append(out.UniqueAuthorEmails, e)
	}
	sort.Strings(out.UniqueAuthorEmails)
	for r := range repos {
		out.UniqueRepos = append(out.UniqueRepos, r)
	}
	sort.Strings(out.UniqueRepos)
	out.Returned = len(out.Commits)
	return nil
}

func ghSearchIssues(ctx context.Context, cli *http.Client, token, q, sortField, order string, limit int, out *GitHubAdvancedSearchOutput) error {
	urlStr := ghBuildSearchURL("issues", q, sortField, order, limit)
	body, hdr, err := ghDoRequest(ctx, cli, urlStr, token, "application/vnd.github+json")
	if err != nil {
		return fmt.Errorf("issues search: %w", err)
	}
	out.RateLimit = ghRateLimitInfo(hdr)
	var raw struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			Title         string `json:"title"`
			State         string `json:"state"`
			User          struct{ Login string `json:"login"` } `json:"user"`
			CreatedAt     string `json:"created_at"`
			UpdatedAt     string `json:"updated_at"`
			RepositoryURL string `json:"repository_url"`
			HTMLURL       string `json:"html_url"`
			Body          string `json:"body"`
			Comments      int    `json:"comments"`
			Labels        []struct{ Name string `json:"name"` } `json:"labels"`
			PullRequest   *struct{}                              `json:"pull_request"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	out.TotalCount = raw.TotalCount
	repos := map[string]struct{}{}
	for _, it := range raw.Items {
		// Extract owner/repo from repository_url (format: https://api.github.com/repos/owner/repo)
		repoStr := ""
		parts := strings.Split(it.RepositoryURL, "/")
		if len(parts) >= 2 {
			repoStr = parts[len(parts)-2] + "/" + parts[len(parts)-1]
		}
		labels := []string{}
		for _, l := range it.Labels {
			labels = append(labels, l.Name)
		}
		out.Issues = append(out.Issues, GHIssueItem{
			Title:      it.Title,
			State:      it.State,
			Author:     it.User.Login,
			CreatedAt:  it.CreatedAt,
			UpdatedAt:  it.UpdatedAt,
			Repository: repoStr,
			IsPR:       it.PullRequest != nil,
			Comments:   it.Comments,
			Labels:     labels,
			Body:       hfTruncate(it.Body, 240),
			URL:        it.HTMLURL,
		})
		if repoStr != "" {
			repos[repoStr] = struct{}{}
		}
	}
	for r := range repos {
		out.UniqueRepos = append(out.UniqueRepos, r)
	}
	sort.Strings(out.UniqueRepos)
	out.Returned = len(out.Issues)
	return nil
}

func ghSearchUsers(ctx context.Context, cli *http.Client, token, q, sortField, order string, limit int, out *GitHubAdvancedSearchOutput) error {
	urlStr := ghBuildSearchURL("users", q, sortField, order, limit)
	body, hdr, err := ghDoRequest(ctx, cli, urlStr, token, "application/vnd.github+json")
	if err != nil {
		return fmt.Errorf("users search: %w", err)
	}
	out.RateLimit = ghRateLimitInfo(hdr)
	var raw struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			Login   string  `json:"login"`
			Type    string  `json:"type"`
			HTMLURL string  `json:"html_url"`
			Score   float64 `json:"score"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	out.TotalCount = raw.TotalCount
	for _, it := range raw.Items {
		out.Users = append(out.Users, GHUserItem{
			Login: it.Login,
			Type:  it.Type,
			URL:   it.HTMLURL,
			Score: it.Score,
		})
	}
	out.Returned = len(out.Users)
	return nil
}

func buildGHASHighlights(o *GitHubAdvancedSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "commits":
		hi = append(hi, fmt.Sprintf("✓ %d total commit matches (returned %d) for '%s'", o.TotalCount, o.Returned, o.Query))
		if len(o.UniqueAuthorEmails) > 0 {
			hi = append(hi, fmt.Sprintf("  📧 unique author/committer emails (%d): %s", len(o.UniqueAuthorEmails), strings.Join(o.UniqueAuthorEmails, ", ")))
		}
		if len(o.UniqueRepos) > 0 {
			truncated := o.UniqueRepos
			suffix := ""
			if len(truncated) > 10 {
				truncated = truncated[:10]
				suffix = fmt.Sprintf(" … +%d more", len(o.UniqueRepos)-10)
			}
			hi = append(hi, fmt.Sprintf("  📦 unique repos (%d): %s%s", len(o.UniqueRepos), strings.Join(truncated, ", "), suffix))
		}
		for i, c := range o.Commits {
			if i >= 5 {
				break
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s @ %s — %s", c.AuthorDate[:10], c.AuthorEmail, c.Repository, hfTruncate(c.Message, 80)))
		}
	case "issues":
		hi = append(hi, fmt.Sprintf("✓ %d total issue/PR matches (returned %d) for '%s'", o.TotalCount, o.Returned, o.Query))
		prCount := 0
		openCount := 0
		for _, it := range o.Issues {
			if it.IsPR {
				prCount++
			}
			if it.State == "open" {
				openCount++
			}
		}
		hi = append(hi, fmt.Sprintf("  breakdown: %d PRs, %d issues; %d open, %d closed",
			prCount, o.Returned-prCount, openCount, o.Returned-openCount))
		for i, it := range o.Issues {
			if i >= 5 {
				break
			}
			marker := "issue"
			if it.IsPR {
				marker = "PR"
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s by %s @ %s — %s", marker, it.State, it.Author, it.Repository, hfTruncate(it.Title, 80)))
		}
	case "users":
		hi = append(hi, fmt.Sprintf("✓ %d total user matches (returned %d) for '%s'", o.TotalCount, o.Returned, o.Query))
		for i, u := range o.Users {
			if i >= 10 {
				break
			}
			hi = append(hi, fmt.Sprintf("  • %s [%s] (score %.1f) — %s", u.Login, u.Type, u.Score, u.URL))
		}
	}
	if o.RateLimit != "" {
		hi = append(hi, "  rate limit: "+o.RateLimit)
	}
	return hi
}
