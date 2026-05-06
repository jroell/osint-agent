package tools

import (
	"context"
	"testing"
)

func TestBraveSearch_NoAPIKey(t *testing.T) {
	setTestEnv(t, "BRAVE_SEARCH_API_KEY", "")
	_, err := BraveSearch(context.Background(), map[string]any{"query": "test"})
	if err == nil {
		t.Fatal("expected error when BRAVE_SEARCH_API_KEY unset")
	}
}

func TestBraveSearch_UnknownMode(t *testing.T) {
	setTestEnv(t, "BRAVE_SEARCH_API_KEY", "x")
	_, err := BraveSearch(context.Background(), map[string]any{"mode": "bogus", "query": "x"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
