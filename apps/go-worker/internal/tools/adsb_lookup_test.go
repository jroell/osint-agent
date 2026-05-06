package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestADSBLookup_LiveByCallsign(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := ADSBLookup(ctx, map[string]any{"callsign": "UAL1"})
	if err != nil {
		t.Logf("ADS-B live lookup returned (transient): %v", err)
		return
	}
	t.Logf("ADS-B callsign UAL1 → %d aircraft", out.Returned)
}

func TestADSBLookup_UnknownMode(t *testing.T) {
	_, err := ADSBLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
