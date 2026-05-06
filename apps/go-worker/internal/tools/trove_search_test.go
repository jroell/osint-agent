package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestTroveSearch_LiveSearch(t *testing.T) {
	if os.Getenv("TROVE_API_KEY") == "" {
		t.Skip("TROVE_API_KEY not set; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := TroveSearch(ctx, map[string]any{
		"query":    "Annie Besant Theosophy",
		"category": "newspaper",
	})
	if err != nil {
		t.Fatalf("TroveSearch: %v", err)
	}
	if out.Total == 0 {
		t.Errorf("expected hits for Annie Besant; got 0")
	}
	if len(out.Entities) == 0 {
		t.Errorf("expected entity envelope")
	}
}

func TestTroveSearch_NoAPIKey(t *testing.T) {
	setTestEnv(t, "TROVE_API_KEY", "")
	_, err := TroveSearch(context.Background(), map[string]any{"query": "x"})
	if err == nil {
		t.Fatal("expected error when TROVE_API_KEY unset")
	}
}

func TestTroveSearch_UnknownMode(t *testing.T) {
	setTestEnv(t, "TROVE_API_KEY", "x")
	_, err := TroveSearch(context.Background(), map[string]any{"mode": "bogus", "query": "x"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
