package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLOCCatalogSearch_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := LOCCatalogSearch(ctx, map[string]any{
		"query": "Walt Whitman Leaves of Grass",
	})
	if err != nil {
		t.Fatalf("LOCCatalogSearch: %v", err)
	}
	if out.Total == 0 {
		t.Errorf("expected hits for Walt Whitman; got 0")
	}
}

func TestLOCCatalogSearch_LiveSubjectAuthority(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := LOCCatalogSearch(ctx, map[string]any{
		"mode":    "subject_authority",
		"subject": "Theosophy",
	})
	if err != nil {
		t.Fatalf("LOCCatalogSearch authority: %v", err)
	}
	if out.Authority == nil {
		t.Logf("no authority record found (still passing)")
	}
}

func TestLOCCatalogSearch_UnknownMode(t *testing.T) {
	_, err := LOCCatalogSearch(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
