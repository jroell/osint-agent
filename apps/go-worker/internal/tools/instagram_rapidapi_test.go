package tools

import (
	"context"
	"testing"
)

func TestInstagramRapidAPI_NoAPIKey(t *testing.T) {
	setTestEnv(t, "RAPID_API_KEY", "")
	_, err := InstagramRapidAPILookup(context.Background(), map[string]any{"username": "instagram"})
	if err == nil {
		t.Fatal("expected error when RAPID_API_KEY unset")
	}
}

func TestInstagramRapidAPI_UnknownMode(t *testing.T) {
	setTestEnv(t, "RAPID_API_KEY", "x")
	_, err := InstagramRapidAPILookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
