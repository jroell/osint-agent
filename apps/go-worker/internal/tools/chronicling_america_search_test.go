package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestChroniclingAmericaSearch_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	out, err := ChroniclingAmericaSearch(ctx, map[string]any{
		"query":     "Annie Besant Theosophy",
		"year_from": float64(1890),
		"year_to":   float64(1920),
	})
	if err != nil {
		t.Fatalf("ChroniclingAmericaSearch: %v", err)
	}
	if out.Total == 0 {
		t.Logf("zero hits — corpus may not cover (still acceptable)")
	}
	if len(out.Entities) == 0 && out.Total > 0 {
		t.Errorf("got total>0 but no entities")
	}
}

func TestChroniclingAmericaSearch_UnknownMode(t *testing.T) {
	_, err := ChroniclingAmericaSearch(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
