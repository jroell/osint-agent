package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestScryfallLookup_LiveNamed hits the real Scryfall API (no key required).
func TestScryfallLookup_LiveNamed(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := ScryfallLookup(ctx, map[string]any{
		"mode":  "named",
		"name":  "Black Lotus",
		"exact": true,
	})
	if err != nil {
		t.Fatalf("ScryfallLookup named: %v", err)
	}
	if out.Returned != 1 {
		t.Errorf("expected exactly 1 card; got %d", out.Returned)
	}
	if len(out.Entities) == 0 {
		t.Fatal("expected entity envelope")
	}
	if out.Entities[0].Game != "magic_the_gathering" {
		t.Errorf("expected game magic_the_gathering; got %s", out.Entities[0].Game)
	}
}

func TestScryfallLookup_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := ScryfallLookup(ctx, map[string]any{
		"mode":  "search",
		"query": "t:dragon mv<=4 c:r",
	})
	if err != nil {
		t.Fatalf("ScryfallLookup search: %v", err)
	}
	if out.Returned == 0 {
		t.Errorf("expected results; got 0")
	}
}

func TestScryfallLookup_UnknownMode(t *testing.T) {
	_, err := ScryfallLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
