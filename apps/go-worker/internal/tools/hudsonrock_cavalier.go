package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// HudsonRockCavalier queries Hudson Rock's free Cavalier OSINT API.
//
// Cavalier is a database of 35M+ infostealer logs scraped from cybercriminal
// markets. Each log records a single computer compromised by infostealer
// malware (Redline, Vidar, Lumma, RisePro, etc.) and the credentials saved
// in browsers on that machine at the time of infection. This is otherwise
// expensive intelligence — Hudson Rock surfaces the lookup tier free as
// lead-generation for their enterprise product.
//
// Three modes:
//   - "email"    : looks up an email address → list of infections that have
//                  this address among the saved logins (with computer name,
//                  OS, IP prefix, malware path, antivirus, top passwords,
//                  date of infection, total services compromised on that
//                  machine)
//   - "domain"   : looks up a corporate domain → aggregate stats on how many
//                  employees / users / third parties have credentials saved
//                  for this domain across the dataset, plus breakdown of
//                  which URLs/endpoints (login forms) were hit
//   - "username" : looks up a username → infections where this username
//                  appears in saved logins
//
// Free tier, no auth. Results are partial (passwords/logins/IPs are
// star-redacted) but the metadata is fully present.

type HRCStealer struct {
	TotalCorporateServices int      `json:"total_corporate_services,omitempty"`
	TotalUserServices      int      `json:"total_user_services,omitempty"`
	DateCompromised        string   `json:"date_compromised,omitempty"`
	ComputerName           string   `json:"computer_name,omitempty"`
	OperatingSystem        string   `json:"operating_system,omitempty"`
	MalwarePath            string   `json:"malware_path,omitempty"`
	Antiviruses            []string `json:"antiviruses,omitempty"`
	IP                     string   `json:"ip,omitempty"`
	TopPasswords           []string `json:"top_passwords,omitempty"`
	TopLogins              []string `json:"top_logins,omitempty"`
}

type HRCDomainURL struct {
	Occurrence int    `json:"occurrence"`
	Type       string `json:"type"`
	URL        string `json:"url"`
}

type HRCDomainData struct {
	EmployeesURLs   []HRCDomainURL `json:"employees_urls,omitempty"`
	UsersURLs       []HRCDomainURL `json:"users_urls,omitempty"`
	ThirdPartiesURLs []HRCDomainURL `json:"third_parties_urls,omitempty"`
}

type HudsonRockCavalierOutput struct {
	Mode              string         `json:"mode"`
	Query             string         `json:"query"`
	Compromised       bool           `json:"compromised"`
	Message           string         `json:"message,omitempty"`
	Total             int            `json:"total,omitempty"`
	TotalStealers     int            `json:"total_stealers,omitempty"`
	Employees         int            `json:"employees,omitempty"`
	Users             int            `json:"users,omitempty"`
	ThirdParties      int            `json:"third_parties,omitempty"`
	Logo              string         `json:"logo,omitempty"`
	Stealers          []HRCStealer   `json:"stealers,omitempty"`
	DomainData        *HRCDomainData `json:"domain_data,omitempty"`
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
	Note              string         `json:"note,omitempty"`
}

// raw shapes
type hrcEmailRaw struct {
	Message  string       `json:"message"`
	Stealers []HRCStealer `json:"stealers"`
	// Cavalier sometimes wraps successful zero-hit responses differently;
	// we tolerate either shape.
	Success *bool  `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
}

type hrcDomainRaw struct {
	Total         int           `json:"total"`
	TotalStealers int           `json:"totalStealers"`
	Employees     int           `json:"employees"`
	Users         int           `json:"users"`
	ThirdParties  int           `json:"third_parties"`
	Logo          string        `json:"logo"`
	Data          HRCDomainData `json:"data"`
	Message       string        `json:"message,omitempty"`
	Success       *bool         `json:"success,omitempty"`
	Error         string        `json:"error,omitempty"`
}

func HudsonRockCavalier(ctx context.Context, input map[string]any) (*HudsonRockCavalierOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, fmt.Errorf("input.query required")
	}
	if mode == "" {
		// Auto-detect: email contains @, otherwise domain if has dot, else username
		switch {
		case strings.Contains(q, "@"):
			mode = "email"
		case strings.Contains(q, "."):
			mode = "domain"
		default:
			mode = "username"
		}
	}

	out := &HudsonRockCavalierOutput{
		Mode:   mode,
		Query:  q,
		Source: "cavalier.hudsonrock.com (free OSINT tools)",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "email", "username":
		endpoint := "search-by-email"
		paramKey := "email"
		if mode == "username" {
			endpoint = "search-by-username"
			paramKey = "username"
		}
		params := url.Values{}
		params.Set(paramKey, q)
		u := "https://cavalier.hudsonrock.com/api/json/v2/osint-tools/" + endpoint + "?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("hudsonrock %s: %w", mode, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		var raw hrcEmailRaw
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("hudsonrock %s decode: %w", mode, err)
		}
		if raw.Error != "" && len(raw.Stealers) == 0 {
			out.Note = raw.Error
			out.HighlightFindings = []string{fmt.Sprintf("✓ '%s' not found in stealer logs", q)}
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		out.Stealers = raw.Stealers
		out.Total = len(raw.Stealers)
		out.Compromised = len(raw.Stealers) > 0
		if raw.Message != "" {
			out.Message = raw.Message
		}

	case "domain":
		params := url.Values{}
		params.Set("domain", q)
		u := "https://cavalier.hudsonrock.com/api/json/v2/osint-tools/search-by-domain?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("hudsonrock domain: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		var raw hrcDomainRaw
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("hudsonrock domain decode: %w", err)
		}
		if raw.Error != "" && raw.Total == 0 {
			out.Note = raw.Error
			out.HighlightFindings = []string{fmt.Sprintf("✓ no stealer logs touching '%s'", q)}
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		out.Total = raw.Total
		out.TotalStealers = raw.TotalStealers
		out.Employees = raw.Employees
		out.Users = raw.Users
		out.ThirdParties = raw.ThirdParties
		out.Logo = raw.Logo
		// Truncate URL lists to top 20 by occurrence
		dd := raw.Data
		dd.EmployeesURLs = topURLs(dd.EmployeesURLs, 20)
		dd.UsersURLs = topURLs(dd.UsersURLs, 20)
		dd.ThirdPartiesURLs = topURLs(dd.ThirdPartiesURLs, 20)
		out.DomainData = &dd
		out.Compromised = raw.Total > 0
		out.Message = raw.Message

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: email, domain, username (or omit for auto-detect)", mode)
	}

	out.HighlightFindings = buildHRCHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func topURLs(urls []HRCDomainURL, n int) []HRCDomainURL {
	if len(urls) <= n {
		return urls
	}
	sort.Slice(urls, func(i, j int) bool { return urls[i].Occurrence > urls[j].Occurrence })
	return urls[:n]
}

func buildHRCHighlights(o *HudsonRockCavalierOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "email", "username":
		if !o.Compromised {
			hi = append(hi, fmt.Sprintf("✓ '%s' clean — not in any stealer log", o.Query))
			return hi
		}
		hi = append(hi, fmt.Sprintf("⚠️  COMPROMISED: '%s' appears in %d infostealer log(s)", o.Query, o.Total))
		// Surface latest infection
		latest := ""
		for _, s := range o.Stealers {
			if s.DateCompromised > latest {
				latest = s.DateCompromised
			}
		}
		if latest != "" {
			hi = append(hi, "  most recent infection: "+latest)
		}
		// Top infection details
		for i, s := range o.Stealers {
			if i >= 3 {
				break
			}
			parts := []string{}
			if s.DateCompromised != "" {
				parts = append(parts, "date="+s.DateCompromised)
			}
			if s.ComputerName != "" {
				parts = append(parts, "host="+s.ComputerName)
			}
			if s.OperatingSystem != "" {
				parts = append(parts, "os="+s.OperatingSystem)
			}
			if s.IP != "" {
				parts = append(parts, "ip="+s.IP)
			}
			if s.TotalCorporateServices > 0 {
				parts = append(parts, fmt.Sprintf("corp_creds=%d", s.TotalCorporateServices))
			}
			if s.TotalUserServices > 0 {
				parts = append(parts, fmt.Sprintf("user_creds=%d", s.TotalUserServices))
			}
			hi = append(hi, "  ["+fmt.Sprintf("%d", i+1)+"] "+strings.Join(parts, " "))
		}
	case "domain":
		if !o.Compromised {
			hi = append(hi, fmt.Sprintf("✓ '%s' clean — no stealer logs touch this domain", o.Query))
			return hi
		}
		hi = append(hi, fmt.Sprintf("⚠️  '%s' appears in %d stealer logs (across %d total stealers in dataset)", o.Query, o.Total, o.TotalStealers))
		hi = append(hi, fmt.Sprintf("  • employees w/ stolen creds: %d", o.Employees))
		hi = append(hi, fmt.Sprintf("  • users w/ stolen creds: %d", o.Users))
		hi = append(hi, fmt.Sprintf("  • third-parties w/ stolen creds: %d", o.ThirdParties))
		if o.DomainData != nil {
			if len(o.DomainData.EmployeesURLs) > 0 {
				hi = append(hi, "  TOP EMPLOYEE-COMPROMISED ENDPOINTS:")
				for i, u := range o.DomainData.EmployeesURLs {
					if i >= 5 {
						break
					}
					hi = append(hi, fmt.Sprintf("    [%d hits] %s", u.Occurrence, u.URL))
				}
			}
			if len(o.DomainData.UsersURLs) > 0 {
				hi = append(hi, "  TOP USER-COMPROMISED ENDPOINTS:")
				for i, u := range o.DomainData.UsersURLs {
					if i >= 3 {
						break
					}
					hi = append(hi, fmt.Sprintf("    [%d hits] %s", u.Occurrence, u.URL))
				}
			}
		}
	}
	return hi
}
