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

type TrackerCorrelateOverlap struct {
	Platform   string   `json:"platform"`
	SharedIDs  []string `json:"shared_ids"`
	Strength   string   `json:"strength"` // strong | medium | weak
	OnlyOnA    []string `json:"only_on_a,omitempty"`
	OnlyOnB    []string `json:"only_on_b,omitempty"`
}

type TrackerCorrelateOutput struct {
	URLA              string                    `json:"url_a"`
	URLB              string                    `json:"url_b"`
	OverlapScore      int                       `json:"overlap_score"`       // 0-100
	OperatorVerdict   string                    `json:"operator_verdict"`    // same | likely-same | unrelated | inconclusive
	OperatorRationale string                    `json:"operator_rationale"`
	Overlaps          []TrackerCorrelateOverlap `json:"overlaps"`
	SharedThirdParty  []string                  `json:"shared_third_party_domains,omitempty"`
	OnlyOnA           map[string][]string       `json:"only_on_a,omitempty"`
	OnlyOnB           map[string][]string       `json:"only_on_b,omitempty"`
	ATotal            int                       `json:"a_total_ids"`
	BTotal            int                       `json:"b_total_ids"`
	Source            string                    `json:"source"`
	TookMs            int64                     `json:"tookMs"`
}

// TrackerCorrelate scores how likely two URLs share an operator by comparing
// their tracker fingerprints. Internal calls to TrackerExtract for both URLs
// in parallel; computes set overlap on each platform; weighs each platform
// by its identity-binding strength.
//
// Verdict logic:
//   - Any STRONG-strength platform overlap (GA UA-/G-, GTM-, FB pixel,
//     Hotjar, LinkedIn pid, Shopify shop) → "same operator" (near-certain).
//   - Multiple medium overlaps without strong → "likely same".
//   - Only weak overlaps (shared 3rd-party CDN domains) → "unrelated".
//   - Both sites tracker-free → "inconclusive".
//
// 100% free; no external dependencies beyond the two HTTP fetches that
// TrackerExtract already does. This is the SOTA "are these sites the same
// operator?" primitive — used by Bellingcat-style investigative journalism
// and brand-protection workflows.
func TrackerCorrelate(ctx context.Context, input map[string]any) (*TrackerCorrelateOutput, error) {
	urlA, _ := input["url_a"].(string)
	urlB, _ := input["url_b"].(string)
	urlA = strings.TrimSpace(urlA)
	urlB = strings.TrimSpace(urlB)
	if urlA == "" || urlB == "" {
		return nil, errors.New("input.url_a and input.url_b are both required")
	}

	start := time.Now()

	// Parallel extraction.
	var aRes, bRes *TrackerExtractOutput
	var aErr, bErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		aRes, aErr = TrackerExtract(ctx, map[string]any{"url": urlA})
	}()
	go func() {
		defer wg.Done()
		bRes, bErr = TrackerExtract(ctx, map[string]any{"url": urlB})
	}()
	wg.Wait()

	if aErr != nil {
		return nil, fmt.Errorf("tracker_extract failed for url_a: %w", aErr)
	}
	if bErr != nil {
		return nil, fmt.Errorf("tracker_extract failed for url_b: %w", bErr)
	}

	// Map of strong-strength platforms (kept consistent with tracker_extract).
	strongPlatforms := map[string]bool{
		"google_analytics_universal": true,
		"google_analytics_4":         true,
		"google_tag_manager":         true,
		"google_adsense":             false, // medium
		"facebook_pixel":             true,
		"hotjar":                     true,
		"linkedin_insight":           true,
		"shopify_id":                 true,
	}
	// Medium-strength = anything in tracker_extract's medium category. We just
	// treat everything else as medium for simplicity.

	allPlatforms := map[string]bool{}
	for p := range aRes.Trackers {
		allPlatforms[p] = true
	}
	for p := range bRes.Trackers {
		allPlatforms[p] = true
	}

	overlaps := []TrackerCorrelateOverlap{}
	onlyOnA := map[string][]string{}
	onlyOnB := map[string][]string{}
	totalScore := 0
	strongOverlap := false
	mediumOverlapCount := 0

	platformList := make([]string, 0, len(allPlatforms))
	for p := range allPlatforms {
		platformList = append(platformList, p)
	}
	sort.Strings(platformList)

	for _, p := range platformList {
		aSet := map[string]bool{}
		bSet := map[string]bool{}
		for _, id := range aRes.Trackers[p] {
			aSet[id] = true
		}
		for _, id := range bRes.Trackers[p] {
			bSet[id] = true
		}
		shared := []string{}
		oa := []string{}
		ob := []string{}
		for id := range aSet {
			if bSet[id] {
				shared = append(shared, id)
			} else {
				oa = append(oa, id)
			}
		}
		for id := range bSet {
			if !aSet[id] {
				ob = append(ob, id)
			}
		}
		sort.Strings(shared)
		sort.Strings(oa)
		sort.Strings(ob)
		strength := "medium"
		if strongPlatforms[p] {
			strength = "strong"
		}

		if len(shared) > 0 {
			overlaps = append(overlaps, TrackerCorrelateOverlap{
				Platform:  p,
				SharedIDs: shared,
				Strength:  strength,
				OnlyOnA:   oa,
				OnlyOnB:   ob,
			})
			if strength == "strong" {
				strongOverlap = true
				totalScore += 50 * len(shared) // strong overlap → big score boost
			} else {
				mediumOverlapCount++
				totalScore += 15 * len(shared)
			}
		}
		if len(oa) > 0 {
			onlyOnA[p] = oa
		}
		if len(ob) > 0 {
			onlyOnB[p] = ob
		}
	}

	// 3rd-party domain overlap (weak signal — shared CDN/SaaS doesn't bind operator).
	aDomains := map[string]bool{}
	for _, d := range aRes.OutboundDomains {
		aDomains[d] = true
	}
	shared3p := []string{}
	for _, d := range bRes.OutboundDomains {
		if aDomains[d] {
			shared3p = append(shared3p, d)
		}
	}
	sort.Strings(shared3p)

	if totalScore > 100 {
		totalScore = 100
	}

	verdict := "inconclusive"
	rationale := ""
	if strongOverlap {
		verdict = "same"
		rationale = "Strong-strength tracker ID match (GA/GTM/FB/Hotjar/LinkedIn/Shopify) — same operator near-certain. These IDs are bound to a single account and don't roam between operators."
	} else if mediumOverlapCount >= 2 {
		verdict = "likely-same"
		rationale = fmt.Sprintf("Multiple medium-strength tracker overlaps (%d platforms) — likely same operator but verify with WHOIS / cert / DNS overlap.", mediumOverlapCount)
	} else if mediumOverlapCount == 1 {
		verdict = "likely-same"
		rationale = "Single medium-strength tracker overlap — possible shared operator, but could be a 3rd-party widget. Verify with additional signals."
	} else if aRes.UniqueIDsCount == 0 && bRes.UniqueIDsCount == 0 {
		verdict = "inconclusive"
		rationale = "Neither site exposed tracker IDs in initial HTML (likely SPA-rendered). Try chaining via firecrawl_scrape first."
	} else if aRes.UniqueIDsCount > 0 && bRes.UniqueIDsCount > 0 {
		verdict = "unrelated"
		rationale = "Both sites had tracker IDs but no overlaps — distinct operators."
	} else {
		verdict = "inconclusive"
		rationale = "One site had no detectable tracker IDs — overlap inference impossible."
	}

	return &TrackerCorrelateOutput{
		URLA:              urlA,
		URLB:              urlB,
		OverlapScore:      totalScore,
		OperatorVerdict:   verdict,
		OperatorRationale: rationale,
		Overlaps:          overlaps,
		SharedThirdParty:  shared3p,
		OnlyOnA:           onlyOnA,
		OnlyOnB:           onlyOnB,
		ATotal:            aRes.UniqueIDsCount,
		BTotal:            bRes.UniqueIDsCount,
		Source:            "tracker_correlate",
		TookMs:            time.Since(start).Milliseconds(),
	}, nil
}
