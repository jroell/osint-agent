package tools

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenAlexAuthorGraph_LiveAuthorWorks(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Geoffrey Hinton OpenAlex id
	out, err := OpenAlexAuthorGraph(ctx, map[string]any{
		"mode":      "author_works",
		"author_id": "A5042120989",
	})
	if err != nil {
		t.Fatalf("OpenAlexAuthorGraph: %v", err)
	}
	if out.Author == nil {
		t.Fatal("expected author record")
	}
	if !strings.EqualFold(out.Author.Name, "Geoffrey Hinton") &&
		!strings.Contains(strings.ToLower(out.Author.Name), "hinton") {
		t.Logf("got author %q (might have changed)", out.Author.Name)
	}
	if len(out.Works) == 0 {
		t.Errorf("expected works for the author")
	}
}

func TestOpenAlexAuthorGraph_UnknownMode(t *testing.T) {
	_, err := OpenAlexAuthorGraph(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
