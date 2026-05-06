package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestGallicaSearch_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	out, err := GallicaSearch(ctx, map[string]any{"query": "Hugo"})
	if err != nil {
		t.Fatalf("GallicaSearch: %v", err)
	}
	if out.Returned == 0 {
		t.Logf("zero results for Hugo (acceptable for SRU peculiarities)")
	}
	t.Logf("Gallica search → %d items (total %d)", out.Returned, out.Total)
}

func TestGallicaSearch_UnknownMode(t *testing.T) {
	_, err := GallicaSearch(context.Background(), map[string]any{"mode": "bogus", "query": "x"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
