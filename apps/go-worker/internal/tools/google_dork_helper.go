package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type GoogleDork struct {
	Category    string `json:"category"`
	Severity    string `json:"severity"` // critical | high | medium | low (likelihood of valuable finding)
	Query       string `json:"query"`
	Description string `json:"description"`
	GoogleURL   string `json:"google_url"` // direct URL — agent can click or feed to google_dork_search
	BingURL     string `json:"bing_url"`
	DDGURL      string `json:"ddg_url"`
}

type GoogleDorkHelperOutput struct {
	Target     string       `json:"target"`
	Categories []string     `json:"categories_included"`
	TotalDorks int          `json:"total_dorks"`
	Dorks      []GoogleDork `json:"dorks"`
	HighestSeverity string  `json:"highest_severity_in_set"`
	Note       string       `json:"note,omitempty"`
	Source     string       `json:"source"`
	TookMs     int64        `json:"tookMs"`
}

// GoogleDorkHelper generates a CATALOG of Google dork queries targeting a
// specific domain. Returns ~25 categorized templates ranging from low-risk
// (subdomain enum) to critical (config files exposed).
//
// Each dork includes ready-to-click URLs for Google, Bing, and DuckDuckGo —
// the agent can either click directly or feed any string to
// `google_dork_search` for programmatic execution.
//
// Categories:
//   - admin_panels        → admin login pages (high-value if accessible)
//   - exposed_configs     → .env, config.json, web.config (CRITICAL)
//   - credentials_in_files → API keys, passwords in code
//   - file_indexes        → directory listing exposed
//   - login_pages         → auth surfaces for credential testing
//   - subdomain_enum      → site:*.target -www
//   - error_messages      → SQL/PHP/Java errors leaking schema
//   - file_extension_dorks → .pdf, .doc, .xls (employee/internal docs)
//   - cloud_storage       → s3.amazonaws.com / blob.core.windows.net for target
//   - github_leaks        → "target.com" site:github.com
//   - exposed_apis        → swagger.json, openapi.yaml, .well-known
//   - log_files           → access.log, error.log
//   - backup_files        → .bak, .old, .backup
//
// Pure utility — no external API. The agent runs the dorks via separate
// search tools.
func GoogleDorkHelper(_ context.Context, input map[string]any) (*GoogleDorkHelperOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return nil, errors.New("input.target required (apex domain or specific URL)")
	}
	target = strings.TrimPrefix(target, "https://")
	target = strings.TrimPrefix(target, "http://")
	target = strings.TrimSuffix(target, "/")

	// Allow filtering by category list
	var includeCats []string
	if v, ok := input["categories"].([]any); ok {
		for _, x := range v {
			if s, ok := x.(string); ok {
				includeCats = append(includeCats, s)
			}
		}
	}
	include := func(cat string) bool {
		if len(includeCats) == 0 {
			return true
		}
		for _, c := range includeCats {
			if c == cat {
				return true
			}
		}
		return false
	}

	start := time.Now()
	dorks := []GoogleDork{}

	// --- CRITICAL severity ---
	if include("exposed_configs") {
		dorks = append(dorks, []GoogleDork{
			{Category: "exposed_configs", Severity: "critical",
				Query: fmt.Sprintf(`site:%s ext:env`, target),
				Description: ".env files (almost always contain DB creds, API keys)"},
			{Category: "exposed_configs", Severity: "critical",
				Query: fmt.Sprintf(`site:%s (ext:json OR ext:yaml OR ext:yml) (config OR settings)`, target),
				Description: "config files in JSON/YAML"},
			{Category: "exposed_configs", Severity: "critical",
				Query: fmt.Sprintf(`site:%s ext:xml inurl:web.config`, target),
				Description: "ASP.NET web.config (often has connection strings)"},
			{Category: "exposed_configs", Severity: "critical",
				Query: fmt.Sprintf(`site:%s ext:json inurl:appsettings`, target),
				Description: ".NET Core appsettings.json"},
			{Category: "exposed_configs", Severity: "critical",
				Query: fmt.Sprintf(`site:%s "DB_PASSWORD" OR "AWS_SECRET" OR "API_KEY"`, target),
				Description: "credential keywords"},
		}...)
	}
	if include("credentials_in_files") {
		dorks = append(dorks, []GoogleDork{
			{Category: "credentials_in_files", Severity: "critical",
				Query: fmt.Sprintf(`"%s" "BEGIN PRIVATE KEY"`, target),
				Description: "PEM-formatted private keys mentioning the org"},
			{Category: "credentials_in_files", Severity: "critical",
				Query: fmt.Sprintf(`site:%s intext:"-----BEGIN OPENSSH PRIVATE KEY-----"`, target),
				Description: "OpenSSH private keys"},
		}...)
	}

	// --- HIGH severity ---
	if include("admin_panels") {
		dorks = append(dorks, []GoogleDork{
			{Category: "admin_panels", Severity: "high",
				Query: fmt.Sprintf(`site:%s inurl:admin`, target),
				Description: "admin URLs"},
			{Category: "admin_panels", Severity: "high",
				Query: fmt.Sprintf(`site:%s inurl:wp-admin OR inurl:wp-login`, target),
				Description: "WordPress admin/login"},
			{Category: "admin_panels", Severity: "high",
				Query: fmt.Sprintf(`site:%s (inurl:phpmyadmin OR inurl:adminer)`, target),
				Description: "phpMyAdmin / Adminer panels"},
			{Category: "admin_panels", Severity: "high",
				Query: fmt.Sprintf(`site:%s intitle:"sign in" OR intitle:"log in" OR intitle:"login"`, target),
				Description: "all login pages"},
		}...)
	}
	if include("file_indexes") {
		dorks = append(dorks, []GoogleDork{
			{Category: "file_indexes", Severity: "high",
				Query: fmt.Sprintf(`site:%s intitle:"index of"`, target),
				Description: "Apache/Nginx directory listings"},
			{Category: "file_indexes", Severity: "high",
				Query: fmt.Sprintf(`site:%s "Index of /" "Last modified"`, target),
				Description: "directory listings (alternate phrasing)"},
		}...)
	}
	if include("error_messages") {
		dorks = append(dorks, []GoogleDork{
			{Category: "error_messages", Severity: "high",
				Query: fmt.Sprintf(`site:%s "sql syntax error" OR "PostgreSQL error" OR "ORA-00933"`, target),
				Description: "SQL error leaks (schema disclosure)"},
			{Category: "error_messages", Severity: "high",
				Query: fmt.Sprintf(`site:%s "Fatal error" OR "Stack trace" OR "Traceback"`, target),
				Description: "PHP/Python/Ruby stack traces"},
		}...)
	}
	if include("backup_files") {
		dorks = append(dorks, []GoogleDork{
			{Category: "backup_files", Severity: "high",
				Query: fmt.Sprintf(`site:%s (ext:bak OR ext:old OR ext:backup OR ext:save)`, target),
				Description: "backup files (often editable copies of production)"},
			{Category: "backup_files", Severity: "high",
				Query: fmt.Sprintf(`site:%s ext:sql`, target),
				Description: "SQL dump files"},
		}...)
	}

	// --- MEDIUM severity ---
	if include("exposed_apis") {
		dorks = append(dorks, []GoogleDork{
			{Category: "exposed_apis", Severity: "medium",
				Query: fmt.Sprintf(`site:%s (inurl:swagger.json OR inurl:openapi.yaml OR inurl:api-docs)`, target),
				Description: "OpenAPI/Swagger spec files"},
			{Category: "exposed_apis", Severity: "medium",
				Query: fmt.Sprintf(`site:%s inurl:graphql`, target),
				Description: "GraphQL endpoints"},
		}...)
	}
	if include("log_files") {
		dorks = append(dorks, []GoogleDork{
			{Category: "log_files", Severity: "medium",
				Query: fmt.Sprintf(`site:%s (ext:log OR inurl:access.log OR inurl:error.log)`, target),
				Description: "log files"},
		}...)
	}
	if include("file_extension_dorks") {
		dorks = append(dorks, []GoogleDork{
			{Category: "file_extension_dorks", Severity: "medium",
				Query: fmt.Sprintf(`site:%s (ext:pdf OR ext:doc OR ext:docx OR ext:xls OR ext:xlsx)`, target),
				Description: "internal documents (PDFs, Office)"},
			{Category: "file_extension_dorks", Severity: "medium",
				Query: fmt.Sprintf(`site:%s ext:csv`, target),
				Description: "CSV exports (often contain emails/contacts)"},
		}...)
	}
	if include("github_leaks") {
		dorks = append(dorks, []GoogleDork{
			{Category: "github_leaks", Severity: "medium",
				Query: fmt.Sprintf(`"%s" site:github.com`, target),
				Description: "GitHub mentions of target (use github_code_search for deeper)"},
			{Category: "github_leaks", Severity: "medium",
				Query: fmt.Sprintf(`"%s" site:gist.github.com`, target),
				Description: "GitHub Gist mentions"},
			{Category: "github_leaks", Severity: "medium",
				Query: fmt.Sprintf(`"%s" site:pastebin.com`, target),
				Description: "Pastebin mentions"},
		}...)
	}

	// --- LOW severity ---
	if include("subdomain_enum") {
		dorks = append(dorks, []GoogleDork{
			{Category: "subdomain_enum", Severity: "low",
				Query: fmt.Sprintf(`site:*.%s -www`, target),
				Description: "subdomain enumeration via Google index"},
		}...)
	}
	if include("cloud_storage") {
		dorks = append(dorks, []GoogleDork{
			{Category: "cloud_storage", Severity: "medium",
				Query: fmt.Sprintf(`"%s" site:s3.amazonaws.com`, target),
				Description: "AWS S3 buckets mentioning target"},
			{Category: "cloud_storage", Severity: "medium",
				Query: fmt.Sprintf(`"%s" site:blob.core.windows.net`, target),
				Description: "Azure Blob Storage mentioning target"},
			{Category: "cloud_storage", Severity: "medium",
				Query: fmt.Sprintf(`"%s" site:storage.googleapis.com`, target),
				Description: "GCS buckets mentioning target"},
		}...)
	}

	// Build URLs for each dork
	for i := range dorks {
		dorks[i].GoogleURL = "https://www.google.com/search?q=" + urlEncode(dorks[i].Query)
		dorks[i].BingURL = "https://www.bing.com/search?q=" + urlEncode(dorks[i].Query)
		dorks[i].DDGURL = "https://duckduckgo.com/?q=" + urlEncode(dorks[i].Query)
	}

	// Highest severity present
	severityRank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	highest := "low"
	for _, d := range dorks {
		if severityRank[d.Severity] < severityRank[highest] {
			highest = d.Severity
		}
	}

	// Categories included
	catSet := map[string]bool{}
	for _, d := range dorks {
		catSet[d.Category] = true
	}
	categories := []string{}
	for c := range catSet {
		categories = append(categories, c)
	}

	out := &GoogleDorkHelperOutput{
		Target:          target,
		Categories:      categories,
		TotalDorks:      len(dorks),
		Dorks:           dorks,
		HighestSeverity: highest,
		Source:          "google_dork_helper",
		TookMs:          time.Since(start).Milliseconds(),
		Note:            "These are TEMPLATES. Run via google_dork_search tool, or click the URLs directly. Critical/high dorks may surface security findings — handle responsibly per scope of authorization.",
	}
	return out, nil
}

func urlEncode(s string) string {
	out := strings.Builder{}
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			out.WriteRune(c)
		} else {
			b := make([]byte, 4)
			n := utf8EncodeRune(b, c)
			for i := 0; i < n; i++ {
				out.WriteString(fmt.Sprintf("%%%02X", b[i]))
			}
		}
	}
	return out.String()
}

func utf8EncodeRune(p []byte, r rune) int {
	switch {
	case r < 0x80:
		p[0] = byte(r)
		return 1
	case r < 0x800:
		p[0] = 0xC0 | byte(r>>6)
		p[1] = 0x80 | byte(r&0x3F)
		return 2
	case r < 0x10000:
		p[0] = 0xE0 | byte(r>>12)
		p[1] = 0x80 | byte((r>>6)&0x3F)
		p[2] = 0x80 | byte(r&0x3F)
		return 3
	default:
		p[0] = 0xF0 | byte(r>>18)
		p[1] = 0x80 | byte((r>>12)&0x3F)
		p[2] = 0x80 | byte((r>>6)&0x3F)
		p[3] = 0x80 | byte(r&0x3F)
		return 4
	}
}
