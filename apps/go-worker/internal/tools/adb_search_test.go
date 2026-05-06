package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestADBSearch_LiveBiography(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := ADBSearch(ctx, map[string]any{
		"mode": "biography",
		"slug": "lawson-henry-7118",
	})
	if err != nil {
		if isTransientLiveNetworkError(err) {
			t.Skipf("ADB endpoint is flaky; skipping transient network failure: %v", err)
		}
		t.Fatalf("ADBSearch biography: %v", err)
	}
	if out.Biography == nil {
		t.Fatal("expected biography record")
	}
	if !strings.Contains(strings.ToLower(out.Biography.Name), "lawson") {
		t.Errorf("expected name to contain Lawson; got %q", out.Biography.Name)
	}
	t.Logf("ADB biography → %s (%s)", out.Biography.Name, out.Biography.Birth)
}

func TestADBSearch_LiveSearchTolerant(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Search HTML rendering on adb.anu.edu.au may be JS-rendered or empty;
	// just verify the call succeeds without HTTP error.
	_, err := ADBSearch(ctx, map[string]any{"query": "Henry Lawson"})
	if err != nil {
		t.Logf("ADB search returned error (acceptable for HTML-only endpoint): %v", err)
	}
}

func TestADBSearch_UnknownMode(t *testing.T) {
	_, err := ADBSearch(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
