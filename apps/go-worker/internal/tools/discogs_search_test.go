package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestDiscogsSearch_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := DiscogsSearch(ctx, map[string]any{"query": "Reggiani Boris Vian"})
	if err != nil {
		t.Logf("Discogs returned (rate-limit/auth varies): %v", err)
		return
	}
	t.Logf("Discogs search → %d records (total %d)", out.Returned, out.Total)
}

func TestDiscogsSearch_UnknownMode(t *testing.T) {
	_, err := DiscogsSearch(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
