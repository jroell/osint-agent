package tools

import (
	"context"
	"testing"
)

func TestSetlistFMLookup_NoAPIKey(t *testing.T) {
	setTestEnv(t, "SETLISTFM_API_KEY", "")
	_, err := SetlistFMLookup(context.Background(), map[string]any{"artist_name": "Radiohead"})
	if err == nil {
		t.Fatal("expected error when SETLISTFM_API_KEY unset")
	}
}

func TestSetlistFMLookup_UnknownMode(t *testing.T) {
	setTestEnv(t, "SETLISTFM_API_KEY", "x")
	_, err := SetlistFMLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
