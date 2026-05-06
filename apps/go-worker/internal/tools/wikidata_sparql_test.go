package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWikidataSPARQL_LiveSimple(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := WikidataSPARQL(ctx, map[string]any{
		"query": `SELECT ?cat ?catLabel WHERE { ?cat wdt:P31 wd:Q146 . SERVICE wikibase:label { bd:serviceParam wikibase:language "en" } } LIMIT 5`,
	})
	if err != nil {
		if isTransientLiveNetworkError(err) {
			t.Skipf("Wikidata endpoint is flaky; skipping transient network failure: %v", err)
		}
		t.Fatalf("WikidataSPARQL: %v", err)
	}
	if out.BindingsCount == 0 {
		t.Errorf("expected ≥1 bindings; got 0")
	}
	if len(out.Entities) == 0 {
		t.Errorf("expected entity envelope")
	}
}

func TestWikidataSPARQL_FindHumansByAttr(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := WikidataSPARQL(ctx, map[string]any{
		"mode":      "find_humans_by_attr",
		"died_year": "2023",
		"limit":     float64(10),
	})
	if err != nil {
		if isTransientLiveNetworkError(err) {
			t.Skipf("Wikidata endpoint is flaky; skipping transient network failure: %v", err)
		}
		t.Fatalf("WikidataSPARQL find_humans: %v", err)
	}
	if !strings.Contains(out.Query, "wdt:P570") {
		t.Errorf("expected DOD filter in generated query: %s", out.Query)
	}
}

func TestWikidataSPARQL_UnknownMode(t *testing.T) {
	_, err := WikidataSPARQL(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
