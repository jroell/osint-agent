package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WikiTreeLookup wraps the WikiTree free public REST API
// (https://api.wikitree.com/api.php). WikiTree is a community-curated
// global family tree with ~30M+ profiles, complementary to FamilySearch
// (LDS) and Geni.
//
// Modes:
//   - "search"          : full-text search by name (FirstName + LastName)
//   - "profile"         : profile by WikiTree ID (LastName-NNN format)
//   - "ancestors"       : ancestor tree to a given depth
//   - "descendants"     : descendant tree to a given depth
//   - "relatives"       : parents + spouses + children + siblings of a profile
//
// Knowledge-graph: emits typed entities (kind: "person") with stable
// WikiTree IDs and role-edge attributes (parent_of, spouse_of, etc.).

type WTPerson struct {
	ID            int64  `json:"user_id,omitempty"`
	WikiTreeID    string `json:"wikitree_id"`
	Name          string `json:"name"`
	FirstName     string `json:"first_name,omitempty"`
	MiddleName    string `json:"middle_name,omitempty"`
	LastName      string `json:"last_name,omitempty"`
	BirthLocation string `json:"birth_location,omitempty"`
	BirthDate     string `json:"birth_date,omitempty"`
	DeathDate     string `json:"death_date,omitempty"`
	DeathLocation string `json:"death_location,omitempty"`
	Gender        string `json:"gender,omitempty"`
	URL           string `json:"url"`
}

type WTRelation struct {
	WikiTreeID string `json:"wikitree_id"`
	Role       string `json:"role"`
	Name       string `json:"name,omitempty"`
}

type WTEntity struct {
	Kind        string         `json:"kind"`
	WikiTreeID  string         `json:"wikitree_id"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type WikiTreeLookupOutput struct {
	Mode              string       `json:"mode"`
	Query             string       `json:"query,omitempty"`
	Returned          int          `json:"returned"`
	People            []WTPerson   `json:"people,omitempty"`
	Relations         []WTRelation `json:"relations,omitempty"`
	Entities          []WTEntity   `json:"entities"`
	HighlightFindings []string     `json:"highlight_findings"`
	Source            string       `json:"source"`
	TookMs            int64        `json:"tookMs"`
}

func WikiTreeLookup(ctx context.Context, input map[string]any) (*WikiTreeLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["wikitree_id"] != nil:
			mode = "profile"
		case input["first_name"] != nil || input["last_name"] != nil:
			mode = "search"
		default:
			return nil, fmt.Errorf("provide wikitree_id (profile/relatives/ancestors/descendants) or first_name+last_name (search)")
		}
	}
	out := &WikiTreeLookupOutput{Mode: mode, Source: "api.wikitree.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	post := func(form url.Values) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "POST",
			"https://api.wikitree.com/api.php",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("wikitree: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("wikitree HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "search":
		first, _ := input["first_name"].(string)
		last, _ := input["last_name"].(string)
		if last == "" {
			return nil, fmt.Errorf("input.last_name required for search")
		}
		out.Query = strings.TrimSpace(first + " " + last)
		form := url.Values{}
		form.Set("action", "searchPerson")
		form.Set("FirstName", first)
		form.Set("LastName", last)
		form.Set("fields", "Name,FirstName,LastNameAtBirth,LastNameCurrent,BirthDate,DeathDate,BirthLocation,DeathLocation,Gender,Id")
		form.Set("format", "json")
		body, err := post(form)
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("wikitree decode: %w", err)
		}
		for _, m := range arr {
			results, _ := m["matches"].([]any)
			for _, r := range results {
				p, _ := r.(map[string]any)
				if p == nil {
					continue
				}
				out.People = append(out.People, parseWTPerson(p))
			}
		}

	case "profile":
		id, _ := input["wikitree_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.wikitree_id required (e.g. 'Smith-1' format)")
		}
		out.Query = id
		form := url.Values{}
		form.Set("action", "getPerson")
		form.Set("key", id)
		form.Set("fields", "Name,FirstName,LastNameAtBirth,LastNameCurrent,BirthDate,DeathDate,BirthLocation,DeathLocation,Gender,Id")
		form.Set("format", "json")
		body, err := post(form)
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("wikitree decode: %w", err)
		}
		for _, m := range arr {
			if p, ok := m["person"].(map[string]any); ok {
				out.People = append(out.People, parseWTPerson(p))
			}
		}

	case "ancestors", "descendants":
		id, _ := input["wikitree_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.wikitree_id required")
		}
		out.Query = id
		depth := 3
		if d, ok := input["depth"].(float64); ok && d > 0 && d <= 10 {
			depth = int(d)
		}
		action := "getAncestors"
		if mode == "descendants" {
			action = "getDescendants"
		}
		form := url.Values{}
		form.Set("action", action)
		form.Set("key", id)
		form.Set("depth", fmt.Sprintf("%d", depth))
		form.Set("fields", "Name,FirstName,LastNameAtBirth,LastNameCurrent,BirthDate,DeathDate,BirthLocation,DeathLocation,Gender,Id")
		form.Set("format", "json")
		body, err := post(form)
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("wikitree decode: %w", err)
		}
		for _, m := range arr {
			ancestors, _ := m["ancestors"].([]any)
			for _, a := range ancestors {
				p, _ := a.(map[string]any)
				if p == nil {
					continue
				}
				out.People = append(out.People, parseWTPerson(p))
			}
			descendants, _ := m["descendants"].([]any)
			for _, d := range descendants {
				p, _ := d.(map[string]any)
				if p == nil {
					continue
				}
				out.People = append(out.People, parseWTPerson(p))
			}
		}

	case "relatives":
		id, _ := input["wikitree_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.wikitree_id required")
		}
		out.Query = id
		form := url.Values{}
		form.Set("action", "getRelatives")
		form.Set("keys", id)
		form.Set("getParents", "1")
		form.Set("getSpouses", "1")
		form.Set("getChildren", "1")
		form.Set("getSiblings", "1")
		form.Set("fields", "Name,FirstName,LastNameAtBirth,LastNameCurrent,BirthDate,DeathDate,BirthLocation,DeathLocation,Gender,Id")
		form.Set("format", "json")
		body, err := post(form)
		if err != nil {
			return nil, err
		}
		var arr []map[string]any
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("wikitree decode: %w", err)
		}
		for _, m := range arr {
			items, _ := m["items"].([]any)
			for _, it := range items {
				rec, _ := it.(map[string]any)
				if rec == nil {
					continue
				}
				if person, ok := rec["person"].(map[string]any); ok {
					seed := parseWTPerson(person)
					out.People = append(out.People, seed)
					for relRole, group := range map[string]string{"Parents": "parent", "Spouses": "spouse", "Children": "child", "Siblings": "sibling"} {
						if rels, ok := person[relRole].(map[string]any); ok {
							for _, rv := range rels {
								if pm, ok := rv.(map[string]any); ok {
									p := parseWTPerson(pm)
									out.People = append(out.People, p)
									out.Relations = append(out.Relations, WTRelation{
										WikiTreeID: p.WikiTreeID, Role: group + "_of:" + seed.WikiTreeID, Name: p.Name,
									})
								}
							}
						}
					}
				}
			}
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.People)
	out.Entities = wtBuildEntities(out)
	out.HighlightFindings = wtBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseWTPerson(m map[string]any) WTPerson {
	id := gtString(m, "Name") // WikiTree's "Name" field is the URL slug like "Smith-1"
	last := gtString(m, "LastNameCurrent")
	if last == "" {
		last = gtString(m, "LastNameAtBirth")
	}
	full := strings.TrimSpace(gtString(m, "FirstName") + " " + last)
	if full == "" {
		full = id
	}
	return WTPerson{
		ID:            int64(gtFloat(m, "Id")),
		WikiTreeID:    id,
		Name:          full,
		FirstName:     gtString(m, "FirstName"),
		MiddleName:    gtString(m, "MiddleName"),
		LastName:      last,
		BirthLocation: gtString(m, "BirthLocation"),
		BirthDate:     gtString(m, "BirthDate"),
		DeathDate:     gtString(m, "DeathDate"),
		DeathLocation: gtString(m, "DeathLocation"),
		Gender:        gtString(m, "Gender"),
		URL:           "https://www.wikitree.com/wiki/" + id,
	}
}

func wtBuildEntities(o *WikiTreeLookupOutput) []WTEntity {
	ents := []WTEntity{}
	seen := map[string]bool{}
	for _, p := range o.People {
		if p.WikiTreeID == "" || seen[p.WikiTreeID] {
			continue
		}
		seen[p.WikiTreeID] = true
		desc := p.BirthLocation
		if p.DeathLocation != "" {
			desc += " → " + p.DeathLocation
		}
		ents = append(ents, WTEntity{
			Kind: "person", WikiTreeID: p.WikiTreeID, Name: p.Name, URL: p.URL,
			Date: p.BirthDate, Description: desc,
			Attributes: map[string]any{
				"first_name":     p.FirstName,
				"last_name":      p.LastName,
				"birth_date":     p.BirthDate,
				"birth_location": p.BirthLocation,
				"death_date":     p.DeathDate,
				"death_location": p.DeathLocation,
				"gender":         p.Gender,
			},
		})
	}
	for _, r := range o.Relations {
		// add edge entities (kind: relationship) for ER edge ingestion
		ents = append(ents, WTEntity{
			Kind: "relationship", WikiTreeID: r.WikiTreeID, Name: r.Name,
			Attributes: map[string]any{"role": r.Role},
		})
	}
	return ents
}

func wtBuildHighlights(o *WikiTreeLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ wikitree %s: %d people, %d relations", o.Mode, len(o.People), len(o.Relations))}
	for i, p := range o.People {
		if i >= 8 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [%s] %s — %s", p.Name, p.WikiTreeID, p.BirthDate, p.URL))
	}
	for i, r := range o.Relations {
		if i >= 8 {
			break
		}
		hi = append(hi, fmt.Sprintf("    rel: %s (%s)", r.Role, r.Name))
	}
	return hi
}
