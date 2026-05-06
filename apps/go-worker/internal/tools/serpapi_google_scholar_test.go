package tools

import (
	"context"
	"testing"
)

func TestSerpAPIGoogleScholar_NoAPIKey(t *testing.T) {
	for _, k := range []string{"SERPAPI_KEY", "SERPAPI_API_KEY"} {
		setTestEnv(t, k, "")
	}
	_, err := SerpAPIGoogleScholar(context.Background(), map[string]any{"query": "test"})
	if err == nil {
		t.Fatal("expected error when SERPAPI_KEY unset")
	}
}

func TestSerpAPIGoogleScholar_UnknownMode(t *testing.T) {
	setTestEnv(t, "SERPAPI_KEY", "x")
	_, err := SerpAPIGoogleScholar(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
