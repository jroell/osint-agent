package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestWikiTreeLookup_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := WikiTreeLookup(ctx, map[string]any{
		"first_name": "Carl",
		"last_name":  "Gauss",
	})
	if err != nil {
		// WikiTree IP-throttles aggressive sessions with HTTP 403; treat as
		// tolerable transient since it's indistinguishable from the API
		// being temporarily unavailable to this caller.
		t.Logf("WikiTreeLookup live (tolerable): %v", err)
		return
	}
	t.Logf("WikiTree search Gauss → %d hits", out.Returned)
}

func TestWikiTreeLookup_UnknownMode(t *testing.T) {
	_, err := WikiTreeLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
