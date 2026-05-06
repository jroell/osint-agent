package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestTMDBLookup_LiveSearchTV exercises the search_tv path against the real
// TMDB API. Skipped if TMDB_API_KEY is not set.
func TestTMDBLookup_LiveSearchTV(t *testing.T) {
	if os.Getenv("TMDB_API_KEY") == "" {
		t.Skip("TMDB_API_KEY not set; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := TMDBLookup(ctx, map[string]any{
		"mode":  "search_tv",
		"query": "Black Mirror",
	})
	if err != nil {
		t.Fatalf("TMDBLookup search_tv: %v", err)
	}
	if out.Returned == 0 {
		t.Errorf("expected at least one TV result; got 0")
	}
	if len(out.Entities) == 0 {
		t.Errorf("expected at least one ER entity envelope")
	}
	for _, e := range out.Entities {
		if e.Kind != "tv_show" {
			continue
		}
		if e.TMDBID == 0 || e.Title == "" {
			t.Errorf("tv_show entity missing identifier or title: %+v", e)
		}
	}
}

func TestTMDBLookup_NoAPIKey(t *testing.T) {
	setTestEnv(t, "TMDB_API_KEY", "")

	_, err := TMDBLookup(context.Background(), map[string]any{
		"mode":  "search_tv",
		"query": "x",
	})
	if err == nil {
		t.Fatal("expected error when TMDB_API_KEY is unset")
	}
}

func TestTMDBLookup_UnknownMode(t *testing.T) {
	if os.Getenv("TMDB_API_KEY") == "" {
		setTestEnv(t, "TMDB_API_KEY", "x")
	}
	_, err := TMDBLookup(context.Background(), map[string]any{"mode": "bogus", "query": "x"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
