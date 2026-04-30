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

// RORName is one name variant for an organization.
type RORName struct {
	Value string   `json:"value"`
	Lang  string   `json:"lang,omitempty"`
	Types []string `json:"types,omitempty"` // ror_display | label | alias | acronym
}

// RORLink is one external link declared on an organization.
type RORLink struct {
	Type  string `json:"type"`
	Value string `json:"url"`
}

// RORLocation is one declared location for an organization.
type RORLocation struct {
	GeonameID  int     `json:"geoname_id,omitempty"`
	Name       string  `json:"name,omitempty"`
	Country    string  `json:"country,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	Lat        float64 `json:"lat,omitempty"`
	Lon        float64 `json:"lon,omitempty"`
}

// RORRelationship is one parent/child/related org link.
type RORRelationship struct {
	Type  string `json:"type"`            // parent | child | related | successor | predecessor
	Label string `json:"label"`
	ID    string `json:"id"`              // ROR URL
	HandleSlug string `json:"handle_slug,omitempty"`
}

// RORExternalID is one external identifier in another namespace.
type RORExternalID struct {
	Namespace string   `json:"namespace"` // wikidata | grid | isni | fundref | etc
	IDs       []string `json:"ids"`
	Preferred string   `json:"preferred,omitempty"`
}

// RORHit is one search result.
type RORHit struct {
	ROR_ID       string         `json:"ror_id"`
	HandleSlug   string         `json:"handle_slug"` // e.g. "00f54p054" (the trailing ROR id portion)
	Names        []RORName      `json:"names,omitempty"`
	DisplayName  string         `json:"display_name,omitempty"`
	Established  int            `json:"established,omitempty"`
	Status       string         `json:"status,omitempty"`
	Types        []string       `json:"types,omitempty"`
	Locations    []RORLocation  `json:"locations,omitempty"`
	Links        []RORLink      `json:"links,omitempty"`
	ExternalIDs  []RORExternalID `json:"external_ids,omitempty"`
}

// RORDetail is the full org record returned in lookup mode.
type RORDetail struct {
	RORHit
	Relationships []RORRelationship `json:"relationships,omitempty"`
	Admin struct {
		CreatedDate string `json:"created_date,omitempty"`
		LastModified string `json:"last_modified_date,omitempty"`
	} `json:"admin,omitempty"`
}

// RORSearchOutput is the response.
type RORSearchOutput struct {
	Mode             string     `json:"mode"`
	Query            string     `json:"query"`
	TotalResults     int        `json:"total_results"`
	Hits             []RORHit   `json:"hits,omitempty"`
	Detail           *RORDetail `json:"detail,omitempty"`
	HighlightFindings []string  `json:"highlight_findings"`
	Source           string     `json:"source"`
	TookMs           int64      `json:"tookMs"`
	Note             string     `json:"note,omitempty"`
}

// raw structures
type rorRawSearch struct {
	NumResults int               `json:"number_of_results"`
	Items      []json.RawMessage `json:"items"`
}

type rorRawOrg struct {
	ID             string                 `json:"id"`
	Established    int                    `json:"established"`
	Status         string                 `json:"status"`
	Types          []string               `json:"types"`
	Names          []map[string]any       `json:"names"`
	Links          []map[string]any       `json:"links"`
	Locations      []map[string]any       `json:"locations"`
	ExternalIDs    []map[string]any       `json:"external_ids"`
	Relationships  []map[string]any       `json:"relationships"`
	Admin          map[string]any         `json:"admin"`
}

// RorOrgLookup queries the public ROR (Research Organization Registry) API.
// Free, no auth. Two modes:
//
//   - "search" : fuzzy name → up to 20 candidate ROR IDs with location +
//                external IDs for disambiguation
//   - "lookup" : exact ROR ID (full URL or just the trailing slug like
//                "00f54p054") → full detail incl parent/child relationships
//                and external IDs in Wikidata / GRID / ISNI / Funder
//                Registry / OFR ID
//
// Why this matters for ER:
//   - ROR is the canonical institutional ID system used by Crossref's
//     funder/affiliation fields, OpenAlex affiliations, ORCID employment
//     records, NIH RePORTER organizations. Resolving a fuzzy name like
//     "Stanford" to "https://ror.org/00f54p054" lets you query the other
//     academic tools with HIGH-CONFIDENCE canonical identifiers instead
//     of name guessing.
//   - The relationships graph (parent/child/related/successor/predecessor)
//     reveals institutional hierarchy: querying Stanford returns 16+ child
//     labs/institutes, each with their own ROR ID — letting you build a
//     full organizational graph in one fan-out.
//   - External ID bridges to Wikidata + GRID + ISNI + Funder Registry mean
//     you can pivot from a ROR query into 4+ other knowledge bases.
func RorOrgLookup(ctx context.Context, input map[string]any) (*RORSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "search"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required (org name for 'search' mode, ROR ID for 'lookup' mode)")
	}
	limit := 20
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 50 {
		limit = int(v)
	}

	out := &RORSearchOutput{
		Mode:   mode,
		Query:  query,
		Source: "api.ror.org/v2",
	}
	start := time.Now()
	client := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "search":
		params := url.Values{}
		params.Set("query", query)
		endpoint := "https://api.ror.org/v2/organizations?" + params.Encode()
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Header.Set("User-Agent", "osint-agent/0.1")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ror search: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("ror %d: %s", resp.StatusCode, string(body))
		}
		var raw rorRawSearch
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, fmt.Errorf("ror decode: %w", err)
		}
		out.TotalResults = raw.NumResults
		for i, item := range raw.Items {
			if i >= limit {
				break
			}
			var rorOrg rorRawOrg
			if err := json.Unmarshal(item, &rorOrg); err != nil {
				continue
			}
			hit := materializeRORHit(&rorOrg)
			out.Hits = append(out.Hits, hit)
		}
	case "lookup":
		// Accept full URL or trailing slug
		slug := query
		if strings.Contains(slug, "ror.org/") {
			i := strings.Index(slug, "ror.org/")
			slug = slug[i+len("ror.org/"):]
			slug = strings.TrimSuffix(slug, "/")
		}
		endpoint := "https://api.ror.org/v2/organizations/" + url.PathEscape(slug)
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Header.Set("User-Agent", "osint-agent/0.1")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ror lookup: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 {
			out.Note = fmt.Sprintf("no ROR record for '%s'", slug)
			out.HighlightFindings = []string{out.Note}
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("ror %d: %s", resp.StatusCode, string(body))
		}
		var rorOrg rorRawOrg
		if err := json.NewDecoder(resp.Body).Decode(&rorOrg); err != nil {
			return nil, fmt.Errorf("ror lookup decode: %w", err)
		}
		hit := materializeRORHit(&rorOrg)
		detail := &RORDetail{RORHit: hit}
		// relationships
		for _, r := range rorOrg.Relationships {
			rt, _ := r["type"].(string)
			lbl, _ := r["label"].(string)
			id, _ := r["id"].(string)
			rel := RORRelationship{
				Type:  rt,
				Label: lbl,
				ID:    id,
			}
			if id != "" {
				if i := strings.LastIndex(id, "/"); i >= 0 {
					rel.HandleSlug = id[i+1:]
				}
			}
			detail.Relationships = append(detail.Relationships, rel)
		}
		// admin dates
		if rorOrg.Admin != nil {
			if cm, ok := rorOrg.Admin["created"].(map[string]any); ok {
				if d, ok := cm["date"].(string); ok {
					detail.Admin.CreatedDate = d
				}
			}
			if mm, ok := rorOrg.Admin["last_modified"].(map[string]any); ok {
				if d, ok := mm["date"].(string); ok {
					detail.Admin.LastModified = d
				}
			}
		}
		out.Detail = detail
		out.TotalResults = 1
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search, lookup", mode)
	}

	out.HighlightFindings = buildRORHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func materializeRORHit(o *rorRawOrg) RORHit {
	hit := RORHit{
		ROR_ID:      o.ID,
		Established: o.Established,
		Status:      o.Status,
		Types:       o.Types,
	}
	// trailing-slug handle (e.g. "00f54p054" from "https://ror.org/00f54p054")
	if i := strings.LastIndex(o.ID, "/"); i >= 0 {
		hit.HandleSlug = o.ID[i+1:]
	}
	// names
	for _, n := range o.Names {
		val, _ := n["value"].(string)
		lang, _ := n["lang"].(string)
		var types []string
		if t, ok := n["types"].([]any); ok {
			for _, x := range t {
				if s, ok := x.(string); ok {
					types = append(types, s)
				}
			}
		}
		rn := RORName{Value: val, Lang: lang, Types: types}
		hit.Names = append(hit.Names, rn)
		// Pick first ror_display name as primary display
		for _, t := range types {
			if t == "ror_display" && hit.DisplayName == "" {
				hit.DisplayName = val
			}
		}
	}
	// fallback display name
	if hit.DisplayName == "" && len(hit.Names) > 0 {
		hit.DisplayName = hit.Names[0].Value
	}
	// links
	for _, l := range o.Links {
		t, _ := l["type"].(string)
		v, _ := l["value"].(string)
		if v != "" {
			hit.Links = append(hit.Links, RORLink{Type: t, Value: v})
		}
	}
	// locations
	for _, loc := range o.Locations {
		gd, _ := loc["geonames_details"].(map[string]any)
		rl := RORLocation{}
		if gd != nil {
			if v, ok := gd["geoname_id"].(float64); ok {
				rl.GeonameID = int(v)
			}
			if s, ok := gd["name"].(string); ok {
				rl.Name = s
			}
			if s, ok := gd["country_name"].(string); ok {
				rl.Country = s
			}
			if s, ok := gd["country_code"].(string); ok {
				rl.CountryCode = s
			}
			if v, ok := gd["lat"].(float64); ok {
				rl.Lat = v
			}
			if v, ok := gd["lng"].(float64); ok {
				rl.Lon = v
			}
		}
		if rl.Name != "" || rl.Country != "" {
			hit.Locations = append(hit.Locations, rl)
		}
	}
	// external_ids
	for _, e := range o.ExternalIDs {
		ns, _ := e["type"].(string)
		preferred, _ := e["preferred"].(string)
		var ids []string
		if a, ok := e["all"].([]any); ok {
			for _, x := range a {
				if s, ok := x.(string); ok {
					ids = append(ids, s)
				}
			}
		}
		if ns != "" && len(ids) > 0 {
			hit.ExternalIDs = append(hit.ExternalIDs, RORExternalID{
				Namespace: ns,
				IDs:       ids,
				Preferred: preferred,
			})
		}
	}
	return hit
}

func buildRORHighlights(o *RORSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search":
		hi = append(hi, fmt.Sprintf("%d ROR matches for '%s' (returned %d)", o.TotalResults, o.Query, len(o.Hits)))
		if len(o.Hits) >= 2 {
			hi = append(hi, fmt.Sprintf("⚠️  %d distinct ROR records — likely separate orgs sharing a name fragment; disambiguate by location/established/external_id before drilling down", len(o.Hits)))
		}
		for i, h := range o.Hits {
			if i >= 5 {
				break
			}
			loc := ""
			if len(h.Locations) > 0 {
				loc = fmt.Sprintf("%s, %s", h.Locations[0].Name, h.Locations[0].CountryCode)
			}
			est := ""
			if h.Established > 0 {
				est = fmt.Sprintf(" est=%d", h.Established)
			}
			types := ""
			if len(h.Types) > 0 {
				types = " " + strings.Join(h.Types, "|")
			}
			hi = append(hi, fmt.Sprintf("  %s%s%s — %s — %s", h.DisplayName, est, types, loc, h.ROR_ID))
		}
	case "lookup":
		if o.Detail == nil {
			break
		}
		d := o.Detail
		hi = append(hi, fmt.Sprintf("✓ %s — ROR ID %s (slug %s)", d.DisplayName, d.ROR_ID, d.HandleSlug))
		if d.Established > 0 {
			hi = append(hi, fmt.Sprintf("📅 established %d", d.Established))
		}
		if len(d.Types) > 0 {
			hi = append(hi, "types: "+strings.Join(d.Types, ", "))
		}
		if len(d.Locations) > 0 {
			loc := d.Locations[0]
			hi = append(hi, fmt.Sprintf("📍 %s, %s (geoname=%d)", loc.Name, loc.Country, loc.GeonameID))
		}
		if len(d.ExternalIDs) > 0 {
			parts := []string{}
			for _, e := range d.ExternalIDs {
				preferred := e.Preferred
				if preferred == "" && len(e.IDs) > 0 {
					preferred = e.IDs[0]
				}
				parts = append(parts, fmt.Sprintf("%s=%s", e.Namespace, preferred))
			}
			hi = append(hi, fmt.Sprintf("🔗 external IDs (%d namespaces): %s", len(d.ExternalIDs), strings.Join(parts, " | ")))
		}
		if len(d.Links) > 0 {
			parts := []string{}
			for _, l := range d.Links {
				parts = append(parts, fmt.Sprintf("%s=%s", l.Type, l.Value))
			}
			hi = append(hi, "links: "+strings.Join(parts, " | "))
		}
		if len(d.Relationships) > 0 {
			byType := map[string]int{}
			for _, r := range d.Relationships {
				byType[r.Type]++
			}
			parts := []string{}
			for t, c := range byType {
				parts = append(parts, fmt.Sprintf("%s=%d", t, c))
			}
			hi = append(hi, fmt.Sprintf("🌳 %d relationships: %s", len(d.Relationships), strings.Join(parts, ", ")))
			// list a few children/parent
			for i, r := range d.Relationships {
				if i >= 4 {
					break
				}
				hi = append(hi, fmt.Sprintf("  %s → %s (%s)", r.Type, r.Label, r.HandleSlug))
			}
		}
		if d.Admin.CreatedDate != "" {
			hi = append(hi, "ROR record created "+d.Admin.CreatedDate)
		}
	}
	return hi
}
