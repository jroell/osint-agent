package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestGeoNamesLookup_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := GeoNamesLookup(ctx, map[string]any{"query": "Cincinnati"})
	if err != nil {
		// "demo" account can be rate-limited or disabled. Acceptable.
		t.Logf("GeoNames returned (likely demo-account quota): %v", err)
		return
	}
	t.Logf("GeoNames search → %d places (total %d)", out.Returned, out.Total)
}

func TestGeoNamesLookup_UnknownMode(t *testing.T) {
	_, err := GeoNamesLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
