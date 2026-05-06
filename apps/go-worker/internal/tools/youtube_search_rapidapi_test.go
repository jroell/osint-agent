package tools

import (
	"context"
	"testing"
)

func TestYouTubeSearchRapidAPI_NoAPIKey(t *testing.T) {
	setTestEnv(t, "RAPID_API_KEY", "")
	_, err := YouTubeSearchRapidAPILookup(context.Background(), map[string]any{"query": "test"})
	if err == nil {
		t.Fatal("expected error when RAPID_API_KEY unset")
	}
}

func TestYouTubeSearchRapidAPI_UnknownMode(t *testing.T) {
	setTestEnv(t, "RAPID_API_KEY", "x")
	_, err := YouTubeSearchRapidAPILookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
