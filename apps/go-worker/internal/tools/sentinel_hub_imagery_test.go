package tools

import (
	"context"
	"testing"
)

func TestSentinelHubImagery_NoCredentials(t *testing.T) {
	setTestEnv(t, "SENTINEL_HUB_CLIENT_ID", "")
	setTestEnv(t, "SENTINEL_HUB_CLIENT_SECRET", "")
	_, err := SentinelHubImagery(context.Background(), map[string]any{
		"bbox": []any{-122.5, 37.7, -122.4, 37.8},
	})
	if err == nil {
		t.Fatal("expected error when credentials unset")
	}
}
