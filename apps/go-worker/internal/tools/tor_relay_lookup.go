package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// TorRelayLookup queries the Tor Project's Onionoo public relay metadata
// service. Free, no auth. ~7,000 running relays + ~2,000 bridges, updated
// hourly.
//
// Why this is uniquely valuable:
//   - Definitive answer to "is this IP a Tor exit relay right now?"
//     Pairs with ip_intel_lookup which gives general proxy/hosting flags
//     but not Tor-specific status.
//   - Per-relay metadata: country + AS + advertised bandwidth + uptime
//     + flags (Exit, Guard, BadExit, Stable, Fast, HSDir, etc.) +
//     **operator contact field** which often contains structured
//     attribution: email, abuse contact, KeyBase ID, Twitter handle,
//     Bitcoin donation address — pure ER pivot gold for relay operators.
//   - The BadExit flag is uniquely valuable: relays marked BadExit have
//     been observed misbehaving (e.g. tampering with traffic) by Tor
//     directory authorities. That's a high-signal flag for any incident
//     attribution involving Tor traffic.
//   - consensus_weight is Tor's network-influence metric: a relay with
//     consensus_weight 250000 carries 5× more traffic than one with 50000.
//
// Four modes:
//
//   - "lookup_ip"      : by IP → matching relay (or null + "not a Tor
//                         relay") with full operator metadata
//   - "search"         : fuzzy substring search across nickname / contact
//                         / AS / fingerprint
//   - "country"        : all relays in country code, optionally filtered
//                         by flag (exit / guard / fast / stable / badexit)
//   - "top_by_weight"  : top N relays globally by consensus weight (the
//                         "who controls Tor traffic" view)

type TorRelay struct {
	Nickname             string   `json:"nickname"`
	Fingerprint          string   `json:"fingerprint,omitempty"`
	ORAddresses          []string `json:"or_addresses,omitempty"`
	ExitAddresses        []string `json:"exit_addresses,omitempty"`
	Country              string   `json:"country,omitempty"`
	CountryName          string   `json:"country_name,omitempty"`
	AS                   string   `json:"as,omitempty"`
	ASName               string   `json:"as_name,omitempty"`
	Contact              string   `json:"contact_raw,omitempty"`
	BandwidthRate        int64    `json:"bandwidth_rate,omitempty"`
	BandwidthMbps        float64  `json:"bandwidth_mbps,omitempty"`
	AdvertisedBandwidth  int64    `json:"advertised_bandwidth,omitempty"`
	ConsensusWeight      int64    `json:"consensus_weight,omitempty"`
	Flags                []string `json:"flags,omitempty"`
	Running              bool     `json:"running"`
	UptimePercent1Year   float64  `json:"uptime_percent_1_year,omitempty"`
	Version              string   `json:"version,omitempty"`
	Platform             string   `json:"platform,omitempty"`
	FirstSeen            string   `json:"first_seen,omitempty"`
	LastSeen             string   `json:"last_seen,omitempty"`

	// Surfaced flag booleans for fast filtering
	IsExit    bool `json:"is_exit,omitempty"`
	IsGuard   bool `json:"is_guard,omitempty"`
	IsBadExit bool `json:"is_bad_exit,omitempty"`
	IsStable  bool `json:"is_stable,omitempty"`
	IsFast    bool `json:"is_fast,omitempty"`

	// Parsed contact field — operator attribution
	ContactEmail   string `json:"contact_email,omitempty"`
	ContactAbuse   string `json:"contact_abuse,omitempty"`
	ContactURL     string `json:"contact_url,omitempty"`
	ContactKeybase string `json:"contact_keybase,omitempty"`
	ContactTwitter string `json:"contact_twitter,omitempty"`
	ContactBitcoin string `json:"contact_bitcoin,omitempty"`
	ContactHoster  string `json:"contact_hoster,omitempty"`
}

type TorRelayLookupOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query,omitempty"`
	RelaysPublished   string     `json:"relays_published,omitempty"`
	TotalReturned     int        `json:"total_returned"`
	Relays            []TorRelay `json:"relays,omitempty"`
	Match             *TorRelay  `json:"match,omitempty"`
	NotInTor          bool       `json:"not_in_tor,omitempty"`

	// Aggregations (for country/top modes)
	UniqueCountries   []string   `json:"unique_countries,omitempty"`
	UniqueASes        []string   `json:"unique_ases,omitempty"`
	ExitCount         int        `json:"exit_count,omitempty"`
	GuardCount        int        `json:"guard_count,omitempty"`
	BadExitCount      int        `json:"bad_exit_count,omitempty"`

	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
	Note              string     `json:"note,omitempty"`
}

func TorRelayLookup(ctx context.Context, input map[string]any) (*TorRelayLookupOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["ip"]; ok {
			mode = "lookup_ip"
		} else if _, ok := input["country"]; ok {
			mode = "country"
		} else if _, ok := input["query"]; ok {
			mode = "search"
		} else {
			mode = "top_by_weight"
		}
	}

	out := &TorRelayLookupOutput{
		Mode:   mode,
		Source: "onionoo.torproject.org",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	commonFields := "nickname,fingerprint,or_addresses,exit_addresses,country,country_name,as,as_name,contact,bandwidth_rate,advertised_bandwidth,consensus_weight,flags,running,uptime_percent_1_year,version,platform,first_seen,last_seen"

	switch mode {
	case "lookup_ip":
		ip, _ := input["ip"].(string)
		ip = strings.TrimSpace(ip)
		if ip == "" {
			return nil, fmt.Errorf("input.ip required for lookup_ip mode")
		}
		out.Query = ip
		params := url.Values{}
		params.Set("search", ip)
		params.Set("fields", commonFields)
		body, err := torGet(ctx, cli, "https://onionoo.torproject.org/details?"+params.Encode())
		if err != nil {
			return nil, err
		}
		raw, err := decodeOnionoo(body, out)
		if err != nil {
			return nil, err
		}
		// Lookup IP must match the OR or exit addresses exactly
		var match *TorRelay
		for i := range raw {
			r := raw[i]
			matched := false
			for _, a := range r.ORAddresses {
				if strings.HasPrefix(a, ip+":") || strings.HasPrefix(a, "["+ip+"]:") {
					matched = true
					break
				}
			}
			if !matched {
				for _, a := range r.ExitAddresses {
					if a == ip {
						matched = true
						break
					}
				}
			}
			if matched {
				match = &r
				break
			}
		}
		if match != nil {
			out.Match = match
			out.TotalReturned = 1
		} else {
			out.NotInTor = true
		}

	case "search":
		q, _ := input["query"].(string)
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, fmt.Errorf("input.query required for search mode")
		}
		out.Query = q
		limit := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			limit = int(l)
		}
		params := url.Values{}
		params.Set("search", q)
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("fields", commonFields)
		if v, ok := input["running_only"].(bool); !ok || v {
			params.Set("running", "true")
		}
		body, err := torGet(ctx, cli, "https://onionoo.torproject.org/details?"+params.Encode())
		if err != nil {
			return nil, err
		}
		raw, err := decodeOnionoo(body, out)
		if err != nil {
			return nil, err
		}
		out.Relays = raw
		out.TotalReturned = len(out.Relays)

	case "country":
		country, _ := input["country"].(string)
		country = strings.ToLower(strings.TrimSpace(country))
		if country == "" {
			return nil, fmt.Errorf("input.country required (2-letter code, e.g. 'us', 'de', 'ru')")
		}
		out.Query = country
		limit := 25
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 200 {
			limit = int(l)
		}
		params := url.Values{}
		params.Set("country", country)
		params.Set("running", "true")
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("fields", commonFields)
		params.Set("order", "-consensus_weight")
		if flag, ok := input["flag"].(string); ok && flag != "" {
			params.Set("flag", flag)
		}
		body, err := torGet(ctx, cli, "https://onionoo.torproject.org/details?"+params.Encode())
		if err != nil {
			return nil, err
		}
		raw, err := decodeOnionoo(body, out)
		if err != nil {
			return nil, err
		}
		out.Relays = raw
		out.TotalReturned = len(out.Relays)

	case "top_by_weight":
		limit := 15
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 100 {
			limit = int(l)
		}
		params := url.Values{}
		params.Set("running", "true")
		params.Set("order", "-consensus_weight")
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("fields", commonFields)
		if flag, ok := input["flag"].(string); ok && flag != "" {
			params.Set("flag", flag)
		}
		out.Query = fmt.Sprintf("top %d by consensus weight", limit)
		body, err := torGet(ctx, cli, "https://onionoo.torproject.org/details?"+params.Encode())
		if err != nil {
			return nil, err
		}
		raw, err := decodeOnionoo(body, out)
		if err != nil {
			return nil, err
		}
		out.Relays = raw
		out.TotalReturned = len(out.Relays)

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: lookup_ip, search, country, top_by_weight", mode)
	}

	// Aggregations across returned set
	if mode != "lookup_ip" {
		countrySet := map[string]struct{}{}
		asSet := map[string]struct{}{}
		for _, r := range out.Relays {
			if r.IsExit {
				out.ExitCount++
			}
			if r.IsGuard {
				out.GuardCount++
			}
			if r.IsBadExit {
				out.BadExitCount++
			}
			if r.CountryName != "" {
				countrySet[r.CountryName] = struct{}{}
			}
			if r.ASName != "" {
				asSet[r.ASName] = struct{}{}
			}
		}
		for k := range countrySet {
			out.UniqueCountries = append(out.UniqueCountries, k)
		}
		sort.Strings(out.UniqueCountries)
		for k := range asSet {
			out.UniqueASes = append(out.UniqueASes, k)
		}
		sort.Strings(out.UniqueASes)
	}

	out.HighlightFindings = buildTorHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func decodeOnionoo(body []byte, out *TorRelayLookupOutput) ([]TorRelay, error) {
	var raw struct {
		RelaysPublished string `json:"relays_published"`
		Relays          []struct {
			Nickname            string   `json:"nickname"`
			Fingerprint         string   `json:"fingerprint"`
			ORAddresses         []string `json:"or_addresses"`
			ExitAddresses       []string `json:"exit_addresses"`
			Country             string   `json:"country"`
			CountryName         string   `json:"country_name"`
			AS                  string   `json:"as"`
			ASName              string   `json:"as_name"`
			Contact             string   `json:"contact"`
			BandwidthRate       int64    `json:"bandwidth_rate"`
			AdvertisedBandwidth int64    `json:"advertised_bandwidth"`
			ConsensusWeight     int64    `json:"consensus_weight"`
			Flags               []string `json:"flags"`
			Running             bool     `json:"running"`
			UptimePercent1Year  float64  `json:"uptime_percent_1_year"`
			Version             string   `json:"version"`
			Platform            string   `json:"platform"`
			FirstSeen           string   `json:"first_seen"`
			LastSeen            string   `json:"last_seen"`
		} `json:"relays"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("onionoo decode: %w", err)
	}
	out.RelaysPublished = raw.RelaysPublished
	relays := make([]TorRelay, 0, len(raw.Relays))
	for _, r := range raw.Relays {
		t := TorRelay{
			Nickname:            r.Nickname,
			Fingerprint:         r.Fingerprint,
			ORAddresses:         r.ORAddresses,
			ExitAddresses:       r.ExitAddresses,
			Country:             r.Country,
			CountryName:         r.CountryName,
			AS:                  r.AS,
			ASName:              r.ASName,
			Contact:             r.Contact,
			BandwidthRate:       r.BandwidthRate,
			BandwidthMbps:       float64(r.BandwidthRate) / 1_000_000,
			AdvertisedBandwidth: r.AdvertisedBandwidth,
			ConsensusWeight:     r.ConsensusWeight,
			Flags:               r.Flags,
			Running:             r.Running,
			UptimePercent1Year:  r.UptimePercent1Year,
			Version:             r.Version,
			Platform:            r.Platform,
			FirstSeen:           r.FirstSeen,
			LastSeen:            r.LastSeen,
		}
		// Surface flag booleans
		for _, f := range r.Flags {
			switch f {
			case "Exit":
				t.IsExit = true
			case "Guard":
				t.IsGuard = true
			case "BadExit":
				t.IsBadExit = true
			case "Stable":
				t.IsStable = true
			case "Fast":
				t.IsFast = true
			}
		}
		// Parse contact field for structured operator attribution
		parseTorContact(&t)
		relays = append(relays, t)
	}
	return relays, nil
}

// parseTorContact extracts structured attribution from the Tor contact
// field. Tor relay operators often publish contact info in a semi-
// structured form like:
//   email:alice[]example.com abuse:abuse[]example.com url:https://...
//   keybase:alice twitter:@alice btc:bc1q... hoster:HostingCo
func parseTorContact(t *TorRelay) {
	if t.Contact == "" {
		return
	}
	c := t.Contact
	// Operator convention: replace [at]/[]/(at)/{at}/at with @ in emails
	emailUnobfuscate := func(s string) string {
		s = strings.ReplaceAll(s, "[]", "@")
		s = strings.ReplaceAll(s, "[at]", "@")
		s = strings.ReplaceAll(s, "(at)", "@")
		s = strings.ReplaceAll(s, "{at}", "@")
		return s
	}
	tokens := strings.Fields(c)
	for _, tok := range tokens {
		colonIdx := strings.IndexByte(tok, ':')
		if colonIdx < 1 {
			continue
		}
		key := strings.ToLower(tok[:colonIdx])
		val := tok[colonIdx+1:]
		switch key {
		case "email":
			t.ContactEmail = emailUnobfuscate(val)
		case "abuse":
			t.ContactAbuse = emailUnobfuscate(val)
		case "url":
			t.ContactURL = val
		case "keybase":
			t.ContactKeybase = val
		case "twitter":
			t.ContactTwitter = strings.TrimPrefix(val, "@")
		case "btc", "bitcoin":
			t.ContactBitcoin = val
		case "hoster":
			t.ContactHoster = val
		}
	}
}

func torGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("onionoo: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("onionoo HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildTorHighlights(o *TorRelayLookupOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "lookup_ip":
		if o.NotInTor {
			hi = append(hi, fmt.Sprintf("✓ %s is NOT a current Tor relay (as of %s)", o.Query, o.RelaysPublished))
			break
		}
		r := o.Match
		marker := ""
		if r.IsExit {
			marker += " 🚪EXIT"
		}
		if r.IsBadExit {
			marker += " ⚠️BADEXIT"
		}
		if r.IsGuard {
			marker += " 🛡️GUARD"
		}
		hi = append(hi, fmt.Sprintf("⚠️  %s IS a Tor relay: %s%s", o.Query, r.Nickname, marker))
		hi = append(hi, fmt.Sprintf("  country: %s · AS: %s · bandwidth: %.1f Mbps · consensus weight: %d", r.CountryName, r.ASName, r.BandwidthMbps, r.ConsensusWeight))
		hi = append(hi, fmt.Sprintf("  flags: %s", strings.Join(r.Flags, ", ")))
		// Operator attribution
		ops := []string{}
		if r.ContactEmail != "" {
			ops = append(ops, "email:"+r.ContactEmail)
		}
		if r.ContactAbuse != "" {
			ops = append(ops, "abuse:"+r.ContactAbuse)
		}
		if r.ContactTwitter != "" {
			ops = append(ops, "twitter:@"+r.ContactTwitter)
		}
		if r.ContactKeybase != "" {
			ops = append(ops, "keybase:"+r.ContactKeybase)
		}
		if r.ContactBitcoin != "" {
			ops = append(ops, "btc:"+r.ContactBitcoin)
		}
		if r.ContactHoster != "" {
			ops = append(ops, "hoster:"+r.ContactHoster)
		}
		if len(ops) > 0 {
			hi = append(hi, "  operator: "+strings.Join(ops, " · "))
		}
		if r.UptimePercent1Year > 0 {
			hi = append(hi, fmt.Sprintf("  1-year uptime: %.1f%% · platform: %s", r.UptimePercent1Year, r.Platform))
		}

	case "search":
		hi = append(hi, fmt.Sprintf("✓ %d relays match '%s'", o.TotalReturned, o.Query))
		summary := []string{}
		if o.ExitCount > 0 {
			summary = append(summary, fmt.Sprintf("%d Exit", o.ExitCount))
		}
		if o.GuardCount > 0 {
			summary = append(summary, fmt.Sprintf("%d Guard", o.GuardCount))
		}
		if o.BadExitCount > 0 {
			summary = append(summary, fmt.Sprintf("%d BadExit ⚠️", o.BadExitCount))
		}
		if len(summary) > 0 {
			hi = append(hi, "  flag breakdown: "+strings.Join(summary, " · "))
		}
		for i, r := range o.Relays {
			if i >= 5 {
				break
			}
			ip := ""
			if len(r.ORAddresses) > 0 {
				ip = r.ORAddresses[0]
			}
			contact := ""
			if r.ContactEmail != "" {
				contact = " · " + r.ContactEmail
			}
			hi = append(hi, fmt.Sprintf("  • %s [%s] — %s — %.1f Mbps%s", r.Nickname, r.CountryName, ip, r.BandwidthMbps, contact))
		}

	case "country":
		hi = append(hi, fmt.Sprintf("✓ %d running relays in country '%s' (sorted by consensus weight)", o.TotalReturned, o.Query))
		hi = append(hi, fmt.Sprintf("  flag mix: %d Exit, %d Guard, %d BadExit ⚠️", o.ExitCount, o.GuardCount, o.BadExitCount))
		if len(o.UniqueASes) > 0 && len(o.UniqueASes) <= 6 {
			hi = append(hi, "  ASes: "+strings.Join(o.UniqueASes, ", "))
		} else if len(o.UniqueASes) > 0 {
			hi = append(hi, fmt.Sprintf("  unique ASes hosting: %d", len(o.UniqueASes)))
		}
		for i, r := range o.Relays {
			if i >= 6 {
				break
			}
			marker := ""
			if r.IsExit {
				marker += " 🚪"
			}
			if r.IsBadExit {
				marker += " ⚠️"
			}
			hi = append(hi, fmt.Sprintf("  • %s [%s] — %.1f Mbps · weight %d%s", r.Nickname, r.ASName, r.BandwidthMbps, r.ConsensusWeight, marker))
		}

	case "top_by_weight":
		hi = append(hi, fmt.Sprintf("✓ Top %d Tor relays by consensus weight (network-influence ranking)", o.TotalReturned))
		hi = append(hi, fmt.Sprintf("  countries: %d unique · ASes: %d unique · BadExits: %d", len(o.UniqueCountries), len(o.UniqueASes), o.BadExitCount))
		for i, r := range o.Relays {
			if i >= 8 {
				break
			}
			marker := ""
			if r.IsExit {
				marker += " 🚪"
			}
			if r.IsBadExit {
				marker += " ⚠️"
			}
			if r.IsGuard {
				marker += " 🛡️"
			}
			hi = append(hi, fmt.Sprintf("  [%2d] %s [%s/%s] — weight %d · %.1f Mbps%s", i+1, r.Nickname, r.CountryName, r.ASName, r.ConsensusWeight, r.BandwidthMbps, marker))
		}
	}
	return hi
}
