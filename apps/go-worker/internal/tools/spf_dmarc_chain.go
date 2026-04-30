package tools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

type SPFNode struct {
	Domain      string   `json:"domain"`
	Raw         string   `json:"raw_spf"`
	Includes    []string `json:"includes,omitempty"`
	IP4         []string `json:"ip4,omitempty"`
	IP6         []string `json:"ip6,omitempty"`
	A           []string `json:"a,omitempty"`
	MX          []string `json:"mx,omitempty"`
	Mechanisms  []string `json:"mechanisms,omitempty"`
	Action      string   `json:"action,omitempty"` // -all, ~all, ?all, +all
	Errors      []string `json:"errors,omitempty"`
}

type DMARCRecord struct {
	Domain      string   `json:"domain"`
	Raw         string   `json:"raw"`
	Policy      string   `json:"policy"`            // none, quarantine, reject
	SubPolicy   string   `json:"subdomain_policy"`  // sp= override
	Pct         string   `json:"percentage,omitempty"`
	RUA         []string `json:"reporting_addresses_rua,omitempty"` // aggregate reports
	RUF         []string `json:"reporting_addresses_ruf,omitempty"` // forensic reports
	ADKIM       string   `json:"adkim,omitempty"`     // alignment mode for DKIM
	ASPF        string   `json:"aspf,omitempty"`      // alignment mode for SPF
	StrengthScore int    `json:"strength_score"`      // 0-100 (reject + 100% pct = max)
}

type DKIMSelector struct {
	Selector string `json:"selector"`
	Found    bool   `json:"found"`
	Raw      string `json:"raw,omitempty"`
	Vendor   string `json:"vendor_hint,omitempty"`
}

type SPFDMARCChainOutput struct {
	Domain                string         `json:"domain"`
	SPF                   *SPFNode       `json:"spf,omitempty"`
	SPFExpanded           []SPFNode      `json:"spf_expanded_chain,omitempty"`
	SPFAllIncludedDomains []string       `json:"spf_all_included_domains,omitempty"`
	SPFAllIP4             []string       `json:"spf_all_ip4_authorized,omitempty"`
	SPFEmailVendors       []string       `json:"spf_email_vendors_detected,omitempty"`
	DMARC                 *DMARCRecord   `json:"dmarc,omitempty"`
	DKIMSelectors         []DKIMSelector `json:"dkim_selectors,omitempty"`
	MX                    []string       `json:"mx_records,omitempty"`
	OperatorFingerprint   string         `json:"operator_fingerprint"`
	EmailSecurityScore    int            `json:"email_security_score"` // 0-100 composite
	EmailSecurityVerdict  string         `json:"email_security_verdict"`
	Source                string         `json:"source"`
	TookMs                int64          `json:"tookMs"`
	Note                  string         `json:"note,omitempty"`
}

// Common DKIM selectors to probe — order matters (most-likely first).
var commonDKIMSelectors = []string{
	"default", "google", "selector1", "selector2", "s1", "s2", "k1", "k2",
	"mail", "email", "dkim", "smtp", "key1", "key2", "mxvault",
	"sm", "everlytickey1", "everlytickey2", "smtpapi", "mandrill",
	"protonmail", "protonmail2", "protonmail3",
	// Service-specific
	"mailchimp", "sendgrid", "ml", "mlsec", "200608", "20221208",
	"litmus1", "litmus2", "amazonses", "scph0322", "fd1", "fd2",
}

// emailVendorHints maps SPF include domains to vendor names.
var emailVendorHints = map[string]string{
	"_spf.google.com":               "Google Workspace",
	"spf.protection.outlook.com":    "Microsoft 365",
	"sendgrid.net":                  "SendGrid",
	"_spf.salesforce.com":           "Salesforce",
	"servers.mcsv.net":              "Mailchimp",
	"sparkpostmail.com":             "SparkPost",
	"_spf.mailgun.org":              "Mailgun",
	"mailgun.org":                   "Mailgun",
	"amazonses.com":                 "AWS SES",
	"_spf.createsend.com":           "Campaign Monitor",
	"_spf.mailjet.com":              "Mailjet",
	"_spf.intermedia.net":           "Intermedia",
	"_spf.zoho.com":                 "Zoho Mail",
	"zoho.com":                      "Zoho",
	"_spf.fastmail.com":             "Fastmail",
	"_spf.icloud.com":               "iCloud Mail",
	"_spf.cloud.microsoft":          "Microsoft Cloud",
	"_spf.privatemail.com":          "PrivateMail",
	"_spf.helpscout.net":            "Help Scout",
	"_spf.hubspotemail.net":         "HubSpot",
	"_spf.kalix.io":                 "Kalix",
	"_spf.tutanota.com":             "Tutanota",
	"_spf.yandex.net":               "Yandex Mail",
	"klaviyo.com":                   "Klaviyo",
	"klaviyomail.com":               "Klaviyo",
	"_spf.constantcontact.com":      "Constant Contact",
	"_spf.postmarkapp.com":          "Postmark",
	"postmarkapp.com":               "Postmark",
	"_spf.mailoutlet.io":            "Mailoutlet",
	"docusign.com":                  "DocuSign",
	"servers.outboundemail.com":     "Outbound Email",
	"_spf.elasticemail.com":         "Elastic Email",
	"_spf.netcore.co.in":            "Netcore",
	"_spf.salesforceiq.com":         "Salesforce IQ",
	"_spf.fishbowl.com":             "Fishbowl",
	"emarsys.net":                   "Emarsys",
	"_netblocks.mimecast.com":       "Mimecast",
	"mimecast.com":                  "Mimecast",
	"_spfa.proofpoint.com":          "Proofpoint",
	"_spf.barracudanetworks.com":    "Barracuda",
}

func dkimVendorHint(selector string) string {
	switch {
	case selector == "google":
		return "Google Workspace"
	case selector == "selector1" || selector == "selector2":
		return "Microsoft 365"
	case selector == "s1" || selector == "s2":
		return "Generic / SendGrid-like"
	case selector == "amazonses":
		return "AWS SES"
	case selector == "litmus1" || selector == "litmus2":
		return "Litmus"
	case selector == "mandrill":
		return "Mandrill / Mailchimp Transactional"
	case strings.HasPrefix(selector, "protonmail"):
		return "Proton Mail"
	}
	return ""
}

// SPFDMARCChain queries DNS to recover the full email-infrastructure
// fingerprint of a domain:
//   - SPF record + recursive include: chain expansion (max depth 5)
//   - DMARC policy + reporting addresses
//   - DKIM selector probing (~30 common selectors)
//   - MX records
//
// Returns operator_fingerprint string useful for cross-domain ER comparison
// (two domains with identical fingerprints likely share an email team /
// operator).
func SPFDMARCChain(ctx context.Context, input map[string]any) (*SPFDMARCChainOutput, error) {
	domain, _ := input["domain"].(string)
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return nil, errors.New("input.domain required")
	}
	maxDepth := 5
	if v, ok := input["max_spf_depth"].(float64); ok && int(v) > 0 && int(v) <= 10 {
		maxDepth = int(v)
	}
	probeDKIM := true
	if v, ok := input["probe_dkim"].(bool); ok {
		probeDKIM = v
	}

	start := time.Now()
	out := &SPFDMARCChainOutput{Domain: domain, Source: "dns"}

	// Run SPF/DMARC/MX/DKIM in parallel.
	var wg sync.WaitGroup

	// 1. SPF chain
	wg.Add(1)
	go func() {
		defer wg.Done()
		root := lookupSPF(ctx, domain)
		if root == nil {
			return
		}
		out.SPF = root
		visited := map[string]bool{domain: true}
		expanded := []SPFNode{*root}
		queue := append([]string{}, root.Includes...)
		depth := 0
		for len(queue) > 0 && depth < maxDepth {
			next := []string{}
			for _, inc := range queue {
				if visited[inc] {
					continue
				}
				visited[inc] = true
				node := lookupSPF(ctx, inc)
				if node != nil {
					expanded = append(expanded, *node)
					next = append(next, node.Includes...)
				}
			}
			queue = next
			depth++
		}
		out.SPFExpanded = expanded

		// Aggregate
		incSet := map[string]bool{}
		ip4Set := map[string]bool{}
		vendors := map[string]bool{}
		for _, n := range expanded {
			for _, i := range n.Includes {
				incSet[i] = true
				if v, ok := emailVendorHints[i]; ok {
					vendors[v] = true
				}
			}
			for _, ip := range n.IP4 {
				ip4Set[ip] = true
			}
		}
		for k := range incSet {
			out.SPFAllIncludedDomains = append(out.SPFAllIncludedDomains, k)
		}
		sort.Strings(out.SPFAllIncludedDomains)
		for k := range ip4Set {
			out.SPFAllIP4 = append(out.SPFAllIP4, k)
		}
		sort.Strings(out.SPFAllIP4)
		for k := range vendors {
			out.SPFEmailVendors = append(out.SPFEmailVendors, k)
		}
		sort.Strings(out.SPFEmailVendors)
	}()

	// 2. DMARC
	wg.Add(1)
	go func() {
		defer wg.Done()
		out.DMARC = lookupDMARC(ctx, domain)
	}()

	// 3. MX
	wg.Add(1)
	go func() {
		defer wg.Done()
		mxs, err := net.LookupMX(domain)
		if err == nil {
			for _, mx := range mxs {
				out.MX = append(out.MX, fmt.Sprintf("%d %s", mx.Pref, strings.TrimSuffix(mx.Host, ".")))
			}
		}
	}()

	// 4. DKIM
	if probeDKIM {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out.DKIMSelectors = probeDKIMSelectors(ctx, domain)
		}()
	}

	wg.Wait()

	// Build operator fingerprint — sorted unique signal that two domains can compare.
	parts := []string{}
	if out.SPF != nil && out.SPF.Action != "" {
		parts = append(parts, "spf-action:"+out.SPF.Action)
	}
	if len(out.SPFEmailVendors) > 0 {
		parts = append(parts, "vendors:"+strings.Join(out.SPFEmailVendors, "|"))
	}
	if len(out.SPFAllIncludedDomains) > 0 {
		// Use first 3 for fingerprint; full list is in the field
		head := out.SPFAllIncludedDomains
		if len(head) > 3 {
			head = head[:3]
		}
		parts = append(parts, "spf-incl:"+strings.Join(head, ","))
	}
	if out.DMARC != nil && out.DMARC.Policy != "" {
		parts = append(parts, "dmarc:"+out.DMARC.Policy)
	}
	if len(out.MX) > 0 {
		// Take MX hostnames only (no priority)
		mxNames := []string{}
		for _, mx := range out.MX {
			fields := strings.Fields(mx)
			if len(fields) >= 2 {
				mxNames = append(mxNames, fields[1])
			}
		}
		sort.Strings(mxNames)
		parts = append(parts, "mx:"+strings.Join(mxNames, ","))
	}
	out.OperatorFingerprint = strings.Join(parts, "/")

	// Email-security score.
	score := 0
	rationale := []string{}
	if out.SPF != nil {
		switch out.SPF.Action {
		case "-all":
			score += 35
			rationale = append(rationale, "strict SPF (-all)")
		case "~all":
			score += 20
			rationale = append(rationale, "soft-fail SPF (~all)")
		case "?all", "+all":
			rationale = append(rationale, "weak/permissive SPF")
		}
	} else {
		rationale = append(rationale, "no SPF")
	}
	if out.DMARC != nil {
		score += out.DMARC.StrengthScore / 2 // weight half
		rationale = append(rationale, fmt.Sprintf("DMARC=%s", out.DMARC.Policy))
	} else {
		rationale = append(rationale, "no DMARC")
	}
	dkimFound := 0
	for _, d := range out.DKIMSelectors {
		if d.Found {
			dkimFound++
		}
	}
	if dkimFound > 0 {
		score += 15
		rationale = append(rationale, fmt.Sprintf("%d DKIM selectors", dkimFound))
	} else {
		rationale = append(rationale, "no DKIM selectors found")
	}
	if score > 100 {
		score = 100
	}
	out.EmailSecurityScore = score
	switch {
	case score >= 80:
		out.EmailSecurityVerdict = "strong"
	case score >= 50:
		out.EmailSecurityVerdict = "moderate"
	case score >= 20:
		out.EmailSecurityVerdict = "weak"
	default:
		out.EmailSecurityVerdict = "missing"
	}
	out.EmailSecurityVerdict += " — " + strings.Join(rationale, "; ")

	out.TookMs = time.Since(start).Milliseconds()
	if out.SPF == nil && out.DMARC == nil && len(out.MX) == 0 {
		out.Note = "No email infrastructure found — domain may not handle email, or DNS records are misconfigured."
	}
	return out, nil
}

// lookupSPF fetches and parses the SPF record for a domain.
func lookupSPF(ctx context.Context, domain string) *SPFNode {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	txts, err := net.DefaultResolver.LookupTXT(rctx, domain)
	if err != nil {
		return nil
	}
	for _, raw := range txts {
		if !strings.HasPrefix(strings.ToLower(raw), "v=spf1") {
			continue
		}
		node := &SPFNode{Domain: domain, Raw: raw}
		fields := strings.Fields(raw)
		for _, f := range fields[1:] {
			fl := strings.ToLower(f)
			switch {
			case strings.HasPrefix(fl, "include:"):
				node.Includes = append(node.Includes, strings.TrimPrefix(fl, "include:"))
			case strings.HasPrefix(fl, "ip4:"):
				node.IP4 = append(node.IP4, strings.TrimPrefix(fl, "ip4:"))
			case strings.HasPrefix(fl, "ip6:"):
				node.IP6 = append(node.IP6, strings.TrimPrefix(fl, "ip6:"))
			case fl == "a" || strings.HasPrefix(fl, "a:"):
				node.A = append(node.A, fl)
			case fl == "mx" || strings.HasPrefix(fl, "mx:"):
				node.MX = append(node.MX, fl)
			case fl == "-all" || fl == "~all" || fl == "?all" || fl == "+all":
				node.Action = fl
			case strings.HasPrefix(fl, "redirect=") || strings.HasPrefix(fl, "exp="):
				node.Mechanisms = append(node.Mechanisms, fl)
			default:
				node.Mechanisms = append(node.Mechanisms, fl)
			}
		}
		return node
	}
	return nil
}

// lookupDMARC fetches and parses _dmarc.<domain>'s TXT.
func lookupDMARC(ctx context.Context, domain string) *DMARCRecord {
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	txts, err := net.DefaultResolver.LookupTXT(rctx, "_dmarc."+domain)
	if err != nil {
		return nil
	}
	for _, raw := range txts {
		if !strings.HasPrefix(strings.ToLower(raw), "v=dmarc1") {
			continue
		}
		rec := &DMARCRecord{Domain: domain, Raw: raw}
		for _, f := range strings.Split(raw, ";") {
			f = strings.TrimSpace(f)
			fl := strings.ToLower(f)
			switch {
			case strings.HasPrefix(fl, "p="):
				rec.Policy = strings.TrimPrefix(fl, "p=")
			case strings.HasPrefix(fl, "sp="):
				rec.SubPolicy = strings.TrimPrefix(fl, "sp=")
			case strings.HasPrefix(fl, "pct="):
				rec.Pct = strings.TrimPrefix(fl, "pct=")
			case strings.HasPrefix(fl, "rua="):
				v := strings.TrimPrefix(f, "rua=")
				v = strings.TrimPrefix(v, "RUA=")
				for _, m := range strings.Split(v, ",") {
					rec.RUA = append(rec.RUA, strings.TrimSpace(m))
				}
			case strings.HasPrefix(fl, "ruf="):
				v := strings.TrimPrefix(f, "ruf=")
				v = strings.TrimPrefix(v, "RUF=")
				for _, m := range strings.Split(v, ",") {
					rec.RUF = append(rec.RUF, strings.TrimSpace(m))
				}
			case strings.HasPrefix(fl, "adkim="):
				rec.ADKIM = strings.TrimPrefix(fl, "adkim=")
			case strings.HasPrefix(fl, "aspf="):
				rec.ASPF = strings.TrimPrefix(fl, "aspf=")
			}
		}
		// Strength: reject + 100% = 100; quarantine + 100% = 60; none = 10
		score := 0
		switch rec.Policy {
		case "reject":
			score = 100
		case "quarantine":
			score = 60
		case "none":
			score = 10
		}
		if rec.Pct != "" && rec.Pct != "100" {
			score = score * 7 / 10 // partial enforcement penalty
		}
		rec.StrengthScore = score
		return rec
	}
	return nil
}

// probeDKIMSelectors probes ~30 well-known selectors in parallel. Returns
// found ones with optional vendor hint.
func probeDKIMSelectors(ctx context.Context, domain string) []DKIMSelector {
	results := make([]DKIMSelector, len(commonDKIMSelectors))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 12)
	for i, sel := range commonDKIMSelectors {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, s string) {
			defer wg.Done()
			defer func() { <-sem }()
			rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			txts, err := net.DefaultResolver.LookupTXT(rctx, s+"._domainkey."+domain)
			r := DKIMSelector{Selector: s}
			if err == nil && len(txts) > 0 {
				for _, t := range txts {
					if strings.Contains(strings.ToLower(t), "v=dkim1") || strings.Contains(strings.ToLower(t), "k=rsa") || strings.Contains(strings.ToLower(t), "p=") {
						r.Found = true
						r.Raw = t
						if len(r.Raw) > 200 {
							r.Raw = r.Raw[:200] + "…"
						}
						r.Vendor = dkimVendorHint(s)
						break
					}
				}
			}
			results[idx] = r
		}(i, sel)
	}
	wg.Wait()
	// Filter to found ones only.
	out := []DKIMSelector{}
	for _, r := range results {
		if r.Found {
			out = append(out, r)
		}
	}
	return out
}
