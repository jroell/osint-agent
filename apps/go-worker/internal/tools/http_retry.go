package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// HTTPRetryConfig controls retry behaviour for HTTPRetryDo.
//
// Defaults (when zero values are passed): MaxAttempts=3, BaseDelay=400ms,
// MaxDelay=4s, RetryOnStatus={429, 500, 502, 503, 504}.
type HTTPRetryConfig struct {
	MaxAttempts    int
	BaseDelay      time.Duration
	MaxDelay       time.Duration
	RetryOnStatus  map[int]bool
	JitterFraction float64 // 0..1; default 0.25
}

func (c HTTPRetryConfig) withDefaults() HTTPRetryConfig {
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.BaseDelay <= 0 {
		c.BaseDelay = 400 * time.Millisecond
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 4 * time.Second
	}
	if c.RetryOnStatus == nil {
		c.RetryOnStatus = map[int]bool{
			http.StatusTooManyRequests:     true, // 429
			http.StatusInternalServerError: true, // 500
			http.StatusBadGateway:          true, // 502
			http.StatusServiceUnavailable:  true, // 503
			http.StatusGatewayTimeout:      true, // 504
		}
	}
	if c.JitterFraction <= 0 || c.JitterFraction > 1 {
		c.JitterFraction = 0.25
	}
	return c
}

// HTTPRetryDo performs an HTTP request with exponential-backoff retry on
// transient failures. A "transient" failure is either a network error or
// a status code in cfg.RetryOnStatus.
//
// On retry, the response body of the prior attempt is fully drained and
// closed before issuing the next request, so the connection can be
// reused by the underlying transport pool.
//
// Returns the FINAL response (whether successful or not) and an error
// only when no successful response was obtainable within MaxAttempts.
//
// The request must be re-buildable per attempt — pass a getReq closure
// rather than a pre-built *http.Request, because http.Request bodies
// are single-use io.Readers.
func HTTPRetryDo(ctx context.Context, cli *http.Client, cfg HTTPRetryConfig, getReq func(context.Context) (*http.Request, error)) (*http.Response, error) {
	cfg = cfg.withDefaults()
	var lastErr error
	var lastResp *http.Response
	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		req, buildErr := getReq(ctx)
		if buildErr != nil {
			return nil, fmt.Errorf("http_retry build req: %w", buildErr)
		}
		resp, err := cli.Do(req)
		if err == nil && !cfg.RetryOnStatus[resp.StatusCode] {
			return resp, nil
		}
		// Drain & close any body before retry, capture last failure for the caller.
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			lastResp = resp
			lastErr = fmt.Errorf("transient HTTP %d on attempt %d", resp.StatusCode, attempt)
		} else {
			lastErr = fmt.Errorf("attempt %d: %w", attempt, err)
		}
		if attempt == cfg.MaxAttempts {
			break
		}
		// Honour Retry-After if present (seconds form only — date form is rare).
		delay := computeBackoff(cfg, attempt, lastResp)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	if lastResp != nil {
		return lastResp, errors.New(lastErr.Error())
	}
	return nil, lastErr
}

func computeBackoff(cfg HTTPRetryConfig, attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			var secs int
			if _, err := fmt.Sscanf(ra, "%d", &secs); err == nil && secs > 0 {
				d := time.Duration(secs) * time.Second
				if d > cfg.MaxDelay {
					d = cfg.MaxDelay
				}
				return d
			}
		}
	}
	// 2^(attempt-1) * BaseDelay, capped at MaxDelay, with ±jitter.
	base := cfg.BaseDelay
	for i := 1; i < attempt; i++ {
		base *= 2
		if base > cfg.MaxDelay {
			base = cfg.MaxDelay
			break
		}
	}
	jitterRange := float64(base) * cfg.JitterFraction
	jitter := (rand.Float64()*2 - 1) * jitterRange
	d := base + time.Duration(jitter)
	if d < 0 {
		d = 0
	}
	return d
}
