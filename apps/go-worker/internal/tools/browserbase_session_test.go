package tools

import (
	"context"
	"testing"
)

func TestBrowserbaseSession_NoCredentials(t *testing.T) {
	setTestEnv(t, "BROWSERBASE_API_KEY", "")
	setTestEnv(t, "BROWSERBASE_PROJECT_ID", "")
	_, err := BrowserbaseSession(context.Background(), map[string]any{"url": "https://example.com"})
	if err == nil {
		t.Fatal("expected error when credentials unset")
	}
}
