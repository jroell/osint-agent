package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestERMoatEnvelope_AllTools is the connecting-the-dots verification:
// every PR-A/B/C tool must emit a top-level `entities[]` array whose
// elements have a `kind` discriminator. This is the contract that
// panel_entity_resolution and entity_link_finder rely on. If any new
// tool drops the envelope, this test fails.
func TestERMoatEnvelope_AllTools(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	type call struct {
		name  string
		tool  func(context.Context, map[string]any) (any, error)
		input map[string]any
	}
	calls := []call{
		{
			"tvmaze_lookup", func(c context.Context, in map[string]any) (any, error) { return TVMazeLookup(c, in) },
			map[string]any{"query": "Black Mirror"},
		},
		{
			"scryfall_lookup", func(c context.Context, in map[string]any) (any, error) { return ScryfallLookup(c, in) },
			map[string]any{"mode": "named", "name": "Black Lotus", "exact": true},
		},
		{
			"ygoprodeck_lookup", func(c context.Context, in map[string]any) (any, error) { return YGOProDeckLookup(c, in) },
			map[string]any{"mode": "name", "name": "Ally of Justice Catastor"},
		},
		{
			"chronicling_america_search", func(c context.Context, in map[string]any) (any, error) {
				return ChroniclingAmericaSearch(c, in)
			},
			map[string]any{"query": "Annie Besant Theosophy", "year_from": float64(1890), "year_to": float64(1920)},
		},
		{
			"loc_catalog_search", func(c context.Context, in map[string]any) (any, error) {
				return LOCCatalogSearch(c, in)
			},
			map[string]any{"query": "Walt Whitman"},
		},
		{
			"wikidata_sparql", func(c context.Context, in map[string]any) (any, error) {
				return WikidataSPARQL(c, in)
			},
			map[string]any{"query": `SELECT ?cat ?catLabel WHERE { ?cat wdt:P31 wd:Q146 . SERVICE wikibase:label { bd:serviceParam wikibase:language "en" } } LIMIT 3`},
		},
		{
			"openalex_author_graph", func(c context.Context, in map[string]any) (any, error) {
				return OpenAlexAuthorGraph(c, in)
			},
			map[string]any{"mode": "author_works", "author_id": "A5042120989"},
		},
		{
			"math_genealogy", func(c context.Context, in map[string]any) (any, error) {
				return MathGenealogy(c, in)
			},
			map[string]any{"mgp_id": "18230"},
		},
		// PR-D
		{
			"wikitree_lookup", func(c context.Context, in map[string]any) (any, error) {
				return WikiTreeLookup(c, in)
			},
			map[string]any{"first_name": "Henry", "last_name": "Lawson"},
		},
		{
			"adb_search", func(c context.Context, in map[string]any) (any, error) {
				return ADBSearch(c, in)
			},
			map[string]any{"mode": "biography", "slug": "lawson-henry-7118"},
		},
		// PR-E
		{
			"hathitrust_search", func(c context.Context, in map[string]any) (any, error) {
				return HathiTrustSearch(c, in)
			},
			map[string]any{"oclc": "644097"},
		},
		{
			"gallica_search", func(c context.Context, in map[string]any) (any, error) {
				return GallicaSearch(c, in)
			},
			map[string]any{"query": "Hugo"},
		},
		{
			"npgallery_search", func(c context.Context, in map[string]any) (any, error) {
				return NPGallerySearch(c, in)
			},
			map[string]any{"query": "Sutter Landing"},
		},
		{
			"ndl_japan_search", func(c context.Context, in map[string]any) (any, error) {
				return NDLJapanSearch(c, in)
			},
			map[string]any{"query": "夏目漱石"},
		},
		// PR-F
		{
			"pokemon_tcg_lookup", func(c context.Context, in map[string]any) (any, error) {
				return PokemonTCGLookup(c, in)
			},
			map[string]any{"query": "name:charizard"},
		},
		{
			"discogs_search", func(c context.Context, in map[string]any) (any, error) {
				return DiscogsSearch(c, in)
			},
			map[string]any{"query": "Reggiani Boris Vian"},
		},
		{
			"worldcat_search", func(c context.Context, in map[string]any) (any, error) {
				return WorldCatSearch(c, in)
			},
			map[string]any{"oclc": "644097"},
		},
		// GeoNames + Setlist.fm omitted — both rate-limited / key-gated for live tests.
		// PR-I
		{
			"inaturalist_search", func(c context.Context, in map[string]any) (any, error) {
				return INaturalistSearch(c, in)
			},
			map[string]any{"taxon_query": "Quercus alba"},
		},
		// PR-J
		{
			"eol_search", func(c context.Context, in map[string]any) (any, error) {
				return EOLSearch(c, in)
			},
			map[string]any{"query": "Quercus alba"},
		},
	}
	// ICIJ uses HTML scraping that's Cloudflare-fronted (HTTP 202); add to tolerant set.
	// Instagram, Browserbase, TikTok, Twitter154, YouTube-RapidAPI all key-gated; not in moat test.
	// GovInfo, AISHub, ADS-B Exchange paid all key-gated. ADS-B free is being rate-limited (451) so add tolerance.
	// TMDB only if key present
	if os.Getenv("TMDB_API_KEY") != "" {
		calls = append(calls, call{
			"tmdb_lookup", func(c context.Context, in map[string]any) (any, error) { return TMDBLookup(c, in) },
			map[string]any{"mode": "search_tv", "query": "Black Mirror"},
		})
	}
	if os.Getenv("TROVE_API_KEY") != "" {
		calls = append(calls, call{
			"trove_search", func(c context.Context, in map[string]any) (any, error) { return TroveSearch(c, in) },
			map[string]any{"query": "Annie Besant", "category": "newspaper"},
		})
	}
	if os.Getenv("FAMILYSEARCH_ACCESS_TOKEN") != "" {
		calls = append(calls, call{
			"familysearch_lookup", func(c context.Context, in map[string]any) (any, error) {
				return FamilySearchLookup(c, in)
			},
			map[string]any{"surname": "Smith", "given_name": "John"},
		})
	}

	// Tools that can rate-limit / refuse on Cloudflare grounds or have
	// flaky TLS — we just want to assert the envelope shape when they
	// DO succeed.
	tolerant := map[string]bool{
		"worldcat_search":    true,
		"hathitrust_search":  true,
		"npgallery_search":   true,
		"adb_search":         true,
		"eol_search":         true, // EOL occasionally returns 5xx
		"pokemon_tcg_lookup": true, // PokemonTCG API periodically times out
		"wikitree_lookup":    true, // WikiTree IP-throttles with 403
	}

	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.tool(ctx, tc.input)
			if err != nil {
				if tolerant[tc.name] {
					t.Logf("%s tolerable failure (Cloudflare/rate-limit): %v", tc.name, err)
					return
				}
				t.Fatalf("%s call failed: %v", tc.name, err)
			}
			// Marshal then re-unmarshal as map[string]any to inspect the envelope generically.
			b, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("%s marshal failed: %v", tc.name, err)
			}
			var generic map[string]any
			if err := json.Unmarshal(b, &generic); err != nil {
				t.Fatalf("%s unmarshal failed: %v", tc.name, err)
			}
			ents, ok := generic["entities"].([]any)
			if !ok {
				t.Fatalf("%s: missing top-level 'entities' array; got keys %v", tc.name, generic)
			}
			if len(ents) == 0 {
				t.Logf("%s: 0 entities (acceptable for empty-result queries)", tc.name)
				return
			}
			for i, e := range ents {
				m, ok := e.(map[string]any)
				if !ok {
					t.Errorf("%s entity[%d]: not an object", tc.name, i)
					continue
				}
				kind, _ := m["kind"].(string)
				if kind == "" {
					t.Errorf("%s entity[%d]: missing kind discriminator: %v", tc.name, i, m)
				}
			}
			t.Logf("%s ✓ %d entities, sample kind=%v", tc.name, len(ents),
				func() string {
					if first, ok := ents[0].(map[string]any); ok {
						return first["kind"].(string)
					}
					return "?"
				}())
		})
	}
}
