package tools

import (
	"context"
	"testing"
)

func TestFamilySearchLookup_NoToken(t *testing.T) {
	setTestEnv(t, "FAMILYSEARCH_ACCESS_TOKEN", "")
	_, err := FamilySearchLookup(context.Background(), map[string]any{"surname": "Smith"})
	if err == nil {
		t.Fatal("expected error when FAMILYSEARCH_ACCESS_TOKEN unset")
	}
}

func TestFamilySearchLookup_UnknownMode(t *testing.T) {
	setTestEnv(t, "FAMILYSEARCH_ACCESS_TOKEN", "x")
	_, err := FamilySearchLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
