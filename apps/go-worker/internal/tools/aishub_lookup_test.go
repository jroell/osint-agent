package tools

import (
	"context"
	"testing"
)

func TestAISHubLookup_NoUsername(t *testing.T) {
	setTestEnv(t, "AISHUB_USERNAME", "")
	_, err := AISHubLookup(context.Background(), map[string]any{"mmsi": float64(123456789)})
	if err == nil {
		t.Fatal("expected error when AISHUB_USERNAME unset")
	}
}

func TestAISHubLookup_UnknownMode(t *testing.T) {
	setTestEnv(t, "AISHUB_USERNAME", "x")
	_, err := AISHubLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
