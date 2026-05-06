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

// YGOProDeckLookup wraps the YGOPRODeck Yu-Gi-Oh! card database. Free, no key.
// Authoritative for Yu-Gi-Oh! TCG/OCG cards: ATK/DEF, level/rank/link rating,
// archetype, set printings, banlist status, lore.
//
// Modes:
//   - "name"      : exact card-name lookup
//   - "fname"     : fuzzy name search ('contains' semantics)
//   - "id"        : YGO numeric ID
//   - "archetype" : list cards in an archetype (e.g. "Ally of Justice")
//
// Knowledge-graph: each card emits a typed entity (kind: "trading_card",
// game: "yugioh") with stable YGO ID.

type YGOCard struct {
	ID        int               `json:"ygo_id"`
	Name      string            `json:"name"`
	Type      string            `json:"type,omitempty"`
	FrameType string            `json:"frame_type,omitempty"`
	Desc      string            `json:"description,omitempty"`
	Atk       int               `json:"atk,omitempty"`
	Def       int               `json:"def,omitempty"`
	Level     int               `json:"level,omitempty"`
	Race      string            `json:"race,omitempty"`
	Attribute string            `json:"attribute,omitempty"`
	Archetype string            `json:"archetype,omitempty"`
	LinkValue int               `json:"link_value,omitempty"`
	Sets      []YGOSet          `json:"sets,omitempty"`
	Banlist   map[string]string `json:"banlist,omitempty"` // tcg/ocg/goat → status
	ImageURL  string            `json:"image_url,omitempty"`
}

type YGOSet struct {
	SetName string `json:"set_name"`
	SetCode string `json:"set_code,omitempty"`
	Rarity  string `json:"rarity,omitempty"`
	Price   string `json:"price,omitempty"`
}

type YGOEntity struct {
	Kind        string         `json:"kind"`
	Game        string         `json:"game"`
	YGOID       int            `json:"ygo_id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type YGOProDeckLookupOutput struct {
	Mode              string      `json:"mode"`
	Query             string      `json:"query,omitempty"`
	Returned          int         `json:"returned"`
	Cards             []YGOCard   `json:"cards,omitempty"`
	Entities          []YGOEntity `json:"entities"`
	HighlightFindings []string    `json:"highlight_findings"`
	Source            string      `json:"source"`
	TookMs            int64       `json:"tookMs"`
}

func YGOProDeckLookup(ctx context.Context, input map[string]any) (*YGOProDeckLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["id"] != nil:
			mode = "id"
		case input["archetype"] != nil:
			mode = "archetype"
		case input["name"] != nil:
			mode = "name"
		default:
			mode = "fname"
		}
	}
	out := &YGOProDeckLookupOutput{Mode: mode, Source: "db.ygoprodeck.com/api/v7"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	params := url.Values{}
	switch mode {
	case "name":
		v, _ := input["name"].(string)
		if v == "" {
			return nil, fmt.Errorf("input.name required")
		}
		out.Query = v
		params.Set("name", v)
	case "fname":
		v, _ := input["query"].(string)
		if v == "" {
			v, _ = input["fname"].(string)
		}
		if v == "" {
			return nil, fmt.Errorf("input.query (or fname) required")
		}
		out.Query = v
		params.Set("fname", v)
	case "id":
		id := tmdbIntID(input, "id")
		if id == 0 {
			return nil, fmt.Errorf("input.id required")
		}
		out.Query = fmt.Sprintf("%d", id)
		params.Set("id", out.Query)
	case "archetype":
		v, _ := input["archetype"].(string)
		if v == "" {
			return nil, fmt.Errorf("input.archetype required")
		}
		out.Query = v
		params.Set("archetype", v)
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	u := "https://db.ygoprodeck.com/api/v7/cardinfo.php?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "osint-agent/1.0")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ygoprodeck: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 400 {
		// YGOPRODeck returns 400 with JSON {"error": "No card matching..."}
		var errResp map[string]any
		if json.Unmarshal(body, &errResp) == nil {
			if e, ok := errResp["error"].(string); ok {
				return nil, fmt.Errorf("ygoprodeck: %s", e)
			}
		}
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ygoprodeck HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	var wrap struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return nil, fmt.Errorf("ygoprodeck decode: %w", err)
	}
	for _, m := range wrap.Data {
		out.Cards = append(out.Cards, parseYGOCard(m))
	}
	out.Returned = len(out.Cards)
	out.Entities = ygoBuildEntities(out)
	out.HighlightFindings = ygoBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseYGOCard(m map[string]any) YGOCard {
	c := YGOCard{
		ID:        int(gtFloat(m, "id")),
		Name:      gtString(m, "name"),
		Type:      gtString(m, "type"),
		FrameType: gtString(m, "frameType"),
		Desc:      gtString(m, "desc"),
		Atk:       int(gtFloat(m, "atk")),
		Def:       int(gtFloat(m, "def")),
		Level:     int(gtFloat(m, "level")),
		Race:      gtString(m, "race"),
		Attribute: gtString(m, "attribute"),
		Archetype: gtString(m, "archetype"),
		LinkValue: int(gtFloat(m, "linkval")),
	}
	if sets, ok := m["card_sets"].([]any); ok {
		for _, x := range sets {
			rec, _ := x.(map[string]any)
			if rec == nil {
				continue
			}
			c.Sets = append(c.Sets, YGOSet{
				SetName: gtString(rec, "set_name"),
				SetCode: gtString(rec, "set_code"),
				Rarity:  gtString(rec, "set_rarity"),
				Price:   gtString(rec, "set_price"),
			})
		}
	}
	if bl, ok := m["banlist_info"].(map[string]any); ok {
		c.Banlist = map[string]string{}
		for k, v := range bl {
			if s, ok := v.(string); ok {
				c.Banlist[k] = s
			}
		}
	}
	if imgs, ok := m["card_images"].([]any); ok && len(imgs) > 0 {
		if first, ok := imgs[0].(map[string]any); ok {
			c.ImageURL = gtString(first, "image_url")
		}
	}
	return c
}

func ygoBuildEntities(o *YGOProDeckLookupOutput) []YGOEntity {
	ents := []YGOEntity{}
	for _, c := range o.Cards {
		ents = append(ents, YGOEntity{
			Kind: "trading_card", Game: "yugioh", YGOID: c.ID, Name: c.Name,
			Description: c.Desc,
			Attributes: map[string]any{
				"type":       c.Type,
				"frame":      c.FrameType,
				"attribute":  c.Attribute,
				"race":       c.Race,
				"level":      c.Level,
				"link_val":   c.LinkValue,
				"atk":        c.Atk,
				"def":        c.Def,
				"archetype":  c.Archetype,
				"banlist":    c.Banlist,
				"sets_count": len(c.Sets),
			},
		})
	}
	return ents
}

func ygoBuildHighlights(o *YGOProDeckLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ ygoprodeck %s: %d cards", o.Mode, o.Returned)}
	for i, c := range o.Cards {
		if i >= 6 {
			break
		}
		stats := ""
		if c.Atk > 0 || c.Def > 0 {
			stats = fmt.Sprintf("ATK/%d DEF/%d ", c.Atk, c.Def)
		}
		hi = append(hi, fmt.Sprintf("  • #%d %s [%s] %s%s — %s",
			c.ID, c.Name, c.Type, stats, c.Attribute, c.Archetype))
	}
	return hi
}
