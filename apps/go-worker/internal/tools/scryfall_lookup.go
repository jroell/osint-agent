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

// ScryfallLookup wraps the Scryfall Magic: The Gathering card database.
// Free, no key. The canonical source for MTG card data: rules text,
// mana cost, set printings, prices, format legalities.
//
// Modes:
//   - "search"      : full-text card search (q=)
//   - "named"       : exact or fuzzy name match
//   - "card_by_id"  : Scryfall UUID
//   - "card_by_set" : set code + collector number
//
// Knowledge-graph: each card emits a typed entity (kind: "trading_card",
// game: "magic_the_gathering") with stable Scryfall+Oracle IDs.

type ScryfallCard struct {
	ID            string            `json:"scryfall_id"`
	OracleID      string            `json:"oracle_id,omitempty"`
	Name          string            `json:"name"`
	ManaCost      string            `json:"mana_cost,omitempty"`
	Cmc           float64           `json:"cmc,omitempty"`
	TypeLine      string            `json:"type_line,omitempty"`
	OracleText    string            `json:"oracle_text,omitempty"`
	Power         string            `json:"power,omitempty"`
	Toughness     string            `json:"toughness,omitempty"`
	Colors        []string          `json:"colors,omitempty"`
	ColorIdentity []string          `json:"color_identity,omitempty"`
	SetCode       string            `json:"set_code,omitempty"`
	SetName       string            `json:"set_name,omitempty"`
	Rarity        string            `json:"rarity,omitempty"`
	CollectorNum  string            `json:"collector_number,omitempty"`
	ReleasedAt    string            `json:"released_at,omitempty"`
	Reserved      bool              `json:"reserved,omitempty"`
	Legalities    map[string]string `json:"legalities,omitempty"`
	Prices        map[string]string `json:"prices,omitempty"`
	ImageURI      string            `json:"image_uri,omitempty"`
	ScryfallURL   string            `json:"scryfall_url,omitempty"`
}

type ScryfallEntity struct {
	Kind        string         `json:"kind"`
	Game        string         `json:"game"`
	ScryfallID  string         `json:"scryfall_id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type ScryfallLookupOutput struct {
	Mode              string           `json:"mode"`
	Query             string           `json:"query,omitempty"`
	Returned          int              `json:"returned"`
	Cards             []ScryfallCard   `json:"cards,omitempty"`
	Entities          []ScryfallEntity `json:"entities"`
	HasMore           bool             `json:"has_more,omitempty"`
	TotalCards        int              `json:"total_cards,omitempty"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source            string           `json:"source"`
	TookMs            int64            `json:"tookMs"`
}

func ScryfallLookup(ctx context.Context, input map[string]any) (*ScryfallLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["scryfall_id"] != nil:
			mode = "card_by_id"
		case input["set_code"] != nil && input["collector_number"] != nil:
			mode = "card_by_set"
		case input["name"] != nil:
			mode = "named"
		default:
			mode = "search"
		}
	}
	out := &ScryfallLookupOutput{Mode: mode, Source: "api.scryfall.com"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("scryfall: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("scryfall: not found (404)")
		}
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("scryfall HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required (Scryfall syntax e.g. 't:dragon mv<=4')")
		}
		out.Query = q
		params := url.Values{"q": []string{q}}
		if order, ok := input["order"].(string); ok && order != "" {
			params.Set("order", order)
		}
		body, err := get("https://api.scryfall.com/cards/search?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var sr struct {
			TotalCards int              `json:"total_cards"`
			HasMore    bool             `json:"has_more"`
			Data       []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(body, &sr); err != nil {
			return nil, fmt.Errorf("scryfall decode: %w", err)
		}
		out.TotalCards = sr.TotalCards
		out.HasMore = sr.HasMore
		for _, c := range sr.Data {
			out.Cards = append(out.Cards, parseScryfallCard(c))
		}
	case "named":
		nm, _ := input["name"].(string)
		if nm == "" {
			return nil, fmt.Errorf("input.name required")
		}
		out.Query = nm
		mode2 := "fuzzy"
		if exact, _ := input["exact"].(bool); exact {
			mode2 = "exact"
		}
		body, err := get(fmt.Sprintf("https://api.scryfall.com/cards/named?%s=%s", mode2, url.QueryEscape(nm)))
		if err != nil {
			return nil, err
		}
		var c map[string]any
		if err := json.Unmarshal(body, &c); err != nil {
			return nil, fmt.Errorf("scryfall decode: %w", err)
		}
		out.Cards = []ScryfallCard{parseScryfallCard(c)}
	case "card_by_id":
		id, _ := input["scryfall_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.scryfall_id required")
		}
		body, err := get("https://api.scryfall.com/cards/" + url.PathEscape(id))
		if err != nil {
			return nil, err
		}
		var c map[string]any
		if err := json.Unmarshal(body, &c); err != nil {
			return nil, fmt.Errorf("scryfall decode: %w", err)
		}
		out.Cards = []ScryfallCard{parseScryfallCard(c)}
	case "card_by_set":
		set, _ := input["set_code"].(string)
		num, _ := input["collector_number"].(string)
		if set == "" || num == "" {
			return nil, fmt.Errorf("input.set_code and input.collector_number required")
		}
		body, err := get(fmt.Sprintf("https://api.scryfall.com/cards/%s/%s", url.PathEscape(strings.ToLower(set)), url.PathEscape(num)))
		if err != nil {
			return nil, err
		}
		var c map[string]any
		if err := json.Unmarshal(body, &c); err != nil {
			return nil, fmt.Errorf("scryfall decode: %w", err)
		}
		out.Cards = []ScryfallCard{parseScryfallCard(c)}
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Cards)
	out.Entities = scryfallBuildEntities(out)
	out.HighlightFindings = scryfallBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseScryfallCard(m map[string]any) ScryfallCard {
	c := ScryfallCard{
		ID:           gtString(m, "id"),
		OracleID:     gtString(m, "oracle_id"),
		Name:         gtString(m, "name"),
		ManaCost:     gtString(m, "mana_cost"),
		Cmc:          gtFloat(m, "cmc"),
		TypeLine:     gtString(m, "type_line"),
		OracleText:   gtString(m, "oracle_text"),
		Power:        gtString(m, "power"),
		Toughness:    gtString(m, "toughness"),
		SetCode:      gtString(m, "set"),
		SetName:      gtString(m, "set_name"),
		Rarity:       gtString(m, "rarity"),
		CollectorNum: gtString(m, "collector_number"),
		ReleasedAt:   gtString(m, "released_at"),
		ScryfallURL:  gtString(m, "scryfall_uri"),
	}
	if v, ok := m["reserved"].(bool); ok {
		c.Reserved = v
	}
	if cs, ok := m["colors"].([]any); ok {
		for _, x := range cs {
			if s, ok := x.(string); ok {
				c.Colors = append(c.Colors, s)
			}
		}
	}
	if ci, ok := m["color_identity"].([]any); ok {
		for _, x := range ci {
			if s, ok := x.(string); ok {
				c.ColorIdentity = append(c.ColorIdentity, s)
			}
		}
	}
	if leg, ok := m["legalities"].(map[string]any); ok {
		c.Legalities = map[string]string{}
		for k, v := range leg {
			if s, ok := v.(string); ok {
				c.Legalities[k] = s
			}
		}
	}
	if pr, ok := m["prices"].(map[string]any); ok {
		c.Prices = map[string]string{}
		for k, v := range pr {
			if s, ok := v.(string); ok {
				c.Prices[k] = s
			}
		}
	}
	if im, ok := m["image_uris"].(map[string]any); ok {
		c.ImageURI = gtString(im, "normal")
	}
	return c
}

func scryfallBuildEntities(o *ScryfallLookupOutput) []ScryfallEntity {
	ents := []ScryfallEntity{}
	for _, c := range o.Cards {
		ents = append(ents, ScryfallEntity{
			Kind: "trading_card", Game: "magic_the_gathering", ScryfallID: c.ID, Name: c.Name,
			Description: c.OracleText,
			Attributes: map[string]any{
				"oracle_id":  c.OracleID,
				"type_line":  c.TypeLine,
				"mana_cost":  c.ManaCost,
				"cmc":        c.Cmc,
				"set":        c.SetCode + " — " + c.SetName,
				"rarity":     c.Rarity,
				"colors":     c.Colors,
				"power":      c.Power,
				"toughness":  c.Toughness,
				"released":   c.ReleasedAt,
				"reserved":   c.Reserved,
				"legalities": c.Legalities,
			},
		})
	}
	return ents
}

func scryfallBuildHighlights(o *ScryfallLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ scryfall %s: %d cards (total %d, has_more=%v)", o.Mode, o.Returned, o.TotalCards, o.HasMore)}
	for i, c := range o.Cards {
		if i >= 8 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [%s/%s] %s — %s",
			c.Name, c.SetCode, c.CollectorNum, c.ManaCost, c.TypeLine))
	}
	return hi
}
