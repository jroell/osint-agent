package tools

import (
	"context"
	"testing"
)

func TestTikTokLookup_NoAPIKey(t *testing.T) {
	setTestEnv(t, "RAPID_API_KEY", "")
	_, err := TikTokLookup(context.Background(), map[string]any{"username": "tiktok"})
	if err == nil {
		t.Fatal("expected error when RAPID_API_KEY unset")
	}
}

func TestTikTokLookup_UnknownMode(t *testing.T) {
	setTestEnv(t, "RAPID_API_KEY", "x")
	_, err := TikTokLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
