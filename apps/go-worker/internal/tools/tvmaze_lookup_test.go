package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestTVMazeLookup_LiveSearchShows exercises the search_shows path against
// the real TVMaze API (free, no key required). Skipped if SKIP_LIVE_TESTS=1.
func TestTVMazeLookup_LiveSearchShows(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := TVMazeLookup(ctx, map[string]any{
		"query": "Cougar Town",
	})
	if err != nil {
		t.Fatalf("TVMazeLookup search_shows: %v", err)
	}
	if out.Returned == 0 {
		t.Errorf("expected at least one show; got 0")
	}
	if len(out.Entities) == 0 {
		t.Errorf("expected at least one ER entity envelope")
	}
	found := false
	for _, e := range out.Entities {
		if e.Kind == "tv_show" && e.Title != "" && e.TVMazeID != 0 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected at least one tv_show entity with id+title")
	}
}

func TestTVMazeLookup_LiveEpisodeByNumber(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// TVMaze show id 305 = Black Mirror
	out, err := TVMazeLookup(ctx, map[string]any{
		"mode":    "episode_by_number",
		"show_id": float64(305),
		"season":  float64(4),
		"number":  float64(4),
	})
	if err != nil {
		t.Fatalf("TVMazeLookup episode_by_number: %v", err)
	}
	if out.Episode == nil {
		t.Fatal("expected episode")
	}
	if out.Episode.Season != 4 || out.Episode.Number != 4 {
		t.Errorf("expected S4E4; got S%dE%d", out.Episode.Season, out.Episode.Number)
	}
}

func TestTVMazeLookup_UnknownMode(t *testing.T) {
	_, err := TVMazeLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
