package tools

import (
	"context"
	"testing"
)

func TestFlightAwareLookup_NoAPIKey(t *testing.T) {
	setTestEnv(t, "FLIGHTAWARE_API_KEY", "")
	_, err := FlightAwareLookup(context.Background(), map[string]any{"ident": "UAL1"})
	if err == nil {
		t.Fatal("expected error when FLIGHTAWARE_API_KEY unset")
	}
}

func TestFlightAwareLookup_UnknownMode(t *testing.T) {
	setTestEnv(t, "FLIGHTAWARE_API_KEY", "x")
	_, err := FlightAwareLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
