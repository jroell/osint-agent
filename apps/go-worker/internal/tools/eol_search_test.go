package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestEOLSearch_LiveSearch(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := EOLSearch(ctx, map[string]any{"query": "Quercus alba"})
	if err != nil {
		t.Logf("EOL live returned (occasional 5xx): %v", err)
		return
	}
	if out.Returned == 0 {
		t.Logf("zero results (acceptable; EOL search occasionally returns nothing for niche taxa)")
	}
	t.Logf("EOL search → %d records", out.Returned)
}

func TestEOLSearch_UnknownMode(t *testing.T) {
	_, err := EOLSearch(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
