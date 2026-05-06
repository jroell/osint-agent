package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestWorldCatSearch_LiveByOCLC(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := WorldCatSearch(ctx, map[string]any{"oclc": "644097"})
	if err != nil {
		t.Logf("WorldCat returned (endpoint varies): %v", err)
		return
	}
	if out.Returned == 0 {
		t.Errorf("expected at least a stub record")
	}
}

func TestWorldCatSearch_UnknownMode(t *testing.T) {
	_, err := WorldCatSearch(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
