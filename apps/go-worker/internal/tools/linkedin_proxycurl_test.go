package tools

import (
	"context"
	"testing"
)

func TestLinkedInProxycurlLookup_NoAPIKey(t *testing.T) {
	setTestEnv(t, "PROXYCURL_API_KEY", "")
	_, err := LinkedInProxycurlLookup(context.Background(), map[string]any{"url": "https://www.linkedin.com/in/williamhgates/"})
	if err == nil {
		t.Fatal("expected error when PROXYCURL_API_KEY unset")
	}
}

func TestLinkedInProxycurlLookup_UnknownMode(t *testing.T) {
	setTestEnv(t, "PROXYCURL_API_KEY", "x")
	_, err := LinkedInProxycurlLookup(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestLinkedInProxycurlLookup_BackwardsCompatURL(t *testing.T) {
	setTestEnv(t, "PROXYCURL_API_KEY", "")
	// Should fail at API-key check, but should NOT fail at the input-validation
	// stage — proves the `url` backwards-compat field was accepted.
	_, err := LinkedInProxycurlLookup(context.Background(), map[string]any{"url": "https://www.linkedin.com/in/williamhgates/"})
	if err == nil {
		t.Fatal("expected error from missing API key")
	}
	if err.Error() == "required: domain | work_email | person_url | company_url | company+role | url" {
		t.Errorf("input was rejected; backwards-compat broken: %v", err)
	}
}
