package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestICIJOffshoreLeaks_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	out, err := ICIJOffshoreLeaks(ctx, map[string]any{"query": "Mossack Fonseca"})
	if err != nil {
		t.Logf("ICIJ live search returned (HTML scraping varies): %v", err)
		return
	}
	t.Logf("ICIJ search → %d nodes (total %d)", out.Returned, out.Total)
}

func TestICIJOffshoreLeaks_UnknownMode(t *testing.T) {
	_, err := ICIJOffshoreLeaks(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
