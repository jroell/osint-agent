package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPokemonTCGLookup_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := PokemonTCGLookup(ctx, map[string]any{"query": "name:charizard"})
	if err != nil {
		t.Fatalf("PokemonTCGLookup: %v", err)
	}
	if out.Returned == 0 {
		t.Errorf("expected charizard results; got 0")
	}
}

func TestPokemonTCGLookup_UnknownMode(t *testing.T) {
	_, err := PokemonTCGLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
