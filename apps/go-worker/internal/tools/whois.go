package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// WhoisOutput is a structured projection of the upstream RDAP response.
// We surface the most-asked fields in a flat shape for ease of LLM consumption,
// and keep the full upstream JSON under `raw` for deep dives.
type WhoisOutput struct {
	Target      string                 `json:"target"`
	Kind        string                 `json:"kind"` // "domain" | "ip"
	Registrar   string                 `json:"registrar,omitempty"`
	Created     string                 `json:"created,omitempty"`
	Updated     string                 `json:"updated,omitempty"`
	Expires     string                 `json:"expires,omitempty"`
	Status      []string               `json:"status,omitempty"`
	Nameservers []string               `json:"nameservers,omitempty"`
	Source      string                 `json:"source"`
	TookMs      int64                  `json:"tookMs"`
	Raw         map[string]interface{} `json:"raw,omitempty"`
}

// Whois performs an RDAP lookup against rdap.org (free, no key, IANA-bootstrap-aware).
// Accepts either a domain (`example.com`) or IP (`1.1.1.1`) and routes to the right RDAP path.
func Whois(ctx context.Context, input map[string]any) (*WhoisOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("input.target required (domain or IP)")
	}

	kind := "domain"
	path := "domain"
	if ip := net.ParseIP(target); ip != nil {
		kind = "ip"
		path = "ip"
	}

	start := time.Now()
	url := fmt.Sprintf("https://rdap.org/%s/%s", path, target)
	body, err := httpGetJSON(ctx, url, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("rdap fetch: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("rdap parse: %w", err)
	}

	out := &WhoisOutput{
		Target: target,
		Kind:   kind,
		Source: "rdap.org",
		TookMs: time.Since(start).Milliseconds(),
		Raw:    raw,
	}

	// Populate flat fields from the RDAP shape (RFC 7483).
	if events, ok := raw["events"].([]interface{}); ok {
		for _, e := range events {
			ev, _ := e.(map[string]interface{})
			action, _ := ev["eventAction"].(string)
			date, _ := ev["eventDate"].(string)
			switch action {
			case "registration":
				out.Created = date
			case "last changed":
				out.Updated = date
			case "expiration":
				out.Expires = date
			}
		}
	}
	if status, ok := raw["status"].([]interface{}); ok {
		for _, s := range status {
			if str, ok := s.(string); ok {
				out.Status = append(out.Status, str)
			}
		}
	}
	if ns, ok := raw["nameservers"].([]interface{}); ok {
		for _, n := range ns {
			nm, _ := n.(map[string]interface{})
			if h, ok := nm["ldhName"].(string); ok {
				out.Nameservers = append(out.Nameservers, strings.ToLower(h))
			}
		}
	}
	// Registrar — RDAP usually nests it under entities[].vcardArray, but the simpler
	// `port43` (legacy WHOIS server) hint is often just as informative.
	if entities, ok := raw["entities"].([]interface{}); ok {
		for _, e := range entities {
			em, _ := e.(map[string]interface{})
			roles, _ := em["roles"].([]interface{})
			isRegistrar := false
			for _, r := range roles {
				if rs, _ := r.(string); rs == "registrar" {
					isRegistrar = true
					break
				}
			}
			if !isRegistrar {
				continue
			}
			if vcard, ok := em["vcardArray"].([]interface{}); ok && len(vcard) >= 2 {
				if rows, ok := vcard[1].([]interface{}); ok {
					for _, row := range rows {
						r, _ := row.([]interface{})
						if len(r) >= 4 {
							if name, _ := r[0].(string); name == "fn" {
								if val, _ := r[3].(string); val != "" {
									out.Registrar = val
								}
							}
						}
					}
				}
			}
		}
	}

	return out, nil
}

// httpGetJSON is a small stdlib helper used by the free-tier external-API tools.
// Sets a project User-Agent so target services can attribute the traffic.
func httpGetJSON(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16 MiB cap
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
