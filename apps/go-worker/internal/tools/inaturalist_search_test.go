package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestINaturalistSearch_LiveSearchTaxa(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := INaturalistSearch(ctx, map[string]any{
		"taxon_query": "Quercus alba",
	})
	if err != nil {
		t.Fatalf("INaturalistSearch search_taxa: %v", err)
	}
	if out.Returned == 0 {
		t.Errorf("expected results for white oak; got 0")
	}
	if len(out.Entities) == 0 {
		t.Errorf("expected entity envelope")
	}
}

func TestINaturalistSearch_UnknownMode(t *testing.T) {
	_, err := INaturalistSearch(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
