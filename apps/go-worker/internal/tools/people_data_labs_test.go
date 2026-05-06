package tools

import (
	"context"
	"testing"
)

func TestPeopleDataLabs_NoAPIKey(t *testing.T) {
	for _, k := range []string{"PEOPLE_DATA_LABS_API_KEY", "PDL_API_KEY"} {
		setTestEnv(t, k, "")
	}
	_, err := PeopleDataLabsLookup(context.Background(), map[string]any{"email": "test@example.com"})
	if err == nil {
		t.Fatal("expected error when PEOPLE_DATA_LABS_API_KEY unset")
	}
}

func TestPeopleDataLabs_UnknownMode(t *testing.T) {
	setTestEnv(t, "PEOPLE_DATA_LABS_API_KEY", "x")
	_, err := PeopleDataLabsLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
