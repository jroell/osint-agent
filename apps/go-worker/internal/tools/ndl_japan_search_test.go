package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNDLJapanSearch_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	out, err := NDLJapanSearch(ctx, map[string]any{"query": "夏目漱石"})
	if err != nil {
		t.Fatalf("NDLJapanSearch: %v", err)
	}
	if out.Returned == 0 {
		t.Logf("zero results (acceptable; OpenSearch is finicky)")
	}
	t.Logf("NDL Japan search → %d items (total %d)", out.Returned, out.Total)
}

func TestNDLJapanSearch_UnknownMode(t *testing.T) {
	_, err := NDLJapanSearch(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
