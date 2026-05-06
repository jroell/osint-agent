package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// FamilySearchLookup wraps the LDS Church-maintained FamilySearch
// Family Tree API. Free with developer registration; OAuth-based.
//
// REQUIRES `FAMILYSEARCH_ACCESS_TOKEN` environment variable. Token can
// be acquired via OAuth at familysearch.org/developers.
//
// FamilySearch is the most comprehensive global genealogy database in
// existence (~1.5B persons indexed); complementary to WikiTree (smaller,
// open) and Geni (paid).
//
// Modes:
//   - "search"      : person search by name + birth/death year + place
//   - "person"      : fetch a person by FamilySearch Person ID (PID)
//   - "ancestry"    : ancestor pedigree from a PID
//   - "descendancy" : descendant tree from a PID
//
// Knowledge-graph: emits typed entities (kind: "person") with stable
// FamilySearch PIDs and edge attributes for ancestor/descendant chains.

type FSPerson struct {
	PID        string `json:"familysearch_pid"`
	Name       string `json:"name"`
	GivenNames string `json:"given,omitempty"`
	Surname    string `json:"surname,omitempty"`
	Gender     string `json:"gender,omitempty"`
	BirthDate  string `json:"birth_date,omitempty"`
	BirthPlace string `json:"birth_place,omitempty"`
	DeathDate  string `json:"death_date,omitempty"`
	DeathPlace string `json:"death_place,omitempty"`
	URL        string `json:"url"`
}

type FSEntity struct {
	Kind        string         `json:"kind"`
	PID         string         `json:"familysearch_pid"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type FamilySearchLookupOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query"`
	Returned          int        `json:"returned"`
	People            []FSPerson `json:"people,omitempty"`
	Entities          []FSEntity `json:"entities"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
}

func FamilySearchLookup(ctx context.Context, input map[string]any) (*FamilySearchLookupOutput, error) {
	token := os.Getenv("FAMILYSEARCH_ACCESS_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("FAMILYSEARCH_ACCESS_TOKEN not set; register at familysearch.org/developers and set the env var")
	}

	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["pid"] != nil:
			mode = "person"
		default:
			mode = "search"
		}
	}
	out := &FamilySearchLookupOutput{Mode: mode, Source: "api.familysearch.org"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(path string, params url.Values) ([]byte, error) {
		u := "https://api.familysearch.org" + path
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/x-gedcomx-v1+json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("familysearch: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode == 401 {
			return nil, fmt.Errorf("familysearch: unauthorized — token expired? (401)")
		}
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("familysearch: not found (404)")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("familysearch HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "search":
		given, _ := input["given_name"].(string)
		surname, _ := input["surname"].(string)
		bornYear, _ := input["born_year"].(string)
		diedYear, _ := input["died_year"].(string)
		bornPlace, _ := input["born_place"].(string)
		if given == "" && surname == "" {
			return nil, fmt.Errorf("input.given_name and/or input.surname required")
		}
		out.Query = strings.TrimSpace(given + " " + surname)
		params := url.Values{}
		var qparts []string
		if given != "" {
			qparts = append(qparts, "givenName:\""+given+"\"")
		}
		if surname != "" {
			qparts = append(qparts, "surname:\""+surname+"\"")
		}
		if bornYear != "" {
			qparts = append(qparts, "birthDate:"+bornYear)
		}
		if diedYear != "" {
			qparts = append(qparts, "deathDate:"+diedYear)
		}
		if bornPlace != "" {
			qparts = append(qparts, "birthPlace:\""+bornPlace+"\"")
		}
		params.Set("q", strings.Join(qparts, " "))
		params.Set("count", "20")
		body, err := get("/platform/tree/search", params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Entries []struct {
				ID      string                 `json:"id"`
				Title   string                 `json:"title"`
				Content map[string]interface{} `json:"content"`
				Score   float64                `json:"score"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("familysearch decode: %w", err)
		}
		for _, e := range resp.Entries {
			out.People = append(out.People, FSPerson{
				PID:  e.ID,
				Name: e.Title,
				URL:  "https://www.familysearch.org/tree/person/details/" + e.ID,
			})
		}

	case "person":
		pid, _ := input["pid"].(string)
		if pid == "" {
			return nil, fmt.Errorf("input.pid required (FamilySearch Person ID)")
		}
		out.Query = pid
		body, err := get("/platform/tree/persons/"+url.PathEscape(pid), url.Values{})
		if err != nil {
			return nil, err
		}
		var resp struct {
			Persons []map[string]any `json:"persons"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("familysearch decode: %w", err)
		}
		for _, p := range resp.Persons {
			out.People = append(out.People, parseFSPerson(p))
		}

	case "ancestry", "descendancy":
		pid, _ := input["pid"].(string)
		if pid == "" {
			return nil, fmt.Errorf("input.pid required")
		}
		out.Query = pid
		generations := 4
		if g, ok := input["generations"].(float64); ok && g > 0 && g <= 8 {
			generations = int(g)
		}
		path := "/platform/tree/ancestry"
		if mode == "descendancy" {
			path = "/platform/tree/descendancy"
		}
		params := url.Values{}
		params.Set("person", pid)
		params.Set("generations", fmt.Sprintf("%d", generations))
		body, err := get(path, params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Persons []map[string]any `json:"persons"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("familysearch decode: %w", err)
		}
		for _, p := range resp.Persons {
			out.People = append(out.People, parseFSPerson(p))
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.People)
	out.Entities = fsBuildEntities(out)
	out.HighlightFindings = fsBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseFSPerson(m map[string]any) FSPerson {
	id := gtString(m, "id")
	p := FSPerson{
		PID: id,
		URL: "https://www.familysearch.org/tree/person/details/" + id,
	}
	if names, ok := m["names"].([]any); ok && len(names) > 0 {
		if first, ok := names[0].(map[string]any); ok {
			if forms, ok := first["nameForms"].([]any); ok && len(forms) > 0 {
				if form, ok := forms[0].(map[string]any); ok {
					p.Name = gtString(form, "fullText")
				}
			}
		}
	}
	if facts, ok := m["facts"].([]any); ok {
		for _, f := range facts {
			fact, _ := f.(map[string]any)
			if fact == nil {
				continue
			}
			ftype := gtString(fact, "type")
			date := ""
			if d, ok := fact["date"].(map[string]any); ok {
				date = gtString(d, "original")
			}
			place := ""
			if pl, ok := fact["place"].(map[string]any); ok {
				place = gtString(pl, "original")
			}
			switch {
			case strings.HasSuffix(ftype, "Birth"):
				p.BirthDate = date
				p.BirthPlace = place
			case strings.HasSuffix(ftype, "Death"):
				p.DeathDate = date
				p.DeathPlace = place
			}
		}
	}
	if g, ok := m["gender"].(map[string]any); ok {
		p.Gender = gtString(g, "type")
	}
	return p
}

func fsBuildEntities(o *FamilySearchLookupOutput) []FSEntity {
	ents := []FSEntity{}
	for _, p := range o.People {
		ents = append(ents, FSEntity{
			Kind: "person", PID: p.PID, Name: p.Name, URL: p.URL, Date: p.BirthDate,
			Attributes: map[string]any{
				"given": p.GivenNames, "surname": p.Surname, "gender": p.Gender,
				"birth_date": p.BirthDate, "birth_place": p.BirthPlace,
				"death_date": p.DeathDate, "death_place": p.DeathPlace,
			},
		})
	}
	return ents
}

func fsBuildHighlights(o *FamilySearchLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ familysearch %s: %d people", o.Mode, o.Returned)}
	for i, p := range o.People {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [%s] %s — %s", p.Name, p.PID, p.BirthDate, p.URL))
	}
	return hi
}
