package tools

import (
	"context"
	"testing"
)

func TestGovInfoSearch_NoAPIKey(t *testing.T) {
	setTestEnv(t, "GOVINFO_API_KEY", "")
	setTestEnv(t, "DATA_GOV_API_KEY", "")
	_, err := GovInfoSearch(context.Background(), map[string]any{"query": "test"})
	if err == nil {
		t.Fatal("expected error when API keys unset")
	}
}

func TestGovInfoSearch_UnknownMode(t *testing.T) {
	setTestEnv(t, "GOVINFO_API_KEY", "x")
	_, err := GovInfoSearch(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
