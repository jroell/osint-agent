package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type GitHubEmailHit struct {
	Email     string   `json:"email"`
	Names     []string `json:"names_seen"`
	Repos     []string `json:"repos_seen"`
	Count     int      `json:"commit_count"`
	IsNoreply bool     `json:"github_noreply"`
}

type GitHubCommitEmailsOutput struct {
	Login        string           `json:"login"`
	EventsRead   int              `json:"events_read"`
	CommitsRead  int              `json:"commits_read"`
	UniqueEmails int              `json:"unique_emails"`
	Emails       []GitHubEmailHit `json:"emails"`
	Source       string           `json:"source"`
	TookMs       int64            `json:"tookMs"`
	Note         string           `json:"note,omitempty"`
}

// GitHubCommitEmails harvests author/committer emails from a GitHub user's
// recent public PushEvents. This is the canonical OSINT pattern for
// associating a real email with a GitHub account — many users configure
// their personal email in git, then push to a public repo.
//
// Free, no key strictly required (60 req/hr). Set GITHUB_TOKEN for 5000/hr.
func GitHubCommitEmails(ctx context.Context, input map[string]any) (*GitHubCommitEmailsOutput, error) {
	login, _ := input["login"].(string)
	login = strings.TrimSpace(login)
	if login == "" {
		return nil, errors.New("input.login required (GitHub user)")
	}
	pages := 3 // GitHub /events/public is paginated; 3 pages × 30 events covers ~most-recent month
	if v, ok := input["pages"].(float64); ok && v > 0 && v <= 10 {
		pages = int(v)
	}

	start := time.Now()
	type emailAgg struct {
		names map[string]struct{}
		repos map[string]struct{}
		count int
	}
	emails := map[string]*emailAgg{}
	totalEvents, totalCommits := 0, 0

	for page := 1; page <= pages; page++ {
		endpoint := fmt.Sprintf("https://api.github.com/users/%s/events/public?per_page=30&page=%d", login, page)
		body, err := githubGet(ctx, endpoint, os.Getenv("GITHUB_TOKEN"))
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break // partial-results tolerance for later pages
		}
		var events []struct {
			Type    string `json:"type"`
			Repo    struct {
				Name string `json:"name"`
			} `json:"repo"`
			Payload struct {
				Commits []struct {
					SHA    string `json:"sha"`
					Author struct {
						Email string `json:"email"`
						Name  string `json:"name"`
					} `json:"author"`
				} `json:"commits"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(body, &events); err != nil {
			break
		}
		if len(events) == 0 {
			break
		}
		totalEvents += len(events)
		for _, e := range events {
			if e.Type != "PushEvent" {
				continue
			}
			for _, c := range e.Payload.Commits {
				totalCommits++
				email := strings.ToLower(strings.TrimSpace(c.Author.Email))
				if email == "" {
					continue
				}
				agg, ok := emails[email]
				if !ok {
					agg = &emailAgg{names: map[string]struct{}{}, repos: map[string]struct{}{}}
					emails[email] = agg
				}
				agg.count++
				if c.Author.Name != "" {
					agg.names[c.Author.Name] = struct{}{}
				}
				if e.Repo.Name != "" {
					agg.repos[e.Repo.Name] = struct{}{}
				}
			}
		}
	}

	out := &GitHubCommitEmailsOutput{
		Login:       login,
		EventsRead:  totalEvents,
		CommitsRead: totalCommits,
		Source:      "api.github.com/events",
		TookMs:      time.Since(start).Milliseconds(),
	}
	if os.Getenv("GITHUB_TOKEN") == "" {
		out.Note = "unauthenticated (60 req/hr); set GITHUB_TOKEN for 5000 req/hr and far deeper history"
	}

	for email, agg := range emails {
		hit := GitHubEmailHit{
			Email:     email,
			Count:     agg.count,
			IsNoreply: strings.Contains(email, "@users.noreply.github.com"),
		}
		for n := range agg.names {
			hit.Names = append(hit.Names, n)
		}
		for r := range agg.repos {
			hit.Repos = append(hit.Repos, r)
		}
		sort.Strings(hit.Names)
		sort.Strings(hit.Repos)
		out.Emails = append(out.Emails, hit)
	}
	sort.Slice(out.Emails, func(i, j int) bool { return out.Emails[i].Count > out.Emails[j].Count })
	out.UniqueEmails = len(out.Emails)
	return out, nil
}

