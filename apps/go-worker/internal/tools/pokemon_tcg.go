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

// PokemonTCGLookup wraps the Pokémon TCG API (api.pokemontcg.io).
// Free, optional POKEMONTCG_API_KEY for higher rate limits.
//
// Modes:
//   - "card_search" : full-text card search with structured filters
//   - "card_by_id"  : fetch one card by id (e.g. "swsh4-25")
//   - "set_list"    : list all sets
//
// Knowledge-graph: emits typed entities (kind: trading_card, game:
// pokemon) with stable Pokémon TCG IDs.

type PKCard struct {
	ID                 string   `json:"pokemon_tcg_id"`
	Name               string   `json:"name"`
	Supertype          string   `json:"supertype,omitempty"`
	Subtypes           []string `json:"subtypes,omitempty"`
	HP                 string   `json:"hp,omitempty"`
	Types              []string `json:"types,omitempty"`
	SetID              string   `json:"set_id,omitempty"`
	SetName            string   `json:"set_name,omitempty"`
	Rarity             string   `json:"rarity,omitempty"`
	Number             string   `json:"number,omitempty"`
	Artist             string   `json:"artist,omitempty"`
	NationalDexNumbers []int    `json:"national_pokedex_numbers,omitempty"`
	ImageURL           string   `json:"image_url,omitempty"`
}

type PKEntity struct {
	Kind        string         `json:"kind"`
	Game        string         `json:"game"`
	PKID        string         `json:"pokemon_tcg_id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type PokemonTCGLookupOutput struct {
	Mode              string           `json:"mode"`
	Query             string           `json:"query,omitempty"`
	Returned          int              `json:"returned"`
	Cards             []PKCard         `json:"cards,omitempty"`
	Sets              []map[string]any `json:"sets,omitempty"`
	Entities          []PKEntity       `json:"entities"`
	HighlightFindings []string         `json:"highlight_findings"`
	Source            string           `json:"source"`
	TookMs            int64            `json:"tookMs"`
}

func PokemonTCGLookup(ctx context.Context, input map[string]any) (*PokemonTCGLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["card_id"] != nil:
			mode = "card_by_id"
		case input["sets"] != nil:
			mode = "set_list"
		default:
			mode = "card_search"
		}
	}
	out := &PokemonTCGLookupOutput{Mode: mode, Source: "api.pokemontcg.io"}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	get := func(u string) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0")
		if key := os.Getenv("POKEMONTCG_API_KEY"); key != "" {
			req.Header.Set("X-Api-Key", key)
		}
		resp, err := cli.Do(req)
		if err != nil {
			return nil, fmt.Errorf("pokemontcg: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("pokemontcg HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
		}
		return body, nil
	}

	switch mode {
	case "card_search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required (Pokémon TCG syntax e.g. 'name:charizard')")
		}
		out.Query = q
		params := url.Values{}
		params.Set("q", q)
		params.Set("pageSize", "20")
		body, err := get("https://api.pokemontcg.io/v2/cards?" + params.Encode())
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("pokemontcg decode: %w", err)
		}
		for _, c := range resp.Data {
			out.Cards = append(out.Cards, parsePKCard(c))
		}
	case "card_by_id":
		id, _ := input["card_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.card_id required")
		}
		out.Query = id
		body, err := get("https://api.pokemontcg.io/v2/cards/" + url.PathEscape(id))
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("pokemontcg decode: %w", err)
		}
		out.Cards = []PKCard{parsePKCard(resp.Data)}
	case "set_list":
		body, err := get("https://api.pokemontcg.io/v2/sets")
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("pokemontcg decode: %w", err)
		}
		out.Sets = resp.Data
	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Cards) + len(out.Sets)
	out.Entities = pokemonBuildEntities(out)
	out.HighlightFindings = pokemonBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parsePKCard(m map[string]any) PKCard {
	c := PKCard{
		ID:        gtString(m, "id"),
		Name:      gtString(m, "name"),
		Supertype: gtString(m, "supertype"),
		HP:        gtString(m, "hp"),
		Rarity:    gtString(m, "rarity"),
		Number:    gtString(m, "number"),
		Artist:    gtString(m, "artist"),
	}
	if subs, ok := m["subtypes"].([]any); ok {
		for _, x := range subs {
			if s, ok := x.(string); ok {
				c.Subtypes = append(c.Subtypes, s)
			}
		}
	}
	if ts, ok := m["types"].([]any); ok {
		for _, x := range ts {
			if s, ok := x.(string); ok {
				c.Types = append(c.Types, s)
			}
		}
	}
	if dex, ok := m["nationalPokedexNumbers"].([]any); ok {
		for _, x := range dex {
			if n, ok := x.(float64); ok {
				c.NationalDexNumbers = append(c.NationalDexNumbers, int(n))
			}
		}
	}
	if set, ok := m["set"].(map[string]any); ok {
		c.SetID = gtString(set, "id")
		c.SetName = gtString(set, "name")
	}
	if im, ok := m["images"].(map[string]any); ok {
		c.ImageURL = gtString(im, "large")
	}
	return c
}

func pokemonBuildEntities(o *PokemonTCGLookupOutput) []PKEntity {
	ents := []PKEntity{}
	for _, c := range o.Cards {
		ents = append(ents, PKEntity{
			Kind: "trading_card", Game: "pokemon", PKID: c.ID, Name: c.Name,
			Description: fmt.Sprintf("%s (%s) — %s", c.Supertype, strings.Join(c.Subtypes, "/"), c.Rarity),
			Attributes: map[string]any{
				"types": c.Types, "hp": c.HP, "set_id": c.SetID, "set_name": c.SetName,
				"rarity": c.Rarity, "number": c.Number, "artist": c.Artist,
				"national_dex": c.NationalDexNumbers,
			},
		})
	}
	for _, s := range o.Sets {
		ents = append(ents, PKEntity{
			Kind: "card_set", Game: "pokemon",
			PKID: gtString(s, "id"), Name: gtString(s, "name"),
			Attributes: map[string]any{"series": gtString(s, "series"), "release": gtString(s, "releaseDate")},
		})
	}
	return ents
}

func pokemonBuildHighlights(o *PokemonTCGLookupOutput) []string {
	hi := []string{fmt.Sprintf("✓ pokemon-tcg %s: %d records", o.Mode, o.Returned)}
	for i, c := range o.Cards {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("  • %s [%s] %s — %s/%s (%s)",
			c.Name, c.ID, strings.Join(c.Types, ","), c.SetName, c.Number, c.Rarity))
	}
	return hi
}
