package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/projectdiscovery/subfinder/v2/pkg/runner"
)

type SubfinderOutput struct {
	Domain     string   `json:"domain"`
	Subdomains []string `json:"subdomains"`
	TookMs     int64    `json:"tookMs"`
}

func Subfinder(ctx context.Context, input map[string]any) (*SubfinderOutput, error) {
	domain, ok := input["domain"].(string)
	if !ok || domain == "" {
		return nil, errors.New("input.domain required")
	}

	start := time.Now()

	cfg := runner.Options{
		Threads:            10,
		Timeout:            30,
		MaxEnumerationTime: 60,
		Silent:             true,
		All:                false,
		Sources:            []string{"crtsh", "hackertarget", "dnsdumpster", "anubis", "alienvault"},
	}
	r, err := runner.NewRunner(&cfg)
	if err != nil {
		return nil, fmt.Errorf("subfinder init: %w", err)
	}

	// EnumerateMultipleDomainsWithCtx is the current API: takes ctx, a reader of
	// newline-delimited domains, and an io.Writer slice for output.
	in := strings.NewReader(domain + "\n")
	var buf bytes.Buffer
	if err := r.EnumerateMultipleDomainsWithCtx(ctx, in, []io.Writer{&buf}); err != nil {
		return nil, fmt.Errorf("subfinder enumerate: %w", err)
	}

	subs := filterEmpty(strings.Split(strings.TrimSpace(buf.String()), "\n"))

	return &SubfinderOutput{
		Domain:     domain,
		Subdomains: subs,
		TookMs:     time.Since(start).Milliseconds(),
	}, nil
}

func filterEmpty(s []string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			out = append(out, x)
		}
	}
	return out
}
