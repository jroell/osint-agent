package tools

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMathGenealogy_LiveByID(t *testing.T) {
	if os.Getenv("SKIP_LIVE_TESTS") == "1" {
		t.Skip("SKIP_LIVE_TESTS=1; skipping live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// MGP id 18230 = Carl Friedrich Gauss
	out, err := MathGenealogy(ctx, map[string]any{
		"mgp_id": "18230",
	})
	if err != nil {
		t.Fatalf("MathGenealogy by_id: %v", err)
	}
	if out.Person == nil {
		t.Fatal("expected person")
	}
	if out.Person.Name == "" {
		t.Errorf("expected non-empty name; got empty")
	}
	t.Logf("MGP 18230 → %s (%s, %d) advisors=%d students=%d",
		out.Person.Name, out.Person.School, out.Person.Year,
		len(out.Person.Advisors), len(out.Person.Students))
}

func TestMathGenealogy_UnknownMode(t *testing.T) {
	_, err := MathGenealogy(context.Background(), map[string]any{"mode": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}
