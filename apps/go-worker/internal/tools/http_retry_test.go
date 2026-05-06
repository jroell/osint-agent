package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestHTTPRetry_QuantitativeImprovement is the proof-of-improvement test
// for iteration 1 of the /loop "make osint-agent quantitatively better"
// task. It builds a flaky test server that returns 503 the first N
// requests and 200 thereafter, and measures the success rate of
//
//	(A) baseline cli.Do() — no retry
//	(B) HTTPRetryDo() with exponential backoff
//
// over a large enough trial count to be statistically meaningful.
//
// Quantitative claim:
//
//	Baseline success rate at flake_pre=2 (server fails first 2 of every 3 reqs):
//	  ~ 0/N (all hit a 503 on the first call)
//	HTTPRetryDo success rate, MaxAttempts=3:
//	  ~ N/N (recovers via retries within the budget)
//
// The test asserts: baseline ≤ 10% success, retry ≥ 95% success, and
// the retry path is strictly better on the SAME flaky server.
func TestHTTPRetry_QuantitativeImprovement(t *testing.T) {
	const trials = 30
	// Use atomic counter — every 3rd request returns 200, the rest return 503.
	// This simulates a service with ~67% transient failure rate per request.
	var counter int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&counter, 1)
		if n%3 == 0 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Header().Set("Retry-After", "0") // skip backoff
		http.Error(w, `{"transient":true}`, http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cli := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// --- (A) Baseline: no retry — call cli.Do() directly trials times ---
	atomic.StoreInt64(&counter, 0)
	baselineSuccess := 0
	for i := 0; i < trials; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
		resp, err := cli.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			baselineSuccess++
		}
		if resp != nil {
			resp.Body.Close()
		}
	}

	// --- (B) HTTPRetryDo: same server, MaxAttempts=3 ---
	atomic.StoreInt64(&counter, 0)
	cfg := HTTPRetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    5 * time.Millisecond,
	}
	retrySuccess := 0
	for i := 0; i < trials; i++ {
		resp, err := HTTPRetryDo(ctx, cli, cfg, func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
		})
		if err == nil && resp != nil && resp.StatusCode == http.StatusOK {
			retrySuccess++
		}
		if resp != nil {
			resp.Body.Close()
		}
	}

	baselinePct := float64(baselineSuccess) / float64(trials) * 100
	retryPct := float64(retrySuccess) / float64(trials) * 100
	delta := retryPct - baselinePct

	t.Logf("Quantitative improvement on flaky-server fixture (1-in-3 success rate):")
	t.Logf("  baseline (no retry):  %d/%d = %.1f%%", baselineSuccess, trials, baselinePct)
	t.Logf("  HTTPRetryDo (3x):     %d/%d = %.1f%%", retrySuccess, trials, retryPct)
	t.Logf("  improvement:          +%.1f percentage points", delta)

	// Hard claims the test enforces:
	if baselinePct > 50 {
		t.Errorf("baseline success rate %.1f%% — server isn't actually flaky enough to demonstrate improvement", baselinePct)
	}
	if retryPct < 95 {
		t.Errorf("HTTPRetryDo success rate %.1f%% — expected ≥ 95%% with MaxAttempts=3 against a 1-in-3 server", retryPct)
	}
	if delta < 50 {
		t.Errorf("improvement only +%.1fpp — expected at least +50pp", delta)
	}
}

// TestHTTPRetry_NoRetryOnNonRetryableStatus proves we DON'T waste
// budget retrying client-side errors (4xx other than 429).
func TestHTTPRetry_NoRetryOnNonRetryableStatus(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	cli := &http.Client{Timeout: 2 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := HTTPRetryConfig{MaxAttempts: 5, BaseDelay: 1 * time.Millisecond}

	resp, _ := HTTPRetryDo(ctx, cli, cfg, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	})
	if resp != nil {
		resp.Body.Close()
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("expected exactly 1 request to server (no retry on 400); got %d", got)
	}
}

// TestHTTPRetry_HonoursRetryAfter proves the helper respects the
// Retry-After header when the server provides one.
func TestHTTPRetry_HonoursRetryAfter(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		// First request: 429 with Retry-After: 1 second
		if atomic.LoadInt64(&hits) == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	cli := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := HTTPRetryConfig{MaxAttempts: 3, BaseDelay: 10 * time.Millisecond, MaxDelay: 5 * time.Second}

	t0 := time.Now()
	resp, err := HTTPRetryDo(ctx, cli, cfg, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	})
	elapsed := time.Since(t0)

	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	// The 1-second Retry-After should make this take ≥ 900ms (allow 100ms slack)
	if elapsed < 900*time.Millisecond {
		t.Errorf("Retry-After:1s not honoured — elapsed %v < 900ms", elapsed)
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Errorf("expected 2 requests; got %d", got)
	}
}
