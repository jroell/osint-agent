package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type SecretHit struct {
	Pattern    string `json:"pattern"`        // which pattern matched
	Repo       string `json:"repo"`           // owner/repo
	Path       string `json:"path"`           // file path within repo
	HTMLURL    string `json:"html_url"`       // browser-readable URL
	Confidence string `json:"confidence"`     // "high" | "medium" | "low"
}

type GitSecretsOutput struct {
	Target   string      `json:"target"`            // user or org searched
	Patterns []string    `json:"patterns_searched"`
	Hits     []SecretHit `json:"hits"`
	Skipped  []string    `json:"skipped,omitempty"` // patterns that hit GitHub rate limits
	TookMs   int64       `json:"tookMs"`
	Source   string      `json:"source"`
	Note     string      `json:"note,omitempty"`
}

// secretFilePatterns are common secret-bearing filenames + repo-search qualifiers.
// Each entry is `query` shape that GitHub's code-search syntax understands.
// We keep this list intentionally short — high-precision filenames first; broad
// substring searches blow through the API rate limit.
var secretFilePatterns = []struct {
	Query      string
	Confidence string
	Label      string
}{
	{"filename:.env", "high", ".env"},
	{"filename:.env.local", "high", ".env.local"},
	{"filename:.env.production", "high", ".env.production"},
	{"filename:credentials extension:json", "high", "credentials.json"},
	{"filename:secrets.yml", "high", "secrets.yml"},
	{"filename:id_rsa", "high", "id_rsa"},
	{"filename:id_dsa", "high", "id_dsa"},
	{"filename:.npmrc _authToken", "high", ".npmrc with auth token"},
	{"filename:.aws/credentials", "high", "aws credentials"},
	{"filename:wp-config.php DB_PASSWORD", "medium", "wp-config.php with DB pw"},
	{"\"AWS_SECRET_ACCESS_KEY\" extension:env", "high", "AWS_SECRET_ACCESS_KEY in env file"},
	{"\"BEGIN RSA PRIVATE KEY\"", "high", "RSA private key block"},
	{"\"xoxb-\" OR \"xoxp-\"", "high", "Slack bot/user token"},
	{"\"sk-\" filename:.env", "medium", "OpenAI/Anthropic-style key in .env"},
}

// LeakedSecretGitScan queries GitHub's code-search API for common secret-bearing
// filenames and key fingerprints scoped to a user or organization. Public-repo
// only. Requires a GitHub PAT in GITHUB_TOKEN (5000 req/hr authenticated;
// unauthenticated code search is blocked entirely by GitHub).
func LeakedSecretGitScan(ctx context.Context, input map[string]any) (*GitSecretsOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("input.target required (GitHub user or org login)")
	}
	scope := "user"
	if s, ok := input["scope"].(string); ok && s != "" {
		scope = s
	}
	if scope != "user" && scope != "org" {
		return nil, errors.New("input.scope must be \"user\" or \"org\"")
	}
	limitPerPattern := 10
	if v, ok := input["limit_per_pattern"].(float64); ok && v > 0 {
		limitPerPattern = int(v)
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, errors.New("GITHUB_TOKEN env var required (GitHub code search blocks unauthenticated requests)")
	}

	start := time.Now()
	out := &GitSecretsOutput{
		Target: target,
		Source: "github-code-search",
		Note:   "public repos only; high-precision filename patterns; matches are *candidates* — manually verify before taking action",
	}
	patterns := make([]string, 0, len(secretFilePatterns))
	for _, p := range secretFilePatterns {
		patterns = append(patterns, p.Label)
	}
	out.Patterns = patterns

	for _, p := range secretFilePatterns {
		q := fmt.Sprintf("%s %s:%s", p.Query, scope, target)
		hits, skipped := githubCodeSearch(ctx, token, q, limitPerPattern, p.Label, p.Confidence)
		if skipped != "" {
			out.Skipped = append(out.Skipped, p.Label+": "+skipped)
			continue
		}
		out.Hits = append(out.Hits, hits...)
	}

	sort.SliceStable(out.Hits, func(i, j int) bool { return out.Hits[i].Repo < out.Hits[j].Repo })
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func githubCodeSearch(ctx context.Context, token, q string, limit int, label, confidence string) ([]SecretHit, string) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	endpoint := "https://api.github.com/search/code?q=" + url.QueryEscape(q) + fmt.Sprintf("&per_page=%d", limit)
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "request build failed"
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "http error: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return nil, fmt.Sprintf("rate limited (%d)", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Sprintf("github %d", resp.StatusCode)
	}
	var body struct {
		Items []struct {
			Name       string `json:"name"`
			Path       string `json:"path"`
			HTMLURL    string `json:"html_url"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, "decode failed"
	}
	hits := make([]SecretHit, 0, len(body.Items))
	for _, it := range body.Items {
		hits = append(hits, SecretHit{
			Pattern:    label,
			Repo:       it.Repository.FullName,
			Path:       it.Path,
			HTMLURL:    it.HTMLURL,
			Confidence: confidence,
		})
	}
	return hits, ""
}
