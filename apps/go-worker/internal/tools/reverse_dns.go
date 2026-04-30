package tools

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

type ReverseDNSOutput struct {
	IP        string   `json:"ip"`
	Hostnames []string `json:"hostnames"`
	TookMs    int64    `json:"tookMs"`
	Note      string   `json:"note,omitempty"`
}

// ReverseDNS performs a live PTR lookup. Returns hostnames bound to the IP today.
// Historical PTR (i.e. names that ever pointed at this IP) requires a paid passive-DNS
// provider — out of scope for the free-tier Phase-0 implementation.
func ReverseDNS(ctx context.Context, input map[string]any) (*ReverseDNSOutput, error) {
	ipStr, _ := input["ip"].(string)
	ipStr = strings.TrimSpace(ipStr)
	if ipStr == "" {
		return nil, errors.New("input.ip required")
	}
	if net.ParseIP(ipStr) == nil {
		return nil, errors.New("input.ip is not a valid IP address")
	}

	start := time.Now()
	r := &net.Resolver{PreferGo: true}
	names, err := r.LookupAddr(ctx, ipStr)
	out := &ReverseDNSOutput{
		IP:     ipStr,
		TookMs: time.Since(start).Milliseconds(),
		Note:   "live PTR only; historical reverse-DNS requires a paid passive-DNS provider",
	}
	if err != nil {
		// `no such host` (NXDOMAIN) is a normal "no PTR" answer, not a tool failure.
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return out, nil
		}
		return nil, err
	}
	for _, n := range names {
		out.Hostnames = append(out.Hostnames, strings.TrimSuffix(strings.ToLower(n), "."))
	}
	return out, nil
}
