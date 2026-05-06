package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNPGallerySearch_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	out, err := NPGallerySearch(ctx, map[string]any{"query": "Sutter Landing"})
	if err != nil {
		t.Logf("NPGallerySearch returned error (acceptable; site varies): %v", err)
		return
	}
	t.Logf("NPGallery search → %d assets (total %d)", out.Returned, out.Total)
	if out.Entities == nil {
		t.Errorf("expected entities field present (even if empty)")
	}
}

func TestNPGallerySearch_UnknownMode(t *testing.T) {
	_, err := NPGallerySearch(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
