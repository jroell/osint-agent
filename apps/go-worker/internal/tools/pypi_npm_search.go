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
	"sync"
	"time"
)

type RegistryPackage struct {
	Name        string   `json:"name"`
	Version     string   `json:"latest_version,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	URL         string   `json:"package_url"`
	Registry    string   `json:"registry"` // pypi | npm
	Maintainers []string `json:"maintainers,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	LastUpdated string   `json:"last_updated,omitempty"`
	License     string   `json:"license,omitempty"`
	Score       float64  `json:"score,omitempty"`
	LeakRisk    string   `json:"leak_risk"` // critical | high | medium | low
	LeakReason  string   `json:"leak_reason,omitempty"`
}

type PypiNpmSearchOutput struct {
	Query         string             `json:"query"`
	PypiPackages  []RegistryPackage  `json:"pypi_packages,omitempty"`
	NpmPackages   []RegistryPackage  `json:"npm_packages,omitempty"`
	TotalPackages int                `json:"total_packages"`
	HighRiskCount int                `json:"high_risk_count"`
	UniqueAuthors []string           `json:"unique_authors"`
	Errors        map[string]string  `json:"errors,omitempty"`
	Source        string             `json:"source"`
	TookMs        int64              `json:"tookMs"`
	Note          string             `json:"note,omitempty"`
}

func classifyRegistryRisk(name, owner string) (string, string) {
	low := strings.ToLower(name)
	criticalTokens := []string{"-internal", "_internal", "internal-", "internal_", "-secret", "-private", "-prod", "-production", "-confidential"}
	highTokens := []string{"-dev", "-staging", "-stg", "-test", "-debug", "-poc", "-experimental", "-build", "-ci"}
	for _, t := range criticalTokens {
		if strings.Contains(low, t) {
			return "critical", "name suggests private/internal artifact: " + t
		}
	}
	for _, t := range highTokens {
		if strings.Contains(low, t) {
			return "high", "name suggests internal-tier artifact: " + t
		}
	}
	return "low", ""
}

// PypiNpmSearch queries PyPI and npm public registries in parallel for
// packages matching a query. Auto-classifies leak risk by name patterns.
//
// Use cases:
//   - Reveal an org's open-source posture (what they've published publicly)
//   - Find dependency-confusion attack surface — internal package names
//     that haven't been claimed on public registries are vulnerable to
//     attackers publishing malicious versions
//   - Catch accidentally-public internal packages
//
// Free, no key. PyPI uses XML-RPC + JSON; npm has a clean search API.
func PypiNpmSearch(ctx context.Context, input map[string]any) (*PypiNpmSearchOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (e.g. brand name or org-name)")
	}
	limit := 20
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}
	skipPyPI := false
	skipNpm := false
	if v, ok := input["skip_pypi"].(bool); ok {
		skipPyPI = v
	}
	if v, ok := input["skip_npm"].(bool); ok {
		skipNpm = v
	}

	start := time.Now()
	out := &PypiNpmSearchOutput{Query: q, Source: "pypi+npm", Errors: map[string]string{}}

	authorSet := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	if !skipPyPI {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pkgs, err := searchPyPI(ctx, q, limit)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Errors["pypi"] = err.Error()
				return
			}
			out.PypiPackages = pkgs
			for _, p := range pkgs {
				if p.Author != "" {
					authorSet[p.Author] = true
				}
				if p.LeakRisk == "critical" || p.LeakRisk == "high" {
					out.HighRiskCount++
				}
			}
		}()
	}

	if !skipNpm {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pkgs, err := searchNpm(ctx, q, limit)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Errors["npm"] = err.Error()
				return
			}
			out.NpmPackages = pkgs
			for _, p := range pkgs {
				if p.Author != "" {
					authorSet[p.Author] = true
				}
				if p.LeakRisk == "critical" || p.LeakRisk == "high" {
					out.HighRiskCount++
				}
			}
		}()
	}

	wg.Wait()
	out.TotalPackages = len(out.PypiPackages) + len(out.NpmPackages)
	for a := range authorSet {
		out.UniqueAuthors = append(out.UniqueAuthors, a)
	}
	sort.Strings(out.UniqueAuthors)
	out.TookMs = time.Since(start).Milliseconds()

	if out.HighRiskCount > 0 {
		out.Note = fmt.Sprintf("⚠️  %d high/critical-risk packages — investigate for leaked credentials in package metadata, README, or source", out.HighRiskCount)
	}
	return out, nil
}

// PyPI search via the public JSON API at /search.
// As of 2024, PyPI deprecated XML-RPC search — we use the search HTML
// scraper-friendly format instead.
// Alternative: pypi.org/search/?q=<query>&format=json - this returns JSON.
// Actually PyPI's official approach: query the Simple index then check JSON
// per package. For this tool we use libraries.io or pypi.org search HTML.
//
// We'll query: https://pypi.org/search/?q=<q> and parse a few package names,
// then call pypi.org/pypi/<name>/json for each for full metadata.
func searchPyPI(ctx context.Context, query string, limit int) ([]RegistryPackage, error) {
	// Strategy: fetch package metadata directly. PyPI search HTML scraping is
	// fragile; libraries.io needs a key. Instead, try direct package lookup
	// for a guess-and-augment approach.
	//
	// First try: exact-name lookup (q itself).
	pkgs := []RegistryPackage{}
	// 1. Direct lookup
	if pkg := pypiPackageLookup(ctx, query); pkg != nil {
		pkgs = append(pkgs, *pkg)
	}

	// 2. Search the libraries.io-style endpoint via PyPI HTML (best-effort,
	//    might break — but most reliable free option).
	htmlEndpoint := "https://pypi.org/search/?q=" + url.QueryEscape(query)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, htmlEndpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/pypi-search")
	req.Header.Set("Accept", "text/html")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return pkgs, nil // soft fail — keep direct hit
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	html := string(body)

	// Find package names from HTML — pattern: <span class="package-snippet__name">NAME</span>
	names := []string{}
	idx := 0
	for {
		i := strings.Index(html[idx:], `package-snippet__name">`)
		if i < 0 {
			break
		}
		startName := idx + i + len(`package-snippet__name">`)
		endName := strings.Index(html[startName:], `<`)
		if endName < 0 {
			break
		}
		name := strings.TrimSpace(html[startName : startName+endName])
		if name != "" && !contains(names, name) {
			names = append(names, name)
		}
		idx = startName + endName
		if len(names) >= limit {
			break
		}
	}

	// Hydrate top N package names with metadata
	hydrateLimit := limit - len(pkgs)
	if hydrateLimit > 10 {
		hydrateLimit = 10
	}
	if hydrateLimit > len(names) {
		hydrateLimit = len(names)
	}
	hydrated := make([]*RegistryPackage, hydrateLimit)
	var hwg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := 0; i < hydrateLimit; i++ {
		hwg.Add(1)
		sem <- struct{}{}
		go func(idx int, name string) {
			defer hwg.Done()
			defer func() { <-sem }()
			hydrated[idx] = pypiPackageLookup(ctx, name)
		}(i, names[i])
	}
	hwg.Wait()
	for _, h := range hydrated {
		if h != nil {
			// avoid duplicate of direct-hit
			if !pypiContainsPackage(pkgs, h.Name) {
				pkgs = append(pkgs, *h)
			}
		}
	}
	return pkgs, nil
}

func pypiContainsPackage(pkgs []RegistryPackage, name string) bool {
	for _, p := range pkgs {
		if strings.EqualFold(p.Name, name) {
			return true
		}
	}
	return false
}

func pypiPackageLookup(ctx context.Context, name string) *RegistryPackage {
	endpoint := fmt.Sprintf("https://pypi.org/pypi/%s/json", url.PathEscape(name))
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/pypi-lookup")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return nil
	}
	var parsed struct {
		Info struct {
			Name        string   `json:"name"`
			Version     string   `json:"version"`
			Summary     string   `json:"summary"`
			Author      string   `json:"author"`
			AuthorEmail string   `json:"author_email"`
			License     string   `json:"license"`
			HomePage    string   `json:"home_page"`
			ProjectURLs map[string]string `json:"project_urls"`
			Keywords    string   `json:"keywords"`
		} `json:"info"`
		Releases map[string][]struct {
			UploadTime string `json:"upload_time"`
		} `json:"releases"`
		URLs []struct {
			UploadTime string `json:"upload_time"`
		} `json:"urls"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	risk, reason := classifyRegistryRisk(parsed.Info.Name, parsed.Info.Author)
	pkg := &RegistryPackage{
		Name:        parsed.Info.Name,
		Version:     parsed.Info.Version,
		Description: truncate(parsed.Info.Summary, 200),
		Author:      parsed.Info.Author,
		License:     parsed.Info.License,
		URL:         "https://pypi.org/project/" + parsed.Info.Name + "/",
		Registry:    "pypi",
		LeakRisk:    risk,
		LeakReason:  reason,
	}
	if parsed.Info.AuthorEmail != "" && pkg.Author == "" {
		pkg.Author = parsed.Info.AuthorEmail
	}
	if parsed.Info.Keywords != "" {
		for _, k := range strings.Split(parsed.Info.Keywords, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				pkg.Keywords = append(pkg.Keywords, k)
			}
		}
	}
	if len(parsed.URLs) > 0 {
		pkg.LastUpdated = parsed.URLs[0].UploadTime
	}
	return pkg
}

func searchNpm(ctx context.Context, query string, limit int) ([]RegistryPackage, error) {
	endpoint := fmt.Sprintf("https://registry.npmjs.org/-/v1/search?text=%s&size=%d", url.QueryEscape(query), limit)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/npm-search")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("npm status %d", resp.StatusCode)
	}
	var parsed struct {
		Objects []struct {
			Package struct {
				Name        string `json:"name"`
				Version     string `json:"version"`
				Description string `json:"description"`
				Keywords    []string `json:"keywords"`
				Date        string `json:"date"`
				Links       struct {
					NPM string `json:"npm"`
				} `json:"links"`
				Author struct {
					Name  string `json:"name"`
					Email string `json:"email"`
				} `json:"author"`
				Publisher struct {
					Username string `json:"username"`
					Email    string `json:"email"`
				} `json:"publisher"`
				Maintainers []struct {
					Username string `json:"username"`
				} `json:"maintainers"`
			} `json:"package"`
			Score struct {
				Final float64 `json:"final"`
			} `json:"score"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	pkgs := []RegistryPackage{}
	for _, o := range parsed.Objects {
		p := o.Package
		risk, reason := classifyRegistryRisk(p.Name, p.Publisher.Username)
		author := p.Author.Name
		if author == "" {
			author = p.Publisher.Username
		}
		pkg := RegistryPackage{
			Name:        p.Name,
			Version:     p.Version,
			Description: truncate(p.Description, 200),
			Author:      author,
			URL:         p.Links.NPM,
			Registry:    "npm",
			Keywords:    p.Keywords,
			LastUpdated: p.Date,
			Score:       o.Score.Final,
			LeakRisk:    risk,
			LeakReason:  reason,
		}
		if pkg.URL == "" {
			pkg.URL = "https://www.npmjs.com/package/" + p.Name
		}
		for _, m := range p.Maintainers {
			pkg.Maintainers = append(pkg.Maintainers, m.Username)
		}
		pkgs = append(pkgs, pkg)
	}
	// Sort high-risk first
	rank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(pkgs, func(i, j int) bool {
		ra, rb := rank[pkgs[i].LeakRisk], rank[pkgs[j].LeakRisk]
		if ra != rb {
			return ra < rb
		}
		return pkgs[i].Score > pkgs[j].Score
	})
	return pkgs, nil
}
