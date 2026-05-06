package tools

import (
	"context"
	"testing"
)

func TestMarineTrafficLookup_NoAPIKey(t *testing.T) {
	setTestEnv(t, "MARINETRAFFIC_API_KEY", "")
	_, err := MarineTrafficLookup(context.Background(), map[string]any{"mmsi": float64(123456789)})
	if err == nil {
		t.Fatal("expected error when MARINETRAFFIC_API_KEY unset")
	}
}

func TestMarineTrafficLookup_UnknownMode(t *testing.T) {
	setTestEnv(t, "MARINETRAFFIC_API_KEY", "x")
	_, err := MarineTrafficLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
