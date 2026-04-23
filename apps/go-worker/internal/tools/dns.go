package tools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

type DNSOutput struct {
	Domain string            `json:"domain"`
	A      []string          `json:"a,omitempty"`
	AAAA   []string          `json:"aaaa,omitempty"`
	MX     []DNSMX           `json:"mx,omitempty"`
	TXT    []string          `json:"txt,omitempty"`
	NS     []string          `json:"ns,omitempty"`
	CNAME  string            `json:"cname,omitempty"`
	TookMs int64             `json:"tookMs"`
	Errors map[string]string `json:"errors,omitempty"`
}

type DNSMX struct {
	Host string `json:"host"`
	Pref int    `json:"preference"`
}

func DNS(ctx context.Context, input map[string]any) (*DNSOutput, error) {
	domain, ok := input["domain"].(string)
	if !ok || domain == "" {
		return nil, errors.New("input.domain required")
	}

	start := time.Now()
	out := &DNSOutput{Domain: domain, Errors: map[string]string{}}

	var resolver = &net.Resolver{PreferGo: true}

	if ips, err := resolver.LookupIPAddr(ctx, domain); err == nil {
		for _, ip := range ips {
			if ip.IP.To4() != nil {
				out.A = append(out.A, ip.IP.String())
			} else {
				out.AAAA = append(out.AAAA, ip.IP.String())
			}
		}
	} else {
		out.Errors["A/AAAA"] = err.Error()
	}

	if mxs, err := resolver.LookupMX(ctx, domain); err == nil {
		for _, mx := range mxs {
			out.MX = append(out.MX, DNSMX{Host: mx.Host, Pref: int(mx.Pref)})
		}
	} else {
		out.Errors["MX"] = err.Error()
	}

	if txts, err := resolver.LookupTXT(ctx, domain); err == nil {
		out.TXT = txts
	} else {
		out.Errors["TXT"] = err.Error()
	}

	if nss, err := resolver.LookupNS(ctx, domain); err == nil {
		for _, n := range nss {
			out.NS = append(out.NS, n.Host)
		}
	} else {
		out.Errors["NS"] = err.Error()
	}

	if cname, err := resolver.LookupCNAME(ctx, domain); err == nil {
		out.CNAME = cname
	} else {
		out.Errors["CNAME"] = err.Error()
	}

	out.TookMs = time.Since(start).Milliseconds()
	if len(out.A) == 0 && len(out.AAAA) == 0 && len(out.MX) == 0 && len(out.TXT) == 0 {
		return out, fmt.Errorf("no DNS records resolved; errors: %v", out.Errors)
	}
	return out, nil
}
