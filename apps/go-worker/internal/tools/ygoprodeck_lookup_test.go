package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestYGOProDeckLookup_LiveName hits the real YGOPRODeck API (no key required).
// Verifies the iter-75 answer 'Ally of Justice Catastor' resolves cleanly.
func TestYGOProDeckLookup_LiveName(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := YGOProDeckLookup(ctx, map[string]any{
		"mode": "name",
		"name": "Ally of Justice Catastor",
	})
	if err != nil {
		t.Fatalf("YGOProDeckLookup name: %v", err)
	}
	if out.Returned != 1 {
		t.Fatalf("expected exactly 1 card; got %d", out.Returned)
	}
	c := out.Cards[0]
	if c.Atk != 2200 {
		t.Errorf("expected ATK 2200 for Ally of Justice Catastor; got %d", c.Atk)
	}
	if c.Archetype != "Ally of Justice" {
		t.Errorf("expected archetype 'Ally of Justice'; got %q", c.Archetype)
	}
	if len(out.Entities) == 0 || out.Entities[0].Game != "yugioh" {
		t.Errorf("expected yugioh entity envelope")
	}
}

func TestYGOProDeckLookup_LiveArchetype(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := YGOProDeckLookup(ctx, map[string]any{
		"mode":      "archetype",
		"archetype": "Ally of Justice",
	})
	if err != nil {
		t.Fatalf("YGOProDeckLookup archetype: %v", err)
	}
	if out.Returned < 5 {
		t.Errorf("expected several Ally of Justice cards; got %d", out.Returned)
	}
}

func TestYGOProDeckLookup_UnknownMode(t *testing.T) {
	_, err := YGOProDeckLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
