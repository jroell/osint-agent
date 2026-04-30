package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// USASpendingSearch wraps USAspending.gov — every US federal contract,
// grant, loan, and direct payment since 2008. Free, no auth.
//
// **Closes the federal political-OSINT chain**:
//   GovTrack (bills) → Federal Register (regs) → LDA (lobbying)
//   → USASpending (who got paid by the government)
//   → SEC EDGAR (publicly-traded recipient corp filings)
//
// Two modes:
//   - "search_awards" : by recipient name / agency / keyword / date /
//                        award type filters → matching awards with
//                        recipient + agency + description + amount +
//                        award type. Includes client-side aggregations
//                        (total amount, top recipients, top agencies).
//   - "award_detail"  : by award ID → full record (subawards, recipient
//                        parent, place of performance).
//
// Award type codes (used to filter): contracts (A,B,C,D for delivery
// orders / IDV / definitive contracts / purchase orders), grants
// (02 = block, 03 = formula, 04 = project, 05 = cooperative agreement),
// loans (07 = direct loan, 08 = loan guarantee), other (06 = direct
// payment, 09/10/11 = other financial assistance).

type USASpendingAward struct {
	InternalID         int64   `json:"internal_id"`
	GeneratedID        string  `json:"generated_internal_id,omitempty"`
	AwardID            string  `json:"award_id,omitempty"`
	RecipientName      string  `json:"recipient_name,omitempty"`
	ActionDate         string  `json:"action_date,omitempty"`
	AwardingAgency     string  `json:"awarding_agency,omitempty"`
	AwardingSubAgency  string  `json:"awarding_sub_agency,omitempty"`
	AgencySlug         string  `json:"agency_slug,omitempty"`
	Description        string  `json:"description,omitempty"`
	AwardAmount        float64 `json:"award_amount,omitempty"`
	AwardType          string  `json:"award_type,omitempty"`
	URL                string  `json:"url,omitempty"`
}

type USASpendingAggregation struct {
	Key   string  `json:"key"`
	Count int     `json:"count"`
	Total float64 `json:"total_usd"`
}

type USASpendingSearchOutput struct {
	Mode              string                   `json:"mode"`
	Query             string                   `json:"query,omitempty"`
	HasMore           bool                     `json:"has_more,omitempty"`
	Returned          int                      `json:"returned"`
	Awards            []USASpendingAward       `json:"awards,omitempty"`
	Award             *USASpendingAward        `json:"award,omitempty"`

	TotalAmount       float64                  `json:"total_amount_usd,omitempty"`
	TopRecipients     []USASpendingAggregation `json:"top_recipients,omitempty"`
	TopAgencies       []USASpendingAggregation `json:"top_agencies,omitempty"`
	UniqueAwardTypes  []string                 `json:"unique_award_types,omitempty"`

	HighlightFindings []string                 `json:"highlight_findings"`
	Source            string                   `json:"source"`
	TookMs            int64                    `json:"tookMs"`
	Note              string                   `json:"note,omitempty"`
}

func USASpendingSearch(ctx context.Context, input map[string]any) (*USASpendingSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if _, ok := input["award_id"]; ok {
			mode = "award_detail"
		} else {
			mode = "search_awards"
		}
	}

	out := &USASpendingSearchOutput{
		Mode:   mode,
		Source: "api.usaspending.gov",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	switch mode {
	case "search_awards":
		// Build filters object
		filters := map[string]any{}
		queryParts := []string{}
		if v, ok := input["recipient"].(string); ok && v != "" {
			filters["recipient_search_text"] = []string{v}
			queryParts = append(queryParts, "recipient="+v)
		}
		if v, ok := input["keyword"].(string); ok && v != "" {
			filters["keywords"] = []string{v}
			queryParts = append(queryParts, "keyword="+v)
		}
		if v, ok := input["agency"].(string); ok && v != "" {
			// agency_search filters by awarding agency name
			filters["agencies"] = []map[string]string{{"type": "awarding", "tier": "toptier", "name": v}}
			queryParts = append(queryParts, "agency="+v)
		}
		// Award type codes
		typeCodes, _ := input["award_type_codes"].([]any)
		codes := []string{}
		for _, t := range typeCodes {
			if s, ok := t.(string); ok && s != "" {
				codes = append(codes, s)
			}
		}
		if len(codes) == 0 {
			// Default: contracts (A,B,C,D)
			codes = []string{"A", "B", "C", "D"}
		}
		filters["award_type_codes"] = codes
		// Date range — required by API
		startDate, _ := input["start_date"].(string)
		endDate, _ := input["end_date"].(string)
		if startDate == "" {
			startDate = "2020-01-01"
		}
		if endDate == "" {
			endDate = time.Now().Format("2006-01-02")
		}
		filters["time_period"] = []map[string]string{
			{"start_date": startDate, "end_date": endDate},
		}
		queryParts = append(queryParts, fmt.Sprintf("dates=%s..%s", startDate, endDate))
		out.Query = strings.Join(queryParts, " · ")
		if len(queryParts) <= 1 {
			return nil, fmt.Errorf("at least one of recipient, keyword, or agency required")
		}

		limit := 25
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 100 {
			limit = int(l)
		}
		body := map[string]any{
			"filters": filters,
			"fields": []string{
				"Award ID", "Recipient Name", "Action Date",
				"Awarding Agency", "Awarding Sub Agency",
				"Description", "Award Amount", "Award Type",
			},
			"page":  1,
			"limit": limit,
			"sort":  "Award Amount",
			"order": "desc",
		}
		bodyBytes, _ := json.Marshal(body)
		respBody, err := usaspendingPost(ctx, cli, "https://api.usaspending.gov/api/v2/search/spending_by_award/", bodyBytes)
		if err != nil {
			return nil, err
		}
		var raw struct {
			Results      []map[string]any `json:"results"`
			PageMetadata map[string]any   `json:"page_metadata"`
		}
		if err := json.Unmarshal(respBody, &raw); err != nil {
			return nil, fmt.Errorf("usaspending decode: %w", err)
		}
		if hm, ok := raw.PageMetadata["hasNext"].(bool); ok {
			out.HasMore = hm
		}
		recipientCount := map[string]float64{}
		recipientHits := map[string]int{}
		agencyCount := map[string]float64{}
		agencyHits := map[string]int{}
		typeSet := map[string]struct{}{}
		for _, r := range raw.Results {
			a := USASpendingAward{
				InternalID:        int64(gtInt(r, "internal_id")),
				GeneratedID:       gtString(r, "generated_internal_id"),
				AwardID:           gtString(r, "Award ID"),
				RecipientName:     gtString(r, "Recipient Name"),
				ActionDate:        gtString(r, "Action Date"),
				AwardingAgency:    gtString(r, "Awarding Agency"),
				AwardingSubAgency: gtString(r, "Awarding Sub Agency"),
				AgencySlug:        gtString(r, "agency_slug"),
				Description:       hfTruncate(gtString(r, "Description"), 200),
				AwardAmount:       gtFloat(r, "Award Amount"),
				AwardType:         gtString(r, "Award Type"),
			}
			if a.GeneratedID != "" {
				a.URL = "https://www.usaspending.gov/award/" + a.GeneratedID
			}
			out.Awards = append(out.Awards, a)
			out.TotalAmount += a.AwardAmount
			if a.RecipientName != "" {
				recipientCount[a.RecipientName] += a.AwardAmount
				recipientHits[a.RecipientName]++
			}
			if a.AwardingAgency != "" {
				agencyCount[a.AwardingAgency] += a.AwardAmount
				agencyHits[a.AwardingAgency]++
			}
			if a.AwardType != "" {
				typeSet[a.AwardType] = struct{}{}
			}
		}
		out.Returned = len(out.Awards)
		// Build aggregations
		for k, v := range recipientCount {
			out.TopRecipients = append(out.TopRecipients, USASpendingAggregation{
				Key: k, Count: recipientHits[k], Total: v,
			})
		}
		sort.SliceStable(out.TopRecipients, func(i, j int) bool { return out.TopRecipients[i].Total > out.TopRecipients[j].Total })
		if len(out.TopRecipients) > 10 {
			out.TopRecipients = out.TopRecipients[:10]
		}
		for k, v := range agencyCount {
			out.TopAgencies = append(out.TopAgencies, USASpendingAggregation{
				Key: k, Count: agencyHits[k], Total: v,
			})
		}
		sort.SliceStable(out.TopAgencies, func(i, j int) bool { return out.TopAgencies[i].Total > out.TopAgencies[j].Total })
		if len(out.TopAgencies) > 10 {
			out.TopAgencies = out.TopAgencies[:10]
		}
		for k := range typeSet {
			out.UniqueAwardTypes = append(out.UniqueAwardTypes, k)
		}
		sort.Strings(out.UniqueAwardTypes)

	case "award_detail":
		idAny := input["award_id"]
		idStr := fmt.Sprintf("%v", idAny)
		idStr = strings.TrimSpace(idStr)
		if idStr == "" || idStr == "<nil>" {
			return nil, fmt.Errorf("input.award_id required (the generated_internal_id, e.g. 'CONT_AWD_...' or 'ASST_NON_...')")
		}
		out.Query = idStr
		body, err := usaspendingGet(ctx, cli, "https://api.usaspending.gov/api/v2/awards/"+idStr+"/")
		if err != nil {
			return nil, err
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("award decode: %w", err)
		}
		a := &USASpendingAward{
			GeneratedID:   idStr,
			AwardID:       gtString(raw, "piid"),
			Description:   hfTruncate(gtString(raw, "description"), 400),
			AwardAmount:   gtFloat(raw, "total_obligation"),
		}
		// Recipient is nested
		if rec, ok := raw["recipient"].(map[string]any); ok {
			a.RecipientName = gtString(rec, "recipient_name")
		}
		// Awarding agency is nested
		if ag, ok := raw["awarding_agency"].(map[string]any); ok {
			if to, ok := ag["toptier_agency"].(map[string]any); ok {
				a.AwardingAgency = gtString(to, "name")
			}
			if su, ok := ag["subtier_agency"].(map[string]any); ok {
				a.AwardingSubAgency = gtString(su, "name")
			}
		}
		// Period of performance
		if pop, ok := raw["period_of_performance"].(map[string]any); ok {
			a.ActionDate = gtString(pop, "start_date")
		}
		a.URL = "https://www.usaspending.gov/award/" + idStr
		out.Award = a
		out.Returned = 1

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search_awards, award_detail", mode)
	}

	out.HighlightFindings = buildUSASpendingHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func usaspendingPost(ctx context.Context, cli *http.Client, urlStr string, body []byte) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", urlStr, bytes.NewReader(body))
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usaspending: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("usaspending HTTP %d: %s", resp.StatusCode, hfTruncate(string(respBody), 200))
	}
	return respBody, nil
}

func usaspendingGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("usaspending: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("usaspending HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildUSASpendingHighlights(o *USASpendingSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search_awards":
		hi = append(hi, fmt.Sprintf("✓ %d awards returned for %s", o.Returned, o.Query))
		if o.HasMore {
			hi = append(hi, "  ⏭️  more results available — narrow filters or paginate")
		}
		if o.TotalAmount > 0 {
			hi = append(hi, fmt.Sprintf("  💰 total amount in result set: %s", formatUSD(o.TotalAmount)))
		}
		if len(o.UniqueAwardTypes) > 0 {
			hi = append(hi, "  award types: "+strings.Join(o.UniqueAwardTypes, ", "))
		}
		if len(o.TopRecipients) > 0 {
			hi = append(hi, "  💵 top recipients:")
			for i, r := range o.TopRecipients {
				if i >= 5 {
					break
				}
				hi = append(hi, fmt.Sprintf("    %s — %s (%d award%s)", r.Key, formatUSD(r.Total), r.Count, plural(r.Count)))
			}
		}
		if len(o.TopAgencies) > 0 {
			hi = append(hi, "  🏛️  top awarding agencies:")
			for i, a := range o.TopAgencies {
				if i >= 5 {
					break
				}
				hi = append(hi, fmt.Sprintf("    %s — %s (%d award%s)", a.Key, formatUSD(a.Total), a.Count, plural(a.Count)))
			}
		}
		for i, a := range o.Awards {
			if i >= 5 {
				break
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s → %s — %s — %s",
				a.ActionDate, a.AwardingAgency, a.RecipientName, formatUSD(a.AwardAmount), hfTruncate(a.Description, 80)))
		}

	case "award_detail":
		if o.Award == nil {
			hi = append(hi, fmt.Sprintf("✗ no award found for %s", o.Query))
			break
		}
		a := o.Award
		hi = append(hi, fmt.Sprintf("✓ Award %s — %s", a.AwardID, formatUSD(a.AwardAmount)))
		hi = append(hi, fmt.Sprintf("  recipient: %s · agency: %s / %s", a.RecipientName, a.AwardingAgency, a.AwardingSubAgency))
		if a.ActionDate != "" {
			hi = append(hi, "  performance start: "+a.ActionDate)
		}
		if a.Description != "" {
			hi = append(hi, "  description: "+hfTruncate(a.Description, 200))
		}
		hi = append(hi, "  url: "+a.URL)
	}
	return hi
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
