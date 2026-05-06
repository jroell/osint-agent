package tools

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

// TestDedupeTMDBEntities_QuantitativeImprovement is the proof-of-improvement
// test for iteration 7 of the /loop "make osint-agent quantitatively
// better" task.
//
// The defect: tmdbBuildEntities builds the entity envelope by walking
// Cast then Crew in two separate loops. When a person appears in both
// (very common — actor-directors like Tarantino, Affleck, Eastwood,
// Coogler, Greta Gerwig; producer-actors like Reese Witherspoon,
// Margot Robbie; cameo cinematographer-directors), they were emitted
// as TWO `kind=person` entities with the same `tmdb_id`. The
// connecting-the-dots ER engine then sees them as two distinct
// people, fragmenting downstream graph edges.
//
// The fix: dedupe by (Kind, TMDBID|IMDbID) at the end of
// tmdbBuildEntities. When duplicates collide, attribute maps are
// merged (preserving both role="cast" and role="crew"), so the
// merged entity carries the full picture without losing data.
//
// Quantitative metric: duplicate-entity rate after construction, on
// a synthetic-but-realistic input mimicking 10 cast members + 5 crew
// members where 3 people overlap (i.e., 3 actor-directors on the
// same title).
func TestDedupeTMDBEntities_QuantitativeImprovement(t *testing.T) {
	// Synthesize a realistic TV-episode credits roll with 3 overlaps.
	// IDs 100..109 are pure cast; IDs 200..201 are pure crew; IDs
	// 500/501/502 appear in BOTH (overlap).
	cast := []TMDBCast{
		{ID: 100, Name: "Pure Cast 1", Character: "Alice"},
		{ID: 101, Name: "Pure Cast 2", Character: "Bob"},
		{ID: 102, Name: "Pure Cast 3", Character: "Carol"},
		{ID: 103, Name: "Pure Cast 4", Character: "Dave"},
		{ID: 104, Name: "Pure Cast 5", Character: "Eve"},
		{ID: 105, Name: "Pure Cast 6", Character: "Frank"},
		{ID: 106, Name: "Pure Cast 7", Character: "Grace"},
		{ID: 500, Name: "Quentin Tarantino", Character: "Mr. Brown"}, // also crew
		{ID: 501, Name: "Greta Gerwig", Character: "Self"},           // also crew
		{ID: 502, Name: "Ryan Coogler", Character: "Cameo"},          // also crew
	}
	crew := []TMDBCrew{
		{ID: 200, Name: "Pure Crew 1", Job: "DP", Department: "Camera"},
		{ID: 201, Name: "Pure Crew 2", Job: "Editor", Department: "Editing"},
		{ID: 500, Name: "Quentin Tarantino", Job: "Director", Department: "Directing"},
		{ID: 501, Name: "Greta Gerwig", Job: "Director", Department: "Directing"},
		{ID: 502, Name: "Ryan Coogler", Job: "Director", Department: "Directing"},
	}
	out := &TMDBLookupOutput{Cast: cast, Crew: crew}

	// --- BEFORE: count entities WITHOUT the dedup pass ---
	beforeEnts := buildTMDBPersonEntitiesNoDedup(out)
	beforeCount := len(beforeEnts)
	beforeDups := countDuplicateKindID(beforeEnts)

	// --- AFTER: count entities WITH the dedup pass (current code path) ---
	afterEnts := tmdbBuildEntities(out)
	afterCount := len(afterEnts)
	afterDups := countDuplicateKindID(afterEnts)

	// Hard expectations from the synthetic fixture:
	// - 10 cast + 5 crew = 15 raw entities
	// - 3 overlaps → 12 distinct people
	expectedBeforeCount := 15
	expectedAfterCount := 12
	expectedBeforeDups := 3 // 3 person-IDs collide
	expectedAfterDups := 0

	dedupRate := float64(beforeCount-afterCount) / float64(beforeCount) * 100

	t.Logf("TMDB cast/crew dedup on synthetic fixture (10 cast + 5 crew with 3 overlaps):")
	t.Logf("  before: %d entities, %d duplicate (kind,id) pairs", beforeCount, beforeDups)
	t.Logf("  after:  %d entities, %d duplicate (kind,id) pairs", afterCount, afterDups)
	t.Logf("  dedup rate: %.1f%% (%d duplicates eliminated)", dedupRate, beforeCount-afterCount)

	if beforeCount != expectedBeforeCount {
		t.Errorf("before count = %d; want %d", beforeCount, expectedBeforeCount)
	}
	if beforeDups != expectedBeforeDups {
		t.Errorf("before dup count = %d; want %d", beforeDups, expectedBeforeDups)
	}
	if afterCount != expectedAfterCount {
		t.Errorf("after count = %d; want %d", afterCount, expectedAfterCount)
	}
	if afterDups != expectedAfterDups {
		t.Errorf("after dup count = %d; want 0", afterDups)
	}
	if dedupRate < 19 {
		t.Errorf("dedup rate %.1f%% — expected ≥19%% (3 dups / 15 = 20%%)", dedupRate)
	}

	// Spot-check: Quentin Tarantino (id 500) merged entity must carry
	// BOTH role="cast" and role="crew" so downstream code can see he
	// was both. This is the property the dedup-WITH-merge offers over
	// dedup-with-drop.
	var tarantino *TMDBEntity
	for i := range afterEnts {
		if afterEnts[i].TMDBID == 500 {
			tarantino = &afterEnts[i]
			break
		}
	}
	if tarantino == nil {
		t.Fatal("merged entity for tmdb_id=500 missing")
	}
	roles := extractRoleAttr(tarantino.Attributes["role"])
	sort.Strings(roles)
	want := []string{"cast", "crew"}
	if !reflect.DeepEqual(roles, want) {
		t.Errorf("merged Tarantino role attrs = %v; want %v", roles, want)
	}
	// Job and character should both be preserved
	if tarantino.Attributes["character"] != "Mr. Brown" {
		t.Errorf("merged Tarantino lost character: got %v", tarantino.Attributes["character"])
	}
	if tarantino.Attributes["job"] != "Director" {
		t.Errorf("merged Tarantino lost job: got %v", tarantino.Attributes["job"])
	}
}

// TestDedupeTMDBEntities_NoOpOnEmptyIDs verifies entities with no
// identifier are passed through unchanged — we never want to dedup
// "anonymous" entities with each other.
func TestDedupeTMDBEntities_NoOpOnEmptyIDs(t *testing.T) {
	in := []TMDBEntity{
		{Kind: "person", Name: "A", Attributes: map[string]any{"role": "cast"}},
		{Kind: "person", Name: "B", Attributes: map[string]any{"role": "cast"}},
		{Kind: "person", Name: "C", Attributes: map[string]any{"role": "cast"}},
	}
	out := dedupeTMDBEntitiesByKindID(in)
	if len(out) != 3 {
		t.Errorf("zero-ID entities should pass through; got %d (want 3)", len(out))
	}
}

// --- helpers used only by these tests ---

// buildTMDBPersonEntitiesNoDedup reproduces the pre-fix logic
// (raw-append from Cast and Crew, no dedup) so the test can compute
// the BEFORE count numerically.
func buildTMDBPersonEntitiesNoDedup(o *TMDBLookupOutput) []TMDBEntity {
	ents := []TMDBEntity{}
	for _, c := range o.Cast {
		ents = append(ents, TMDBEntity{
			Kind: "person", TMDBID: c.ID, Name: c.Name,
			Attributes: map[string]any{"role": "cast", "character": c.Character, "order": c.Order},
		})
	}
	for _, c := range o.Crew {
		ents = append(ents, TMDBEntity{
			Kind: "person", TMDBID: c.ID, Name: c.Name,
			Attributes: map[string]any{"role": "crew", "job": c.Job, "department": c.Department},
		})
	}
	return ents
}

func countDuplicateKindID(ents []TMDBEntity) int {
	seen := map[string]int{}
	for _, e := range ents {
		if e.TMDBID == 0 && e.IMDbID == "" {
			continue
		}
		key := fmt.Sprintf("%s:%d:%s", e.Kind, e.TMDBID, e.IMDbID)
		seen[key]++
	}
	dups := 0
	for _, n := range seen {
		if n > 1 {
			dups += n - 1
		}
	}
	return dups
}

func extractRoleAttr(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, y := range x {
			if s, ok := y.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
