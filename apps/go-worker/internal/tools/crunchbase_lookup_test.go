package tools

import (
	"context"
	"testing"
)

func TestCrunchbaseLookup_NoAPIKey(t *testing.T) {
	setTestEnv(t, "CRUNCHBASE_API_KEY", "")
	_, err := CrunchbaseLookup(context.Background(), map[string]any{"query": "Anthropic"})
	if err == nil {
		t.Fatal("expected error when CRUNCHBASE_API_KEY unset")
	}
}

func TestCrunchbaseLookup_UnknownMode(t *testing.T) {
	setTestEnv(t, "CRUNCHBASE_API_KEY", "x")
	_, err := CrunchbaseLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
