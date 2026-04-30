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

// IETFPerson is one matched person.
type IETFPerson struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	ASCII      string `json:"ascii,omitempty"`
	Plain      string `json:"plain,omitempty"`
	ProfileURL string `json:"profile_url,omitempty"`
}

// IETFDocAuthorship is one (document, person) authorship row from
// /api/v1/doc/documentauthor/. The killer ER feature: tracks the
// affiliation + email AT TIME OF DOCUMENT AUTHORING.
type IETFDocAuthorship struct {
	DocName     string `json:"doc_name"`
	DocTitle    string `json:"doc_title,omitempty"`
	Affiliation string `json:"affiliation,omitempty"`
	Email       string `json:"email,omitempty"`
	Country     string `json:"country,omitempty"`
	AuthorOrder int    `json:"author_order,omitempty"`
}

// IETFAffiliation summarizes one (person, affiliation) pair across all docs.
type IETFAffiliation struct {
	Affiliation string `json:"affiliation"`
	DocCount    int    `json:"doc_count"`
}

// IETFEmailHistory summarizes (person, email) pairs.
type IETFEmailHistory struct {
	Email    string `json:"email"`
	DocCount int    `json:"doc_count"`
}

// IETFDocument is a slim doc-level result.
type IETFDocument struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Abstract    string `json:"abstract,omitempty"`
	Time        string `json:"time,omitempty"`
	Pages       int    `json:"pages,omitempty"`
	StdLevel    string `json:"intended_std_level,omitempty"`
	RFC         string `json:"rfc,omitempty"`
}

// IETFDataTrackerOutput is the response.
type IETFDataTrackerOutput struct {
	Mode             string             `json:"mode"`
	Query            string             `json:"query"`
	TotalResults     int                `json:"total_results"`
	People           []IETFPerson       `json:"people,omitempty"`
	Documents        []IETFDocument     `json:"documents,omitempty"`
	Authorships      []IETFDocAuthorship `json:"authorships,omitempty"`
	UniqueAffiliations []IETFAffiliation  `json:"unique_affiliations,omitempty"`
	UniqueEmails     []IETFEmailHistory `json:"unique_emails,omitempty"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source           string             `json:"source"`
	TookMs           int64              `json:"tookMs"`
	Note             string             `json:"note,omitempty"`
}

// raw structs
type ietfPersonRaw struct {
	Meta    struct{ TotalCount int `json:"total_count"` } `json:"meta"`
	Objects []struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		ASCII    string `json:"ascii"`
		Plain    string `json:"plain"`
		User     string `json:"user"`
	} `json:"objects"`
}

type ietfDocRaw struct {
	Meta    struct{ TotalCount int `json:"total_count"` } `json:"meta"`
	Objects []struct {
		Name             string `json:"name"`
		Title            string `json:"title"`
		Abstract         string `json:"abstract"`
		Time             string `json:"time"`
		Pages            int    `json:"pages"`
		IntendedStdLevel string `json:"intended_std_level"`
		RFC              string `json:"rfc"`
	} `json:"objects"`
}

type ietfAuthorshipRaw struct {
	Meta    struct{ TotalCount int `json:"total_count"` } `json:"meta"`
	Objects []struct {
		Document    string `json:"document"`
		Person      string `json:"person"`
		Email       string `json:"email"`
		Affiliation string `json:"affiliation"`
		Country     string `json:"country"`
		Order       int    `json:"order"`
	} `json:"objects"`
}

// IETFDataTrackerSearch queries the public IETF datatracker API.
// Free, no auth.
//
// Modes:
//   - "person_search" : name → IETF person IDs (case-insensitive contains)
//   - "person_docs"   : by person ID → all authorship rows (with affiliation
//                       and email AT THE TIME of each document)
//   - "doc_search"    : draft/RFC name prefix or title keyword
//   - "doc_lookup"    : by exact doc name (e.g. "draft-ietf-tls-rfc8446bis"
//                       or "rfc8446")
//
// Why this matters for ER:
//   - IETF tracks ~50 years of internet protocol design with stable person
//     IDs. Cryptographers, network engineers, security researchers, and
//     standards-body participants all touch IETF.
//   - The /documentauthor table records affiliation + email AT TIME OF
//     EACH DOCUMENT — meaning a person's IETF profile gives a temporal
//     audit trail of their employer + email history. Jim Schaad's 105
//     authored docs show his career evolve from Microsoft → Soaring Hawk
//     Consulting, with his email shifting from jimsch@microsoft.com →
//     ietf@augustcellars.com.
//   - Working group context (drafts named like "draft-ietf-tls-X" or
//     "draft-ietf-oauth-Y") reveals technical specialty.
//   - For security/cryptographer ER specifically, IETF is one of the
//     highest-trust public sources (RFCs are essentially career CVs for
//     network protocol designers).
func IETFDataTrackerSearch(ctx context.Context, input map[string]any) (*IETFDataTrackerOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "person_search"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required")
	}
	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 200 {
		limit = int(v)
	}

	out := &IETFDataTrackerOutput{
		Mode:   mode,
		Query:  query,
		Source: "datatracker.ietf.org/api/v1",
	}
	start := time.Now()
	client := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "person_search":
		people, total, err := ietfPersonSearch(ctx, client, query, limit)
		if err != nil {
			return nil, err
		}
		out.People = people
		out.TotalResults = total
	case "person_docs":
		// query is either numeric person ID or a name → first resolve to ID
		personID := 0
		if _, err := fmt.Sscanf(query, "%d", &personID); err != nil || personID == 0 {
			people, _, perr := ietfPersonSearch(ctx, client, query, 1)
			if perr != nil {
				return nil, perr
			}
			if len(people) == 0 {
				out.Note = fmt.Sprintf("no IETF person matching '%s'", query)
				out.HighlightFindings = []string{out.Note}
				out.TookMs = time.Since(start).Milliseconds()
				return out, nil
			}
			personID = people[0].ID
		}
		auths, total, err := ietfPersonDocs(ctx, client, personID, limit)
		if err != nil {
			return nil, err
		}
		out.Authorships = auths
		out.TotalResults = total

		// Aggregate affiliation + email histories
		affMap := map[string]int{}
		emailMap := map[string]int{}
		for _, a := range auths {
			if a.Affiliation != "" {
				affMap[a.Affiliation]++
			}
			if a.Email != "" {
				emailMap[a.Email]++
			}
		}
		for af, c := range affMap {
			out.UniqueAffiliations = append(out.UniqueAffiliations, IETFAffiliation{Affiliation: af, DocCount: c})
		}
		sort.SliceStable(out.UniqueAffiliations, func(i, j int) bool {
			return out.UniqueAffiliations[i].DocCount > out.UniqueAffiliations[j].DocCount
		})
		for em, c := range emailMap {
			out.UniqueEmails = append(out.UniqueEmails, IETFEmailHistory{Email: em, DocCount: c})
		}
		sort.SliceStable(out.UniqueEmails, func(i, j int) bool {
			return out.UniqueEmails[i].DocCount > out.UniqueEmails[j].DocCount
		})
	case "doc_search":
		docs, total, err := ietfDocSearch(ctx, client, query, limit)
		if err != nil {
			return nil, err
		}
		out.Documents = docs
		out.TotalResults = total
	case "doc_lookup":
		doc, err := ietfDocLookup(ctx, client, query)
		if err != nil {
			return nil, err
		}
		if doc == nil {
			out.Note = fmt.Sprintf("no IETF document with name '%s'", query)
			out.HighlightFindings = []string{out.Note}
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		out.Documents = []IETFDocument{*doc}
		out.TotalResults = 1
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: person_search, person_docs, doc_search, doc_lookup", mode)
	}

	out.HighlightFindings = buildIETFHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func ietfPersonSearch(ctx context.Context, client *http.Client, query string, limit int) ([]IETFPerson, int, error) {
	params := url.Values{}
	params.Set("name__icontains", query)
	params.Set("format", "json")
	params.Set("limit", fmt.Sprintf("%d", limit))
	endpoint := "https://datatracker.ietf.org/api/v1/person/person/?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("ietf person search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, 0, fmt.Errorf("ietf %d: %s", resp.StatusCode, string(body))
	}
	var raw ietfPersonRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, 0, err
	}
	people := []IETFPerson{}
	for _, p := range raw.Objects {
		people = append(people, IETFPerson{
			ID:         p.ID,
			Name:       p.Name,
			ASCII:      p.ASCII,
			Plain:      p.Plain,
			ProfileURL: fmt.Sprintf("https://datatracker.ietf.org/person/%d/", p.ID),
		})
	}
	return people, raw.Meta.TotalCount, nil
}

func ietfPersonDocs(ctx context.Context, client *http.Client, personID, limit int) ([]IETFDocAuthorship, int, error) {
	params := url.Values{}
	params.Set("person", fmt.Sprintf("%d", personID))
	params.Set("format", "json")
	params.Set("limit", fmt.Sprintf("%d", limit))
	endpoint := "https://datatracker.ietf.org/api/v1/doc/documentauthor/?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("ietf person docs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("ietf %d", resp.StatusCode)
	}
	var raw ietfAuthorshipRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, 0, err
	}
	auths := make([]IETFDocAuthorship, 0, len(raw.Objects))
	for _, o := range raw.Objects {
		// Document field is a URI like "/api/v1/doc/document/draft-ietf-pkix-cmc/"
		docName := strings.TrimSuffix(strings.TrimPrefix(o.Document, "/api/v1/doc/document/"), "/")
		// Email field is a URI like "/api/v1/person/email/jimsch@microsoft.com/"
		emailURI := o.Email
		emailVal := emailURI
		if strings.HasPrefix(emailURI, "/api/v1/person/email/") {
			emailVal = strings.TrimSuffix(strings.TrimPrefix(emailURI, "/api/v1/person/email/"), "/")
		}
		auths = append(auths, IETFDocAuthorship{
			DocName:     docName,
			Email:       emailVal,
			Affiliation: o.Affiliation,
			Country:     o.Country,
			AuthorOrder: o.Order,
		})
	}
	return auths, raw.Meta.TotalCount, nil
}

func ietfDocSearch(ctx context.Context, client *http.Client, query string, limit int) ([]IETFDocument, int, error) {
	// Try title__icontains; if `query` looks like a draft prefix, use name__startswith
	params := url.Values{}
	if strings.HasPrefix(query, "draft-") || strings.HasPrefix(query, "rfc") {
		params.Set("name__startswith", query)
	} else {
		params.Set("title__icontains", query)
	}
	params.Set("format", "json")
	params.Set("limit", fmt.Sprintf("%d", limit))
	endpoint := "https://datatracker.ietf.org/api/v1/doc/document/?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("ietf doc search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("ietf %d", resp.StatusCode)
	}
	var raw ietfDocRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, 0, err
	}
	docs := []IETFDocument{}
	for _, o := range raw.Objects {
		docs = append(docs, IETFDocument{
			Name:     o.Name,
			Title:    o.Title,
			Abstract: hfTruncate(o.Abstract, 400),
			Time:     o.Time,
			Pages:    o.Pages,
			StdLevel: shortenStdLevel(o.IntendedStdLevel),
			RFC:      o.RFC,
		})
	}
	return docs, raw.Meta.TotalCount, nil
}

func ietfDocLookup(ctx context.Context, client *http.Client, name string) (*IETFDocument, error) {
	endpoint := "https://datatracker.ietf.org/api/v1/doc/document/" + url.PathEscape(name) + "/?format=json"
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ietf doc lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ietf %d", resp.StatusCode)
	}
	var raw struct {
		Name             string `json:"name"`
		Title            string `json:"title"`
		Abstract         string `json:"abstract"`
		Time             string `json:"time"`
		Pages            int    `json:"pages"`
		IntendedStdLevel string `json:"intended_std_level"`
		RFC              string `json:"rfc"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return &IETFDocument{
		Name:     raw.Name,
		Title:    raw.Title,
		Abstract: hfTruncate(raw.Abstract, 600),
		Time:     raw.Time,
		Pages:    raw.Pages,
		StdLevel: shortenStdLevel(raw.IntendedStdLevel),
		RFC:      raw.RFC,
	}, nil
}

func shortenStdLevel(uri string) string {
	// "/api/v1/name/intendedstdlevelname/ps/" → "ps"
	uri = strings.TrimSuffix(uri, "/")
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[i+1:]
	}
	return uri
}

func buildIETFHighlights(o *IETFDataTrackerOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "person_search":
		hi = append(hi, fmt.Sprintf("%d IETF persons match '%s'", o.TotalResults, o.Query))
		for i, p := range o.People {
			if i >= 5 {
				break
			}
			hi = append(hi, fmt.Sprintf("  id=%d  %s  → %s", p.ID, p.Name, p.ProfileURL))
		}
	case "person_docs":
		hi = append(hi, fmt.Sprintf("✓ %d total IETF documents authored (returned %d)", o.TotalResults, len(o.Authorships)))
		if len(o.UniqueAffiliations) > 0 {
			affList := []string{}
			for _, a := range o.UniqueAffiliations[:min2(5, len(o.UniqueAffiliations))] {
				affList = append(affList, fmt.Sprintf("%s (%dx)", a.Affiliation, a.DocCount))
			}
			hi = append(hi, "🏛  affiliation history (from doc authorship — temporal employer trail): "+strings.Join(affList, " | "))
			if len(o.UniqueAffiliations) >= 2 {
				hi = append(hi, fmt.Sprintf("⚡ %d distinct employers across IETF history — career mobility signal", len(o.UniqueAffiliations)))
			}
		}
		if len(o.UniqueEmails) > 0 {
			emList := []string{}
			for _, e := range o.UniqueEmails[:min2(5, len(o.UniqueEmails))] {
				emList = append(emList, fmt.Sprintf("%s (%dx)", e.Email, e.DocCount))
			}
			hi = append(hi, "📧 email history: "+strings.Join(emList, " | "))
			if len(o.UniqueEmails) >= 3 {
				hi = append(hi, "⚡ multiple email addresses across docs — useful for email-based ER pivots (hibp / holehe / dehashed)")
			}
		}
		if len(o.Authorships) > 0 {
			recent := o.Authorships[0]
			hi = append(hi, fmt.Sprintf("most recent authorship: %s (affiliation=%s, email=%s)", recent.DocName, recent.Affiliation, recent.Email))
		}
	case "doc_search":
		hi = append(hi, fmt.Sprintf("%d documents match '%s' (returned %d)", o.TotalResults, o.Query, len(o.Documents)))
		// Identify working groups touched
		wgMap := map[string]int{}
		for _, d := range o.Documents {
			// "draft-ietf-tls-X" → wg=tls
			parts := strings.SplitN(d.Name, "-", 4)
			if len(parts) >= 3 && parts[0] == "draft" && parts[1] == "ietf" {
				wgMap[parts[2]]++
			}
		}
		if len(wgMap) > 0 {
			wgs := []string{}
			for w, c := range wgMap {
				wgs = append(wgs, fmt.Sprintf("%s (%d)", w, c))
			}
			sort.SliceStable(wgs, func(i, j int) bool { return wgs[i] < wgs[j] })
			hi = append(hi, "🛠 working groups represented: "+strings.Join(wgs, ", "))
		}
	case "doc_lookup":
		if len(o.Documents) > 0 {
			d := o.Documents[0]
			hi = append(hi, fmt.Sprintf("✓ %s — %s", d.Name, d.Title))
			if d.RFC != "" {
				hi = append(hi, "📜 published as RFC "+d.RFC)
			}
			if d.StdLevel != "" {
				hi = append(hi, "intended_std_level: "+d.StdLevel)
			}
			if d.Time != "" {
				hi = append(hi, "last updated: "+d.Time)
			}
		}
	}
	return hi
}
