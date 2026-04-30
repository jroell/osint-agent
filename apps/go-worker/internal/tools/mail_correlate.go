package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MailCorrelateOutput struct {
	DomainA              string   `json:"domain_a"`
	DomainB              string   `json:"domain_b"`
	OverlapScore         int      `json:"overlap_score"`        // 0-100
	OperatorVerdict      string   `json:"operator_verdict"`     // same | likely-same | shared-vendor | unrelated | inconclusive
	OperatorRationale    string   `json:"operator_rationale"`
	SharedSPFIncludes    []string `json:"shared_spf_includes,omitempty"`
	SharedSPFVendors     []string `json:"shared_spf_vendors,omitempty"`
	SharedMXHostnames    []string `json:"shared_mx_hostnames,omitempty"`
	SharedMXApex         []string `json:"shared_mx_apex,omitempty"`         // e.g. both end in google.com
	SharedDKIMSelectors  []string `json:"shared_dkim_selectors,omitempty"`
	SharedDMARCRua       []string `json:"shared_dmarc_rua_addresses,omitempty"` // **smoking gun**
	SharedDMARCRuf       []string `json:"shared_dmarc_ruf_addresses,omitempty"`
	OnlyOnA              MailFingerprint `json:"only_on_a"`
	OnlyOnB              MailFingerprint `json:"only_on_b"`
	FingerprintA         string   `json:"fingerprint_a"`
	FingerprintB         string   `json:"fingerprint_b"`
	FingerprintMatch     bool     `json:"fingerprint_exact_match"`
	Source               string   `json:"source"`
	TookMs               int64    `json:"tookMs"`
	Note                 string   `json:"note,omitempty"`
}

type MailFingerprint struct {
	SPFIncludes []string `json:"spf_includes,omitempty"`
	Vendors     []string `json:"vendors,omitempty"`
	MX          []string `json:"mx,omitempty"`
	DKIM        []string `json:"dkim_selectors,omitempty"`
}

func mxApex(mx string) string {
	// "1 aspmx.l.google.com" → "google.com"
	parts := strings.Fields(mx)
	if len(parts) >= 2 {
		return apexDomain(parts[1])
	}
	return apexDomain(mx)
}

func mxHost(mx string) string {
	parts := strings.Fields(mx)
	if len(parts) >= 2 {
		return strings.ToLower(strings.TrimSuffix(parts[1], "."))
	}
	return strings.ToLower(strings.TrimSuffix(mx, "."))
}

// MailCorrelate scores how likely two domains share an email operator by
// running spf_dmarc_chain on each in parallel and computing overlap on:
//   - SPF expanded include set
//   - SPF email-vendor detections
//   - MX records (full hostname AND apex)
//   - DKIM selector set
//   - DMARC reporting addresses (rua, ruf) — strongest single signal
//
// Verdict:
//   - "same"          : DMARC rua/ruf match (smoking gun)
//                       OR exact fingerprint match
//   - "likely-same"   : Multi-layer overlap (≥3 of: SPF includes, MX, DKIM, vendors)
//   - "shared-vendor" : Same vendor stack but no other strong signal (e.g. both
//                       use Google Workspace + SendGrid — common for SaaS)
//   - "unrelated"     : Different vendors AND different MX
//   - "inconclusive"  : Insufficient data (one or both have no email infra)
func MailCorrelate(ctx context.Context, input map[string]any) (*MailCorrelateOutput, error) {
	dA, _ := input["domain_a"].(string)
	dB, _ := input["domain_b"].(string)
	dA = strings.TrimSpace(strings.ToLower(dA))
	dB = strings.TrimSpace(strings.ToLower(dB))
	if dA == "" || dB == "" {
		return nil, errors.New("input.domain_a and input.domain_b both required")
	}

	probeDKIM := true
	if v, ok := input["probe_dkim"].(bool); ok {
		probeDKIM = v
	}

	start := time.Now()

	// Parallel chain analysis.
	var aRes, bRes *SPFDMARCChainOutput
	var aErr, bErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		aRes, aErr = SPFDMARCChain(ctx, map[string]any{"domain": dA, "probe_dkim": probeDKIM})
	}()
	go func() {
		defer wg.Done()
		bRes, bErr = SPFDMARCChain(ctx, map[string]any{"domain": dB, "probe_dkim": probeDKIM})
	}()
	wg.Wait()

	if aErr != nil {
		return nil, fmt.Errorf("spf_dmarc_chain failed for domain_a: %w", aErr)
	}
	if bErr != nil {
		return nil, fmt.Errorf("spf_dmarc_chain failed for domain_b: %w", bErr)
	}

	out := &MailCorrelateOutput{
		DomainA: dA, DomainB: dB,
		FingerprintA: aRes.OperatorFingerprint,
		FingerprintB: bRes.OperatorFingerprint,
		FingerprintMatch: aRes.OperatorFingerprint != "" && aRes.OperatorFingerprint == bRes.OperatorFingerprint,
		Source: "mail_correlate",
	}

	// SPF includes overlap
	aSpfIncludes := stringSet(aRes.SPFAllIncludedDomains)
	bSpfIncludes := stringSet(bRes.SPFAllIncludedDomains)
	for inc := range aSpfIncludes {
		if bSpfIncludes[inc] {
			out.SharedSPFIncludes = append(out.SharedSPFIncludes, inc)
		}
	}
	sort.Strings(out.SharedSPFIncludes)

	// Vendor overlap
	aVendors := stringSet(aRes.SPFEmailVendors)
	bVendors := stringSet(bRes.SPFEmailVendors)
	for v := range aVendors {
		if bVendors[v] {
			out.SharedSPFVendors = append(out.SharedSPFVendors, v)
		}
	}
	sort.Strings(out.SharedSPFVendors)

	// MX overlap (full hostname AND apex)
	aMxHosts := map[string]bool{}
	bMxHosts := map[string]bool{}
	aMxApexes := map[string]bool{}
	bMxApexes := map[string]bool{}
	for _, mx := range aRes.MX {
		aMxHosts[mxHost(mx)] = true
		aMxApexes[mxApex(mx)] = true
	}
	for _, mx := range bRes.MX {
		bMxHosts[mxHost(mx)] = true
		bMxApexes[mxApex(mx)] = true
	}
	for h := range aMxHosts {
		if bMxHosts[h] {
			out.SharedMXHostnames = append(out.SharedMXHostnames, h)
		}
	}
	for a := range aMxApexes {
		if bMxApexes[a] {
			out.SharedMXApex = append(out.SharedMXApex, a)
		}
	}
	sort.Strings(out.SharedMXHostnames)
	sort.Strings(out.SharedMXApex)

	// DKIM overlap
	aDkim := dkimSet(aRes.DKIMSelectors)
	bDkim := dkimSet(bRes.DKIMSelectors)
	for k := range aDkim {
		if bDkim[k] {
			out.SharedDKIMSelectors = append(out.SharedDKIMSelectors, k)
		}
	}
	sort.Strings(out.SharedDKIMSelectors)

	// DMARC reporting overlap — STRONGEST single signal
	if aRes.DMARC != nil && bRes.DMARC != nil {
		aRua := stringSet(aRes.DMARC.RUA)
		bRua := stringSet(bRes.DMARC.RUA)
		for v := range aRua {
			if bRua[v] {
				out.SharedDMARCRua = append(out.SharedDMARCRua, v)
			}
		}
		aRuf := stringSet(aRes.DMARC.RUF)
		bRuf := stringSet(bRes.DMARC.RUF)
		for v := range aRuf {
			if bRuf[v] {
				out.SharedDMARCRuf = append(out.SharedDMARCRuf, v)
			}
		}
		sort.Strings(out.SharedDMARCRua)
		sort.Strings(out.SharedDMARCRuf)
	}

	// only_on_a / only_on_b
	for s := range aSpfIncludes {
		if !bSpfIncludes[s] {
			out.OnlyOnA.SPFIncludes = append(out.OnlyOnA.SPFIncludes, s)
		}
	}
	for s := range bSpfIncludes {
		if !aSpfIncludes[s] {
			out.OnlyOnB.SPFIncludes = append(out.OnlyOnB.SPFIncludes, s)
		}
	}
	for v := range aVendors {
		if !bVendors[v] {
			out.OnlyOnA.Vendors = append(out.OnlyOnA.Vendors, v)
		}
	}
	for v := range bVendors {
		if !aVendors[v] {
			out.OnlyOnB.Vendors = append(out.OnlyOnB.Vendors, v)
		}
	}
	for k := range aDkim {
		if !bDkim[k] {
			out.OnlyOnA.DKIM = append(out.OnlyOnA.DKIM, k)
		}
	}
	for k := range bDkim {
		if !aDkim[k] {
			out.OnlyOnB.DKIM = append(out.OnlyOnB.DKIM, k)
		}
	}
	for h := range aMxHosts {
		if !bMxHosts[h] {
			out.OnlyOnA.MX = append(out.OnlyOnA.MX, h)
		}
	}
	for h := range bMxHosts {
		if !aMxHosts[h] {
			out.OnlyOnB.MX = append(out.OnlyOnB.MX, h)
		}
	}
	sort.Strings(out.OnlyOnA.SPFIncludes)
	sort.Strings(out.OnlyOnB.SPFIncludes)
	sort.Strings(out.OnlyOnA.Vendors)
	sort.Strings(out.OnlyOnB.Vendors)
	sort.Strings(out.OnlyOnA.DKIM)
	sort.Strings(out.OnlyOnB.DKIM)
	sort.Strings(out.OnlyOnA.MX)
	sort.Strings(out.OnlyOnB.MX)

	// Score & verdict.
	score := 0
	rationale := []string{}

	// DMARC rua/ruf is the smoking gun.
	if len(out.SharedDMARCRua) > 0 || len(out.SharedDMARCRuf) > 0 {
		score += 50
		rationale = append(rationale, fmt.Sprintf("shared DMARC reporting address (smoking gun): %s%s",
			strings.Join(out.SharedDMARCRua, ","), strings.Join(out.SharedDMARCRuf, ",")))
	}
	if out.FingerprintMatch && aRes.OperatorFingerprint != "" {
		score += 50
		rationale = append(rationale, "exact operator_fingerprint match")
	}
	// Multi-layer overlap counts.
	multilayerCount := 0
	if len(out.SharedSPFIncludes) > 0 {
		score += 10 + 5*minInt(len(out.SharedSPFIncludes), 4)
		multilayerCount++
		rationale = append(rationale, fmt.Sprintf("%d shared SPF includes", len(out.SharedSPFIncludes)))
	}
	if len(out.SharedSPFVendors) > 0 {
		score += 5 * len(out.SharedSPFVendors)
		multilayerCount++
		rationale = append(rationale, fmt.Sprintf("%d shared email vendors (%s)", len(out.SharedSPFVendors), strings.Join(out.SharedSPFVendors, ",")))
	}
	if len(out.SharedMXHostnames) > 0 {
		score += 15
		multilayerCount++
		rationale = append(rationale, fmt.Sprintf("%d shared MX hostnames", len(out.SharedMXHostnames)))
	} else if len(out.SharedMXApex) > 0 {
		score += 5
		rationale = append(rationale, fmt.Sprintf("MX share apex (%s) but distinct hosts", strings.Join(out.SharedMXApex, ",")))
	}
	if len(out.SharedDKIMSelectors) > 0 {
		score += 5 * len(out.SharedDKIMSelectors)
		multilayerCount++
		rationale = append(rationale, fmt.Sprintf("%d shared DKIM selectors", len(out.SharedDKIMSelectors)))
	}

	if score > 100 {
		score = 100
	}
	out.OverlapScore = score

	// Verdict logic.
	verdict := "inconclusive"
	switch {
	case len(out.SharedDMARCRua) > 0 || len(out.SharedDMARCRuf) > 0:
		verdict = "same"
	case out.FingerprintMatch:
		verdict = "same"
	case multilayerCount >= 3 && len(out.SharedMXHostnames) > 0:
		verdict = "likely-same"
	case multilayerCount >= 2 && len(out.SharedSPFIncludes) >= 2:
		verdict = "likely-same"
	case len(out.SharedSPFVendors) >= 2 && len(out.SharedSPFIncludes) >= 1:
		verdict = "shared-vendor"
	case len(out.SharedSPFVendors) > 0:
		verdict = "shared-vendor"
	case (aRes.SPF == nil && len(aRes.MX) == 0) || (bRes.SPF == nil && len(bRes.MX) == 0):
		verdict = "inconclusive"
	default:
		verdict = "unrelated"
	}
	out.OperatorVerdict = verdict
	out.OperatorRationale = strings.Join(rationale, "; ")
	if out.OperatorRationale == "" {
		out.OperatorRationale = "No overlapping email-infrastructure signals between the two domains."
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func stringSet(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[strings.ToLower(strings.TrimSpace(x))] = true
	}
	delete(m, "")
	return m
}

func dkimSet(ds []DKIMSelector) map[string]bool {
	m := map[string]bool{}
	for _, d := range ds {
		if d.Found {
			m[d.Selector] = true
		}
	}
	return m
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
