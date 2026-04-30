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

type GitHubCodeMatch struct {
	Repo       string   `json:"repo"`
	RepoURL    string   `json:"repo_url"`
	Path       string   `json:"path"`
	HTMLURL    string   `json:"html_url"`
	Owner      string   `json:"owner"`
	Score      float64  `json:"score"`
	Severity   string   `json:"severity"`         // critical | high | medium | low
	SeverityReason string `json:"severity_reason,omitempty"`
	Snippets   []string `json:"text_snippets,omitempty"`
}

type GitHubCodeSearchOutput struct {
	Query           string             `json:"query"`
	TotalCount      int                `json:"total_count"`
	IncompleteResults bool             `json:"incomplete_results"`
	Returned        int                `json:"returned"`
	Matches         []GitHubCodeMatch  `json:"matches"`
	UniqueRepos     []string           `json:"unique_repos"`
	UniqueOwners    []string           `json:"unique_owners"`
	SeverityBreakdown map[string]int   `json:"severity_breakdown"`
	HighSeverityFiles []string         `json:"high_severity_paths,omitempty"`
	Source          string             `json:"source"`
	TookMs          int64              `json:"tookMs"`
	Note            string             `json:"note,omitempty"`
}

// classifyCodeMatch rates a code-search hit by leak severity based on file
// path + filename signals. Critical = config/secrets files; high = infra
// IaC; medium = source code; low = tests/docs.
func classifyCodeMatch(path string) (severity, reason string) {
	low := strings.ToLower(path)
	base := low
	if i := strings.LastIndex(low, "/"); i >= 0 {
		base = low[i+1:]
	}

	criticalPatterns := []struct {
		pat    string
		reason string
	}{
		{".env", "matches in .env files frequently leak credentials"},
		{"secrets", "path contains 'secrets' — high leak risk"},
		{"credential", "path contains 'credential'"},
		{".pem", "PEM key file"},
		{"id_rsa", "private SSH key file"},
		{"private_key", "filename indicates private key"},
		{"settings.py", "django settings often contain SECRET_KEY/DB creds"},
		{"config.py", "python config file"},
		{"web.config", ".NET web config (often contains connection strings)"},
		{"appsettings.json", ".NET appsettings (often has secrets)"},
	}
	highPatterns := []struct {
		pat    string
		reason string
	}{
		{"dockerfile", "Dockerfile may pin internal registry URLs"},
		{"docker-compose", "compose file exposes service URLs/credentials"},
		{"terraform", "terraform IaC reveals infrastructure layout"},
		{".tf", "terraform IaC"},
		{"k8s", "kubernetes config"},
		{"kubernetes", "kubernetes config"},
		{".yaml", "yaml config"},
		{".yml", "yaml config"},
		{"helm", "helm chart"},
		{"makefile", "Makefile may have hardcoded URLs/credentials"},
		{"ci.yml", "CI config may expose deployment targets"},
		{".github/workflows", "GitHub Actions exposes CI/CD secrets layout"},
		{"jenkins", "Jenkins config"},
	}
	mediumPatterns := []string{
		".js", ".ts", ".jsx", ".tsx", ".go", ".py", ".rb", ".java", ".cs", ".php", ".rs", ".kt",
	}
	lowPatterns := []string{
		"test", "spec", "fixtures", "examples", ".md", ".rst", "readme", "docs/", "documentation/",
	}

	for _, p := range criticalPatterns {
		if strings.Contains(base, p.pat) || strings.Contains(low, p.pat) {
			return "critical", p.reason
		}
	}
	for _, p := range highPatterns {
		if strings.Contains(base, p.pat) || strings.Contains(low, p.pat) {
			return "high", p.reason
		}
	}
	for _, p := range lowPatterns {
		if strings.Contains(low, p) {
			return "low", "test/doc/example file"
		}
	}
	for _, ext := range mediumPatterns {
		if strings.HasSuffix(base, ext) {
			return "medium", "source code file"
		}
	}
	return "low", "unclassified"
}

// GitHubCodeSearch searches GitHub's public-repo code index for a query
// string. Returns matched repos, file paths, and HTML URLs grouped by leak
// severity. Use cases:
//   - "api.targetcompany.com" → finds every public repo that hardcoded that URL
//     (often reveals deprecated subdomains, internal-only endpoints leaked into
//     public dockerfiles, partner integration repos)
//   - employee@target.com → reveals public repos owned by that employee
//   - internal hostname pattern → maps internal infrastructure
//
// Auth: GITHUB_TOKEN env var → 30 req/min (vs 10 unauth). Without auth,
// search results are degraded (no full snippets).
//
// Free; the only "cost" is the GitHub Code Search rate limit which is high
// for our scale.
func GitHubCodeSearch(ctx context.Context, input map[string]any) (*GitHubCodeSearchOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (e.g. 'api.vurvey.app' or an email address)")
	}
	limit := 30
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}
	// Optional filters.
	if lang, ok := input["language"].(string); ok && lang != "" {
		q = fmt.Sprintf("%s language:%s", q, lang)
	}
	if repo, ok := input["repo"].(string); ok && repo != "" {
		q = fmt.Sprintf("%s repo:%s", q, repo)
	}
	if user, ok := input["user"].(string); ok && user != "" {
		q = fmt.Sprintf("%s user:%s", q, user)
	}
	if extension, ok := input["extension"].(string); ok && extension != "" {
		q = fmt.Sprintf("%s extension:%s", q, extension)
	}
	if path, ok := input["in_path"].(string); ok && path != "" {
		q = fmt.Sprintf("%s path:%s", q, path)
	}

	apiKey := os.Getenv("GITHUB_TOKEN")
	if apiKey == "" {
		// Allow unauth but warn — results are degraded
	}

	start := time.Now()

	endpoint := fmt.Sprintf("https://api.github.com/search/code?q=%s&per_page=%d", url.QueryEscape(q), limit)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.text-match+json") // include text match snippets
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "osint-agent/code-search")
	if apiKey != "" {
		req.Header.Set("Authorization", "token "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github code search failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("github 403: %s — likely rate-limited or token lacks scope. Headers: %s", truncate(string(body), 200), resp.Header.Get("X-RateLimit-Remaining"))
	}
	if resp.StatusCode == 422 {
		return nil, fmt.Errorf("github 422 (validation): query may need quoting or contain unsupported operators. Body: %s", truncate(string(body), 200))
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		TotalCount        int  `json:"total_count"`
		IncompleteResults bool `json:"incomplete_results"`
		Items             []struct {
			Name       string  `json:"name"`
			Path       string  `json:"path"`
			HTMLURL    string  `json:"html_url"`
			Score      float64 `json:"score"`
			Repository struct {
				FullName string `json:"full_name"`
				HTMLURL  string `json:"html_url"`
				Owner    struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repository"`
			TextMatches []struct {
				Fragment string `json:"fragment"`
			} `json:"text_matches"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("github response parse failed: %w", err)
	}

	out := &GitHubCodeSearchOutput{
		Query:             q,
		TotalCount:        parsed.TotalCount,
		IncompleteResults: parsed.IncompleteResults,
		Returned:          len(parsed.Items),
		SeverityBreakdown: map[string]int{},
		Source:            "github_code_search",
	}
	repoSet := map[string]bool{}
	ownerSet := map[string]bool{}

	for _, it := range parsed.Items {
		sev, reason := classifyCodeMatch(it.Path)
		snips := []string{}
		for _, m := range it.TextMatches {
			s := strings.TrimSpace(m.Fragment)
			if s != "" {
				if len(s) > 200 {
					s = s[:200] + "…"
				}
				snips = append(snips, s)
			}
		}
		match := GitHubCodeMatch{
			Repo:           it.Repository.FullName,
			RepoURL:        it.Repository.HTMLURL,
			Path:           it.Path,
			HTMLURL:        it.HTMLURL,
			Owner:          it.Repository.Owner.Login,
			Score:          it.Score,
			Severity:       sev,
			SeverityReason: reason,
			Snippets:       snips,
		}
		out.Matches = append(out.Matches, match)
		out.SeverityBreakdown[sev]++
		repoSet[it.Repository.FullName] = true
		ownerSet[it.Repository.Owner.Login] = true
		if sev == "critical" || sev == "high" {
			out.HighSeverityFiles = append(out.HighSeverityFiles, fmt.Sprintf("[%s] %s/%s", sev, it.Repository.FullName, it.Path))
		}
	}

	// Sort matches by severity (critical first), then by score
	severityRank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(out.Matches, func(i, j int) bool {
		ra, rb := severityRank[out.Matches[i].Severity], severityRank[out.Matches[j].Severity]
		if ra != rb {
			return ra < rb
		}
		return out.Matches[i].Score > out.Matches[j].Score
	})

	for r := range repoSet {
		out.UniqueRepos = append(out.UniqueRepos, r)
	}
	for o := range ownerSet {
		out.UniqueOwners = append(out.UniqueOwners, o)
	}
	sort.Strings(out.UniqueRepos)
	sort.Strings(out.UniqueOwners)
	out.TookMs = time.Since(start).Milliseconds()

	if apiKey == "" {
		out.Note = "GITHUB_TOKEN not set — using unauth (10 req/min, no snippets). Set token for full results."
	}
	if parsed.TotalCount == 0 {
		if out.Note == "" {
			out.Note = "No matches in public GitHub. Query may be too rare, or string may not appear in indexed code."
		}
	}
	return out, nil
}
