package tools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type ASNOutput struct {
	Query       string `json:"query"` // input echo
	Kind        string `json:"kind"`  // "ip" | "asn"
	ASN         int    `json:"asn,omitempty"`
	ASName      string `json:"as_name,omitempty"`
	Prefix      string `json:"prefix,omitempty"`        // covering prefix (IP path only)
	CountryCode string `json:"country_code,omitempty"`
	Registry    string `json:"registry,omitempty"`      // arin / ripe / apnic / lacnic / afrinic
	Allocated   string `json:"allocated,omitempty"`     // YYYY-MM-DD
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
	TookMs      int64  `json:"tookMs"`
}

// ASNLookup resolves an IP or ASN via Team Cymru's DNS-based whois service —
// free, no API key, decade-stable, no third-party domain dependency
// (asn.cymru.com is actively maintained and authoritative).
//
// IP path: TXT lookup at `<reversed-ip>.origin.asn.cymru.com` (or origin6 for v6)
//   → "ASN | Prefix | CC | Registry | Allocated"
// ASN path: TXT lookup at `AS<num>.asn.cymru.com`
//   → "ASN | CC | Registry | Allocated | AS-Name"
//
// Reference: https://team-cymru.com/community-services/ip-asn-mapping/
func ASNLookup(ctx context.Context, input map[string]any) (*ASNOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("input.target required (IP or ASN)")
	}
	start := time.Now()
	r := &net.Resolver{PreferGo: true}

	// IP path.
	if ip := net.ParseIP(target); ip != nil {
		zone := "origin.asn.cymru.com"
		var label string
		if ip4 := ip.To4(); ip4 != nil {
			label = fmt.Sprintf("%d.%d.%d.%d", ip4[3], ip4[2], ip4[1], ip4[0])
		} else {
			zone = "origin6.asn.cymru.com"
			label = reverseV6Nibbles(ip)
		}
		txts, err := r.LookupTXT(ctx, label+"."+zone)
		if err != nil {
			return nil, fmt.Errorf("cymru ip lookup: %w", err)
		}
		if len(txts) == 0 {
			return nil, fmt.Errorf("no Cymru record for %s", target)
		}
		// "ASN | Prefix | CC | Registry | Allocated"
		parts := splitPipe(txts[0])
		out := &ASNOutput{
			Query:  target,
			Kind:   "ip",
			Source: "team-cymru-dns",
			TookMs: time.Since(start).Milliseconds(),
		}
		if len(parts) >= 1 {
			// Cymru returns space-separated multi-origin ASNs sometimes — take the first.
			asnStr := strings.Fields(parts[0])
			if len(asnStr) > 0 {
				out.ASN, _ = strconv.Atoi(asnStr[0])
			}
		}
		if len(parts) >= 2 {
			out.Prefix = parts[1]
		}
		if len(parts) >= 3 {
			out.CountryCode = parts[2]
		}
		if len(parts) >= 4 {
			out.Registry = parts[3]
		}
		if len(parts) >= 5 {
			out.Allocated = parts[4]
		}

		// Optional second hop: enrich with the AS name.
		if out.ASN > 0 {
			if name, _ := lookupASName(ctx, r, out.ASN); name != "" {
				out.ASName = name
			}
		}
		return out, nil
	}

	// ASN path. Accept "AS15169", "as15169", "15169".
	asnNum := strings.TrimPrefix(strings.ToUpper(target), "AS")
	if !isPositiveInt(asnNum) {
		return nil, fmt.Errorf("input.target %q is neither a valid IP nor ASN", target)
	}
	n, _ := strconv.Atoi(asnNum)
	out := &ASNOutput{
		Query:  target,
		Kind:   "asn",
		ASN:    n,
		Source: "team-cymru-dns",
		TookMs: time.Since(start).Milliseconds(),
	}
	if name, fields := lookupASNFull(ctx, r, n); name != "" || fields != nil {
		out.ASName = name
		if len(fields) >= 2 {
			out.CountryCode = fields[1]
		}
		if len(fields) >= 3 {
			out.Registry = fields[2]
		}
		if len(fields) >= 4 {
			out.Allocated = fields[3]
		}
		if len(fields) >= 5 {
			out.Description = fields[4]
		}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	return nil, fmt.Errorf("no Cymru record for AS%d", n)
}

// lookupASName returns just the AS name for an ASN.
func lookupASName(ctx context.Context, r *net.Resolver, asn int) (string, error) {
	_, fields := lookupASNFull(ctx, r, asn)
	if len(fields) < 5 {
		return "", nil
	}
	return strings.TrimSpace(fields[4]), nil
}

// lookupASNFull returns (asName, fullFields) where fullFields = [ASN, CC, Registry, Allocated, Name].
func lookupASNFull(ctx context.Context, r *net.Resolver, asn int) (string, []string) {
	txts, err := r.LookupTXT(ctx, fmt.Sprintf("AS%d.asn.cymru.com", asn))
	if err != nil || len(txts) == 0 {
		return "", nil
	}
	fields := splitPipe(txts[0])
	if len(fields) < 5 {
		return "", fields
	}
	return strings.TrimSpace(fields[4]), fields
}

func splitPipe(s string) []string {
	parts := strings.Split(s, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

func reverseV6Nibbles(ip net.IP) string {
	ip16 := ip.To16()
	if ip16 == nil {
		return ""
	}
	out := make([]byte, 0, 64)
	for i := 15; i >= 0; i-- {
		hi := ip16[i] >> 4
		lo := ip16[i] & 0x0f
		out = append(out, hexNibble(lo), '.', hexNibble(hi))
		if i > 0 {
			out = append(out, '.')
		}
	}
	return string(out)
}

func hexNibble(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + (b - 10)
}

func isPositiveInt(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
