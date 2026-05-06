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

// iNaturalistSearch wraps the public iNaturalist API (api.inaturalist.org).
// Free, no key. Useful for biodiversity OSINT: species observations
// with timestamps + locations, taxon lookups, and observer profiles.
//
// Modes:
//   - "search_taxa"     : taxon search (returns species + ranks)
//   - "search_observations" : observations with optional taxon/place/date filters
//   - "user_profile"    : iNaturalist user record + activity stats
//
// Knowledge-graph: emits typed entities (kind: "taxon" | "observation" |
// "person") with stable iNat IDs.

type iNatTaxon struct {
	ID               int    `json:"taxon_id"`
	Name             string `json:"name"`
	CommonName       string `json:"common_name,omitempty"`
	Rank             string `json:"rank,omitempty"`
	URL              string `json:"inat_url"`
	WikipediaSummary string `json:"wikipedia_summary,omitempty"`
}

type iNatObservation struct {
	ID         int     `json:"observation_id"`
	TaxonID    int     `json:"taxon_id,omitempty"`
	TaxonName  string  `json:"taxon_name,omitempty"`
	ObservedOn string  `json:"observed_on,omitempty"`
	UserLogin  string  `json:"user_login,omitempty"`
	Lat        float64 `json:"latitude,omitempty"`
	Lon        float64 `json:"longitude,omitempty"`
	PlaceGuess string  `json:"place_guess,omitempty"`
	URL        string  `json:"inat_url"`
}

type iNatUser struct {
	ID                   int    `json:"user_id"`
	Login                string `json:"login"`
	Name                 string `json:"name,omitempty"`
	ObservationsCount    int    `json:"observations_count,omitempty"`
	IdentificationsCount int    `json:"identifications_count,omitempty"`
	URL                  string `json:"inat_url"`
}

type iNatEntity struct {
	Kind        string         `json:"kind"`
	INatID      int            `json:"inat_id"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Date        string         `json:"date,omitempty"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type iNaturalistSearchOutput struct {
	Mode              string            `json:"mode"`
	Query             string            `json:"query,omitempty"`
	Returned          int               `json:"returned"`
	Total             int               `json:"total,omitempty"`
	Taxa              []iNatTaxon       `json:"taxa,omitempty"`
	Observations      []iNatObservation `json:"observations,omitempty"`
	User              *iNatUser         `json:"user,omitempty"`
	Entities          []iNatEntity      `json:"entities"`
	HighlightFindings []string          `json:"highlight_findings"`
	Source            string            `json:"source"`
	TookMs            int64             `json:"tookMs"`
}

func INaturalistSearch(ctx context.Context, input map[string]any) (*iNaturalistSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["user_login"] != nil:
			mode = "user_profile"
		case input["taxon_id"] != nil || input["place_id"] != nil:
			mode = "search_observations"
		case input["taxon_query"] != nil || input["q"] != nil:
			mode = "search_taxa"
		default:
			mode = "search_observations"
		}
	}
	out := &iNaturalistSearchOutput{Mode: mode, Source: "api.inaturalist.org/v1"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(path string, params url.Values) ([]byte, error) {
		u := "https://api.inaturalist.org/v1" + path
		if encoded := params.Encode(); encoded != "" {
			u += "?" + encoded
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("inat: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("inat HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "search_taxa":
		q, _ := input["taxon_query"].(string)
		if q == "" {
			q, _ = input["q"].(string)
		}
		if q == "" {
			return nil, fmt.Errorf("input.taxon_query (or q) required")
		}
		out.Query = q
		params := url.Values{}
		params.Set("q", q)
		params.Set("per_page", "20")
		body, err := get("/taxa", params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			TotalResults int              `json:"total_results"`
			Results      []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("inat decode: %w", err)
		}
		out.Total = resp.TotalResults
		for _, r := range resp.Results {
			id := int(gtFloat(r, "id"))
			t := iNatTaxon{
				ID:               id,
				Name:             gtString(r, "name"),
				CommonName:       gtString(r, "preferred_common_name"),
				Rank:             gtString(r, "rank"),
				URL:              fmt.Sprintf("https://www.inaturalist.org/taxa/%d", id),
				WikipediaSummary: hfTruncate(gtString(r, "wikipedia_summary"), 400),
			}
			out.Taxa = append(out.Taxa, t)
		}
	case "search_observations":
		params := url.Values{}
		params.Set("per_page", "20")
		params.Set("order", "desc")
		params.Set("order_by", "observed_on")
		if t, ok := input["taxon_id"].(float64); ok && t > 0 {
			params.Set("taxon_id", fmt.Sprintf("%d", int(t)))
		}
		if t, ok := input["taxon_name"].(string); ok && t != "" {
			params.Set("taxon_name", t)
		}
		if p, ok := input["place_id"].(float64); ok && p > 0 {
			params.Set("place_id", fmt.Sprintf("%d", int(p)))
		}
		if y1, ok := input["year"].(float64); ok && y1 > 0 {
			params.Set("year", fmt.Sprintf("%d", int(y1)))
		}
		if m, ok := input["month"].(float64); ok && m > 0 {
			params.Set("month", fmt.Sprintf("%d", int(m)))
		}
		if u, ok := input["user_login"].(string); ok && u != "" {
			params.Set("user_login", u)
		}
		if q, ok := input["q"].(string); ok && q != "" {
			params.Set("q", q)
			out.Query = q
		}
		body, err := get("/observations", params)
		if err != nil {
			return nil, err
		}
		var resp struct {
			TotalResults int              `json:"total_results"`
			Results      []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("inat decode: %w", err)
		}
		out.Total = resp.TotalResults
		for _, r := range resp.Results {
			id := int(gtFloat(r, "id"))
			o2 := iNatObservation{
				ID:         id,
				ObservedOn: gtString(r, "observed_on_string"),
				PlaceGuess: gtString(r, "place_guess"),
				URL:        fmt.Sprintf("https://www.inaturalist.org/observations/%d", id),
			}
			if taxon, ok := r["taxon"].(map[string]any); ok {
				o2.TaxonID = int(gtFloat(taxon, "id"))
				o2.TaxonName = gtString(taxon, "name")
				if o2.TaxonName == "" {
					o2.TaxonName = gtString(taxon, "preferred_common_name")
				}
			}
			if user, ok := r["user"].(map[string]any); ok {
				o2.UserLogin = gtString(user, "login")
			}
			if geo, ok := r["geojson"].(map[string]any); ok {
				if coords, ok := geo["coordinates"].([]any); ok && len(coords) == 2 {
					o2.Lon = gtFloatAt(coords, 0)
					o2.Lat = gtFloatAt(coords, 1)
				}
			}
			out.Observations = append(out.Observations, o2)
		}
	case "user_profile":
		login, _ := input["user_login"].(string)
		if login == "" {
			return nil, fmt.Errorf("input.user_login required")
		}
		out.Query = login
		body, err := get("/users/"+url.PathEscape(login), url.Values{})
		if err != nil {
			return nil, err
		}
		var resp struct {
			Results []map[string]any `json:"results"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("inat decode: %w", err)
		}
		if len(resp.Results) > 0 {
			r := resp.Results[0]
			id := int(gtFloat(r, "id"))
			out.User = &iNatUser{
				ID: id, Login: gtString(r, "login"), Name: gtString(r, "name"),
				ObservationsCount:    int(gtFloat(r, "observations_count")),
				IdentificationsCount: int(gtFloat(r, "identifications_count")),
				URL:                  fmt.Sprintf("https://www.inaturalist.org/people/%d", id),
			}
		}
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Taxa) + len(out.Observations)
	if out.User != nil {
		out.Returned++
	}
	out.Entities = inatBuildEntities(out)
	out.HighlightFindings = inatBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func inatBuildEntities(o *iNaturalistSearchOutput) []iNatEntity {
	ents := []iNatEntity{}
	for _, t := range o.Taxa {
		ents = append(ents, iNatEntity{
			Kind: "taxon", INatID: t.ID, Name: t.Name, URL: t.URL,
			Description: t.CommonName,
			Attributes:  map[string]any{"rank": t.Rank, "wikipedia": t.WikipediaSummary},
		})
	}
	for _, ob := range o.Observations {
		ents = append(ents, iNatEntity{
			Kind: "observation", INatID: ob.ID, Name: ob.TaxonName, URL: ob.URL, Date: ob.ObservedOn,
			Description: ob.PlaceGuess,
			Attributes: map[string]any{
				"taxon_id":  ob.TaxonID,
				"user":      ob.UserLogin,
				"latitude":  ob.Lat,
				"longitude": ob.Lon,
				"place":     ob.PlaceGuess,
			},
		})
	}
	if u := o.User; u != nil {
		ents = append(ents, iNatEntity{
			Kind: "person", INatID: u.ID, Name: u.Login, URL: u.URL,
			Description: u.Name,
			Attributes: map[string]any{
				"observations":    u.ObservationsCount,
				"identifications": u.IdentificationsCount,
			},
		})
	}
	return ents
}

func inatBuildHighlights(o *iNaturalistSearchOutput) []string {
	hi := []string{fmt.Sprintf("✓ inaturalist %s: %d records (total %d)", o.Mode, o.Returned, o.Total)}
	for i, t := range o.Taxa {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s (%s) [#%d %s] — %s", t.Name, t.CommonName, t.ID, t.Rank, t.URL))
	}
	for i, ob := range o.Observations {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • #%d %s @ %s on %s by %s — %s",
			ob.ID, ob.TaxonName, ob.PlaceGuess, ob.ObservedOn, ob.UserLogin, ob.URL))
	}
	if u := o.User; u != nil {
		hi = append(hi, fmt.Sprintf("  • user @%s [%s] — %d observations, %d ids — %s",
			u.Login, u.Name, u.ObservationsCount, u.IdentificationsCount, u.URL))
	}
	return hi
}
