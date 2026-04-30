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

// CFPBComplaintsSearch queries the Consumer Financial Protection Bureau's
// public consumer-complaints database — every complaint filed against a
// US financial-services company since 2011. ~14.8M records. Free, no auth.
//
// Why this is unique ER:
//   - The only public dataset of consumer-facing financial-services issues
//     by company. No other catalog tool covers this.
//   - Every complaint has product + sub_product + issue + sub_issue
//     taxonomy, plus optional consumer narrative. The taxonomy reveals
//     what specific products/services drive complaint volume.
//   - Company response field shows whether the dispute was "Closed with
//     monetary relief", "Closed with non-monetary relief", or "Closed
//     with explanation" (default brush-off response).
//   - Pairs with `sec_edgar_search` (corp filings — public co material
//     consumer harm), `documentcloud_search` (CFPB enforcement actions),
//     `propublica_nonprofit` (consumer-advocacy nonprofits filing about
//     same patterns).
//
// Two modes:
//
//   - "search"          : by company / search_term / product / state /
//                         date range / submitted_via / has_narrative /
//                         response_type filters. Returns matching
//                         complaints + client-side aggregations of
//                         product/issue/state distribution from the
//                         returned set.
//   - "complaint_detail": by complaint_id → full record (including the
//                         consumer's free-text narrative if disclosed).

type CFPBComplaint struct {
	ComplaintID            string `json:"complaint_id"`
	DateReceived           string `json:"date_received,omitempty"`
	DateSentToCompany      string `json:"date_sent_to_company,omitempty"`
	Company                string `json:"company,omitempty"`
	Product                string `json:"product,omitempty"`
	SubProduct             string `json:"sub_product,omitempty"`
	Issue                  string `json:"issue,omitempty"`
	SubIssue               string `json:"sub_issue,omitempty"`
	State                  string `json:"state,omitempty"`
	ZipCode                string `json:"zip_code,omitempty"`
	Tags                   string `json:"tags,omitempty"` // e.g. "Servicemember", "Older American"
	HasNarrative           bool   `json:"has_narrative,omitempty"`
	ConsumerNarrative      string `json:"consumer_narrative,omitempty"`
	CompanyResponse        string `json:"company_response,omitempty"`
	CompanyPublicResponse  string `json:"company_public_response,omitempty"`
	Timely                 string `json:"timely,omitempty"`
	SubmittedVia           string `json:"submitted_via,omitempty"`
	ConsumerDisputed       string `json:"consumer_disputed,omitempty"`
}

type CFPBAggregation struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type CFPBComplaintsSearchOutput struct {
	Mode               string             `json:"mode"`
	Query              string             `json:"query,omitempty"`
	TotalCount         int                `json:"total_count,omitempty"`
	Returned           int                `json:"returned"`
	Complaints         []CFPBComplaint    `json:"complaints,omitempty"`
	Complaint          *CFPBComplaint     `json:"complaint,omitempty"`

	// Client-side aggregations across returned set (capped at limit, so
	// representative not authoritative for very large queries)
	ByProduct          []CFPBAggregation  `json:"by_product,omitempty"`
	ByIssue            []CFPBAggregation  `json:"by_issue,omitempty"`
	ByState            []CFPBAggregation  `json:"by_state,omitempty"`
	ByCompanyResponse  []CFPBAggregation  `json:"by_company_response,omitempty"`
	WithNarrativeCount int                `json:"with_narrative_count,omitempty"`
	TimelyCount        int                `json:"timely_count,omitempty"`
	UntimelyCount      int                `json:"untimely_count,omitempty"`

	HighlightFindings  []string           `json:"highlight_findings"`
	Source             string             `json:"source"`
	TookMs             int64              `json:"tookMs"`
	Note               string             `json:"note,omitempty"`
}

func CFPBComplaintsSearch(ctx context.Context, input map[string]any) (*CFPBComplaintsSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if _, ok := input["complaint_id"]; ok {
			mode = "complaint_detail"
		} else {
			mode = "search"
		}
	}

	out := &CFPBComplaintsSearchOutput{
		Mode:   mode,
		Source: "consumerfinance.gov/data-research/consumer-complaints",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	switch mode {
	case "search":
		params := url.Values{}
		// CFPB uses `company` for exact match, `search_term` for full-text
		queryDesc := []string{}
		if v, ok := input["company"].(string); ok && v != "" {
			params.Set("company", v)
			queryDesc = append(queryDesc, "company="+v)
		}
		if v, ok := input["search_term"].(string); ok && v != "" {
			params.Set("search_term", v)
			queryDesc = append(queryDesc, "term="+v)
		}
		if v, ok := input["query"].(string); ok && v != "" && len(queryDesc) == 0 {
			// Generic query → search_term
			params.Set("search_term", v)
			queryDesc = append(queryDesc, "term="+v)
		}
		if v, ok := input["product"].(string); ok && v != "" {
			params.Set("product", v)
			queryDesc = append(queryDesc, "product="+v)
		}
		if v, ok := input["sub_product"].(string); ok && v != "" {
			params.Set("sub_product", v)
		}
		if v, ok := input["issue"].(string); ok && v != "" {
			params.Set("issue", v)
			queryDesc = append(queryDesc, "issue="+v)
		}
		if v, ok := input["state"].(string); ok && v != "" {
			params.Set("state", strings.ToUpper(v))
			queryDesc = append(queryDesc, "state="+v)
		}
		if v, ok := input["date_received_min"].(string); ok && v != "" {
			params.Set("date_received_min", v)
			queryDesc = append(queryDesc, "after="+v)
		}
		if v, ok := input["date_received_max"].(string); ok && v != "" {
			params.Set("date_received_max", v)
			queryDesc = append(queryDesc, "before="+v)
		}
		if v, ok := input["submitted_via"].(string); ok && v != "" {
			params.Set("submitted_via", v) // Web | Phone | Postal mail | Email | Fax | Referral
			queryDesc = append(queryDesc, "via="+v)
		}
		if v, ok := input["has_narrative"].(bool); ok && v {
			params.Set("has_narrative", "true")
			queryDesc = append(queryDesc, "has_narrative")
		}
		if v, ok := input["company_response"].(string); ok && v != "" {
			params.Set("company_response", v)
			queryDesc = append(queryDesc, "response="+v)
		}
		if v, ok := input["timely"].(string); ok && v != "" {
			params.Set("timely", v)
			queryDesc = append(queryDesc, "timely="+v)
		}
		if v, ok := input["tags"].(string); ok && v != "" {
			params.Set("tags", v) // "Servicemember", "Older American"
			queryDesc = append(queryDesc, "tag="+v)
		}
		if len(queryDesc) == 0 {
			return nil, fmt.Errorf("at least one filter required: company, search_term, query, product, issue, state, tags, timely, dates, etc.")
		}
		out.Query = strings.Join(queryDesc, " · ")

		size := 25
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 100 {
			size = int(l)
		}
		params.Set("size", fmt.Sprintf("%d", size))
		// Default: newest first
		sortBy := "created_date_desc"
		if v, ok := input["sort"].(string); ok && v != "" {
			sortBy = v
		}
		params.Set("sort", sortBy)
		// Intentionally omit format=json — that triggers a 200MB+ flat-array
		// stream of the entire matched corpus. The default ES-envelope
		// shape honors size= properly.

		body, err := cfpbGet(ctx, cli, "https://www.consumerfinance.gov/data-research/consumer-complaints/search/api/v1/?"+params.Encode())
		if err != nil {
			return nil, err
		}
		if err := decodeCFPBSearch(body, out); err != nil {
			return nil, err
		}

	case "complaint_detail":
		idAny := input["complaint_id"]
		idStr := fmt.Sprintf("%v", idAny)
		idStr = strings.TrimSpace(idStr)
		if idStr == "" || idStr == "<nil>" {
			return nil, fmt.Errorf("input.complaint_id required (numeric ID)")
		}
		out.Query = idStr
		// CFPB doesn't have a per-id endpoint — search by complaint_id
		params := url.Values{}
		params.Set("search_term", idStr)
		params.Set("size", "1")
		// Intentionally omit format=json — that triggers a 200MB+ flat-array
		// stream of the entire matched corpus. The default ES-envelope
		// shape honors size= properly.
		body, err := cfpbGet(ctx, cli, "https://www.consumerfinance.gov/data-research/consumer-complaints/search/api/v1/?"+params.Encode())
		if err != nil {
			return nil, err
		}
		if err := decodeCFPBSearch(body, out); err != nil {
			return nil, err
		}
		// Find the complaint with the matching ID
		for i := range out.Complaints {
			if out.Complaints[i].ComplaintID == idStr {
				cp := out.Complaints[i]
				out.Complaint = &cp
				break
			}
		}
		out.Complaints = nil
		if out.Complaint == nil {
			out.Note = fmt.Sprintf("complaint_id %s not found in search results — IDs may have rotated or be invalid", idStr)
		}
		out.Returned = 0
		if out.Complaint != nil {
			out.Returned = 1
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search, complaint_detail", mode)
	}

	out.HighlightFindings = buildCFPBHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func decodeCFPBSearch(body []byte, out *CFPBComplaintsSearchOutput) error {
	// Elasticsearch-style envelope
	var raw struct {
		Hits struct {
			Total struct{ Value int `json:"value"` } `json:"total"`
			Hits  []struct {
				ID     string         `json:"_id"`
				Source map[string]any `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("CFPB decode: %w", err)
	}
	out.TotalCount = raw.Hits.Total.Value

	productCount := map[string]int{}
	issueCount := map[string]int{}
	stateCount := map[string]int{}
	respCount := map[string]int{}
	for _, h := range raw.Hits.Hits {
		s := h.Source
		c := CFPBComplaint{
			ComplaintID:           gtString(s, "complaint_id"),
			DateReceived:          gtString(s, "date_received"),
			DateSentToCompany:     gtString(s, "date_sent_to_company"),
			Company:               gtString(s, "company"),
			Product:               gtString(s, "product"),
			SubProduct:            gtString(s, "sub_product"),
			Issue:                 gtString(s, "issue"),
			SubIssue:              gtString(s, "sub_issue"),
			State:                 gtString(s, "state"),
			ZipCode:               gtString(s, "zip_code"),
			Tags:                  gtString(s, "tags"),
			ConsumerNarrative:     hfTruncate(gtString(s, "complaint_what_happened"), 1500),
			HasNarrative:          gtBool(s, "has_narrative"),
			CompanyResponse:       gtString(s, "company_response"),
			CompanyPublicResponse: gtString(s, "company_public_response"),
			Timely:                gtString(s, "timely"),
			SubmittedVia:          gtString(s, "submitted_via"),
			ConsumerDisputed:      gtString(s, "consumer_disputed"),
		}
		// Strip time-of-day from dates
		if len(c.DateReceived) > 10 {
			c.DateReceived = c.DateReceived[:10]
		}
		if len(c.DateSentToCompany) > 10 {
			c.DateSentToCompany = c.DateSentToCompany[:10]
		}
		out.Complaints = append(out.Complaints, c)

		if c.Product != "" {
			productCount[c.Product]++
		}
		if c.Issue != "" {
			issueCount[c.Issue]++
		}
		if c.State != "" {
			stateCount[c.State]++
		}
		if c.CompanyResponse != "" {
			respCount[c.CompanyResponse]++
		}
		if c.HasNarrative {
			out.WithNarrativeCount++
		}
		switch c.Timely {
		case "Yes":
			out.TimelyCount++
		case "No":
			out.UntimelyCount++
		}
	}
	out.Returned = len(out.Complaints)
	out.ByProduct = topAggregations(productCount, 8)
	out.ByIssue = topAggregations(issueCount, 8)
	out.ByState = topAggregations(stateCount, 8)
	out.ByCompanyResponse = topAggregations(respCount, 8)
	return nil
}

func topAggregations(m map[string]int, n int) []CFPBAggregation {
	out := make([]CFPBAggregation, 0, len(m))
	for k, v := range m {
		out = append(out, CFPBAggregation{Key: k, Count: v})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func cfpbGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("CFPB: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("CFPB HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildCFPBHighlights(o *CFPBComplaintsSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search":
		hi = append(hi, fmt.Sprintf("✓ %d complaints match %s (returning %d, newest first)", o.TotalCount, o.Query, o.Returned))
		// Aggregations
		if len(o.ByProduct) > 0 {
			parts := []string{}
			for i, p := range o.ByProduct {
				if i >= 4 {
					break
				}
				parts = append(parts, fmt.Sprintf("%s×%d", hfTruncate(p.Key, 30), p.Count))
			}
			hi = append(hi, "  📊 by product (top 4 in result set): "+strings.Join(parts, "  "))
		}
		if len(o.ByIssue) > 0 {
			parts := []string{}
			for i, p := range o.ByIssue {
				if i >= 4 {
					break
				}
				parts = append(parts, fmt.Sprintf("%s×%d", hfTruncate(p.Key, 30), p.Count))
			}
			hi = append(hi, "  ⚠️  by issue (top 4): "+strings.Join(parts, "  "))
		}
		if len(o.ByCompanyResponse) > 0 {
			parts := []string{}
			for _, p := range o.ByCompanyResponse {
				parts = append(parts, fmt.Sprintf("%s×%d", p.Key, p.Count))
			}
			hi = append(hi, "  💬 company responses: "+strings.Join(parts, "  "))
		}
		if o.WithNarrativeCount > 0 {
			hi = append(hi, fmt.Sprintf("  📝 %d/%d include consumer narrative", o.WithNarrativeCount, o.Returned))
		}
		if o.UntimelyCount > 0 {
			hi = append(hi, fmt.Sprintf("  ⏰ %d/%d untimely company response", o.UntimelyCount, o.Returned))
		}
		// Sample complaints
		for i, c := range o.Complaints {
			if i >= 5 {
				break
			}
			loc := ""
			if c.State != "" {
				loc = c.State
				if c.ZipCode != "" && c.ZipCode != "0" {
					loc += " " + c.ZipCode
				}
			}
			tag := ""
			if c.Tags != "" {
				tag = " [" + c.Tags + "]"
			}
			narr := ""
			if c.HasNarrative && c.ConsumerNarrative != "" {
				narr = " — " + hfTruncate(c.ConsumerNarrative, 150)
			}
			hi = append(hi, fmt.Sprintf("  • [%s %s] %s — %s / %s — %s%s%s",
				c.DateReceived, loc, hfTruncate(c.Company, 30), hfTruncate(c.Product, 30), hfTruncate(c.Issue, 40), c.CompanyResponse, tag, narr))
		}

	case "complaint_detail":
		if o.Complaint == nil {
			hi = append(hi, fmt.Sprintf("✗ no complaint found for id %s", o.Query))
			break
		}
		c := o.Complaint
		hi = append(hi, fmt.Sprintf("✓ Complaint %s — %s", c.ComplaintID, c.Company))
		hi = append(hi, fmt.Sprintf("  received: %s · sent to company: %s · state: %s %s · submitted via: %s",
			c.DateReceived, c.DateSentToCompany, c.State, c.ZipCode, c.SubmittedVia))
		hi = append(hi, fmt.Sprintf("  product: %s · sub: %s", c.Product, c.SubProduct))
		hi = append(hi, fmt.Sprintf("  issue: %s · sub: %s", c.Issue, c.SubIssue))
		if c.Tags != "" {
			hi = append(hi, "  tags: "+c.Tags)
		}
		hi = append(hi, fmt.Sprintf("  response: %s · timely: %s", c.CompanyResponse, c.Timely))
		if c.CompanyPublicResponse != "" {
			hi = append(hi, "  public statement: "+hfTruncate(c.CompanyPublicResponse, 200))
		}
		if c.HasNarrative && c.ConsumerNarrative != "" {
			hi = append(hi, "  consumer narrative: "+hfTruncate(c.ConsumerNarrative, 500))
		}
	}
	return hi
}
