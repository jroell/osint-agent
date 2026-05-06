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
	// ExtractedLogin is the GitHub username encoded in users.noreply.github.com
	// emails. Two formats are recognized:
	//   - "12345678+username@users.noreply.github.com" (modern, default
	//     when "Keep my email private" is enabled — the numeric prefix
	//     is the user's stable GitHub user-ID)
	//   - "username@users.noreply.github.com" (legacy, pre-2017)
	// Bot handles are surfaced without the "[bot]" cosmetic suffix.
	ExtractedLogin string `json:"extracted_github_login,omitempty"`
	// ExtractedUserID is the stable numeric GitHub user-ID from the
	// modern format. Useful for tracking rename history — the ID
	// is permanent while the login can change.
	ExtractedUserID string `json:"extracted_github_user_id,omitempty"`
	// LoginMatchesInput is true iff ExtractedLogin (case-insensitive)
	// equals the tool's input login. False on rename or collaborator
	// commits — both worth flagging.
	LoginMatchesInput bool `json:"login_matches_input,omitempty"`
}

type GitHubCommitEmailsOutput struct {
	Login        string           `json:"login"`
	EventsRead   int              `json:"events_read"`
	CommitsRead  int              `json:"commits_read"`
	UniqueEmails int              `json:"unique_emails"`
	Emails       []GitHubEmailHit `json:"emails"`
	// LeakedLogins is the deduped union of every GitHub login extracted
	// from noreply emails in this harvest. For an org-/repo-wide harvest
	// this is the contributor-handle list — directly feedable into
	// sherlock / RapidAPI cross-platform graph lookups.
	LeakedLogins []string `json:"leaked_logins"`
	// AliasLogins lists extracted logins that DIFFER from the input login
	// (case-insensitive). Surfaces rename history + collaborator commits.
	AliasLogins []string `json:"alias_logins"`
	Source      string   `json:"source"`
	TookMs      int64    `json:"tookMs"`
	Note        string   `json:"note,omitempty"`
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
			Type string `json:"type"`
			Repo struct {
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

	loginLower := strings.ToLower(login)
	leakedSet := map[string]struct{}{}
	aliasSet := map[string]struct{}{}

	for email, agg := range emails {
		hit := GitHubEmailHit{
			Email:     email,
			Count:     agg.count,
			IsNoreply: strings.Contains(email, "@users.noreply.github.com"),
		}
		if hit.IsNoreply {
			extractedLogin, userID := extractGitHubNoreplyLogin(email)
			hit.ExtractedLogin = extractedLogin
			hit.ExtractedUserID = userID
			if extractedLogin != "" {
				leakedSet[strings.ToLower(extractedLogin)] = struct{}{}
				if strings.ToLower(extractedLogin) == loginLower {
					hit.LoginMatchesInput = true
				} else {
					aliasSet[extractedLogin] = struct{}{}
				}
			}
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

	for l := range leakedSet {
		out.LeakedLogins = append(out.LeakedLogins, l)
	}
	sort.Strings(out.LeakedLogins)
	for l := range aliasSet {
		out.AliasLogins = append(out.AliasLogins, l)
	}
	sort.Strings(out.AliasLogins)
	return out, nil
}

// extractGitHubNoreplyLogin parses GitHub privacy-noreply emails and
// returns (login, numericUserID).
//
// Formats handled (verified against GitHub docs):
//
//  1. Modern "<id>+<login>@users.noreply.github.com"
//     (default since 2017; <id> is the user's permanent numeric ID,
//     <login> is the current account name)
//  2. Legacy "<login>@users.noreply.github.com"
//     (pre-2017; no numeric prefix)
//  3. Bot handles like "49699333+dependabot[bot]@users.noreply.github.com"
//     — the [bot] suffix is cosmetic and is stripped from the returned
//     login (the actual GH handle is "dependabot").
//
// Returns ("", "") for inputs that aren't users.noreply.github.com
// emails or whose local-part isn't parseable.
func extractGitHubNoreplyLogin(email string) (login, userID string) {
	const suffix = "@users.noreply.github.com"
	lower := strings.ToLower(strings.TrimSpace(email))
	if !strings.HasSuffix(lower, suffix) {
		return "", ""
	}
	// Use the original-case email for the returned login (GitHub
	// usernames are case-insensitive but conventional case is preserved
	// in commit metadata).
	original := strings.TrimSpace(email)
	local := original[:len(original)-len(suffix)]
	if local == "" {
		return "", ""
	}
	// Modern format: split on first '+'.
	if i := strings.IndexByte(local, '+'); i > 0 {
		head := local[:i]
		tail := local[i+1:]
		if isAllDigits(head) && tail != "" {
			return stripBotSuffix(tail), head
		}
	}
	// Legacy format: the entire local part is the login.
	return stripBotSuffix(local), ""
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func stripBotSuffix(login string) string {
	// "[bot]" is the cosmetic suffix GitHub appends to bot account
	// display names. The actual handle (used in URLs / API) is the
	// part before "[bot]".
	if strings.HasSuffix(strings.ToLower(login), "[bot]") {
		return login[:len(login)-5]
	}
	return login
}
