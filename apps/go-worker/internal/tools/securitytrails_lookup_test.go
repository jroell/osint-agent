package tools

import (
	"context"
	"testing"
)

func TestSecurityTrailsLookup_NoAPIKey(t *testing.T) {
	setTestEnv(t, "SECURITYTRAILS_API_KEY", "")
	_, err := SecurityTrailsLookup(context.Background(), map[string]any{"domain": "example.com"})
	if err == nil {
		t.Fatal("expected error when SECURITYTRAILS_API_KEY unset")
	}
}

func TestSecurityTrailsLookup_UnknownMode(t *testing.T) {
	setTestEnv(t, "SECURITYTRAILS_API_KEY", "x")
	_, err := SecurityTrailsLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
