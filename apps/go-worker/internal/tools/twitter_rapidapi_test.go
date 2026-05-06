package tools

import (
	"context"
	"testing"
)

func TestTwitterRapidAPILookup_NoAPIKey(t *testing.T) {
	setTestEnv(t, "RAPID_API_KEY", "")
	_, err := TwitterRapidAPILookup(context.Background(), map[string]any{"username": "elonmusk"})
	if err == nil {
		t.Fatal("expected error when RAPID_API_KEY unset")
	}
}

func TestTwitterRapidAPILookup_UnknownMode(t *testing.T) {
	setTestEnv(t, "RAPID_API_KEY", "x")
	_, err := TwitterRapidAPILookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
