package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GovTrackSearch queries the GovTrack.us free no-auth API for US
// congressional intelligence:
//
//   - "bill_search"   : full-text + filter by congress/status/sponsor
//                       across every bill since 1971
//   - "bill_detail"   : by bill_id (numeric internal id) → full record
//                       with sponsor + citations + text URLs
//   - "vote_recent"   : most-recent floor votes (by chamber/congress) with
//                       related-bill linkage and tally
//   - "member_search" : Senators/Representatives by name/state/party/role
//                       — returns bioguide_id (Congressional canonical),
//                       OpenSecrets osid (campaign finance), C-SPAN id,
//                       Twitter, YouTube — these IDs are cross-reference
//                       keys into the rest of the OSINT graph.
//
// Free, no auth.

type GTBill struct {
	ID                 int      `json:"id,omitempty"`
	Title              string   `json:"title"`
	TitleNoNumber      string   `json:"title_without_number,omitempty"`
	BillType           string   `json:"bill_type,omitempty"`
	BillTypeLabel      string   `json:"bill_type_label,omitempty"`
	Congress           int      `json:"congress,omitempty"`
	Number             int      `json:"number,omitempty"`
	DisplayNumber      string   `json:"display_number,omitempty"`
	CurrentStatus      string   `json:"current_status,omitempty"`
	CurrentStatusLabel string   `json:"current_status_label,omitempty"`
	CurrentStatusDate  string   `json:"current_status_date,omitempty"`
	IntroducedDate     string   `json:"introduced_date,omitempty"`
	Sponsor            string   `json:"sponsor,omitempty"`
	IsAlive            bool     `json:"is_alive,omitempty"`
	Link               string   `json:"link,omitempty"`
	GPOPdfURL          string   `json:"gpo_pdf_url,omitempty"`
	NumPages           int      `json:"num_pages,omitempty"`
	Citations          []string `json:"citations,omitempty"`
}

type GTMember struct {
	BioguideID    string `json:"bioguide_id"`
	Name          string `json:"name"`
	FirstName     string `json:"first_name,omitempty"`
	LastName      string `json:"last_name,omitempty"`
	Nickname      string `json:"nickname,omitempty"`
	Birthday      string `json:"birthday,omitempty"`
	Gender        string `json:"gender,omitempty"`
	CSPANID       int    `json:"cspan_id,omitempty"`
	OpenSecretsID string `json:"opensecrets_id,omitempty"`
	TwitterID     string `json:"twitter,omitempty"`
	YouTubeID     string `json:"youtube,omitempty"`
	Link          string `json:"link,omitempty"`

	// Current role (populated if from /role endpoint or via join)
	RoleType        string `json:"role_type,omitempty"`
	RoleTypeLabel   string `json:"role_type_label,omitempty"`
	State           string `json:"state,omitempty"`
	District        int    `json:"district,omitempty"`
	Party           string `json:"party,omitempty"`
	LeadershipTitle string `json:"leadership_title,omitempty"`
	StartDate       string `json:"role_start_date,omitempty"`
	EndDate         string `json:"role_end_date,omitempty"`
}

type GTVote struct {
	Chamber     string  `json:"chamber"`
	Congress    int     `json:"congress"`
	Session     string  `json:"session"`
	Number      int     `json:"number"`
	Question    string  `json:"question"`
	Result      string  `json:"result"`
	TotalPlus   int     `json:"yes_count"`
	TotalMinus  int     `json:"no_count"`
	TotalOther  int     `json:"other_count,omitempty"`
	Created     string  `json:"created"`
	Link        string  `json:"link"`
	RelatedBill *GTBill `json:"related_bill,omitempty"`
}

type GovTrackSearchOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query,omitempty"`
	TotalCount        int        `json:"total_count,omitempty"`
	Returned          int        `json:"returned"`
	Bills             []GTBill   `json:"bills,omitempty"`
	Bill              *GTBill    `json:"bill,omitempty"`
	Members           []GTMember `json:"members,omitempty"`
	Votes             []GTVote   `json:"votes,omitempty"`
	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
	Note              string     `json:"note,omitempty"`
}

func GovTrackSearch(ctx context.Context, input map[string]any) (*GovTrackSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["bill_id"]; ok {
			mode = "bill_detail"
		} else if v, ok := input["query"].(string); ok && v != "" {
			mode = "bill_search"
		} else if _, ok := input["state"]; ok {
			mode = "member_search"
		} else if _, ok := input["last_name"]; ok {
			mode = "member_search"
		} else {
			mode = "vote_recent"
		}
	}

	out := &GovTrackSearchOutput{
		Mode:   mode,
		Source: "govtrack.us/api/v2",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "bill_search":
		q, _ := input["query"].(string)
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, fmt.Errorf("input.query required for bill_search")
		}
		out.Query = q
		params := url.Values{}
		params.Set("q", q)
		if v, ok := input["congress"].(float64); ok && v > 0 {
			params.Set("congress", fmt.Sprintf("%d", int(v)))
		}
		if v, ok := input["current_status"].(string); ok && v != "" {
			params.Set("current_status", v)
		}
		limit := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			limit = int(l)
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
		if v, ok := input["order_by"].(string); ok && v != "" {
			params.Set("order_by", v)
		} else {
			params.Set("order_by", "-current_status_date")
		}
		body, err := gtGet(ctx, cli, "https://www.govtrack.us/api/v2/bill?"+params.Encode())
		if err != nil {
			return nil, err
		}
		if err := decodeGTBillSearch(body, out); err != nil {
			return nil, err
		}

	case "bill_detail":
		idAny := input["bill_id"]
		idStr := fmt.Sprintf("%v", idAny)
		idStr = strings.TrimSpace(idStr)
		if idStr == "" || idStr == "<nil>" {
			return nil, fmt.Errorf("input.bill_id required for bill_detail (numeric internal id)")
		}
		out.Query = idStr
		body, err := gtGet(ctx, cli, "https://www.govtrack.us/api/v2/bill/"+idStr)
		if err != nil {
			return nil, err
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("bill detail decode: %w", err)
		}
		bill := convertGTBill(raw)
		out.Bill = &bill
		out.Returned = 1

	case "vote_recent":
		params := url.Values{}
		params.Set("order_by", "-created")
		congress := 119 // current Congress as of 2026
		if v, ok := input["congress"].(float64); ok && v > 0 {
			congress = int(v)
		}
		params.Set("congress", fmt.Sprintf("%d", congress))
		if v, ok := input["chamber"].(string); ok && v != "" {
			params.Set("chamber", v)
		}
		limit := 15
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			limit = int(l)
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
		out.Query = "most-recent floor votes"
		body, err := gtGet(ctx, cli, "https://www.govtrack.us/api/v2/vote?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			Meta struct {
				TotalCount int `json:"total_count"`
			} `json:"meta"`
			Objects []map[string]any `json:"objects"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("votes decode: %w", err)
		}
		out.TotalCount = raw.Meta.TotalCount
		for _, v := range raw.Objects {
			out.Votes = append(out.Votes, convertGTVote(v))
		}
		out.Returned = len(out.Votes)

	case "member_search":
		// Two-stage: hit /role for current members with state/party/chamber filters,
		// fall back to /person?q=lastname when name-search is requested
		params := url.Values{}
		current := true
		if v, ok := input["current_only"].(bool); ok {
			current = v
		}
		if current {
			params.Set("current", "true")
		}
		if v, ok := input["state"].(string); ok && v != "" {
			params.Set("state", strings.ToUpper(v))
		}
		if v, ok := input["role_type"].(string); ok && v != "" {
			params.Set("role_type", v) // "senator" | "representative"
		}
		if v, ok := input["party"].(string); ok && v != "" {
			params.Set("party", v)
		}
		// Did the caller search by name? Use /person endpoint instead.
		var lastNameQ string
		if v, ok := input["last_name"].(string); ok && strings.TrimSpace(v) != "" {
			lastNameQ = strings.TrimSpace(v)
		}
		if v, ok := input["query"].(string); ok && strings.TrimSpace(v) != "" {
			lastNameQ = strings.TrimSpace(v)
		}
		limit := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			limit = int(l)
		}
		out.Query = lastNameQ
		if lastNameQ != "" {
			personParams := url.Values{}
			personParams.Set("q", lastNameQ)
			personParams.Set("limit", fmt.Sprintf("%d", limit))
			body, err := gtGet(ctx, cli, "https://www.govtrack.us/api/v2/person?"+personParams.Encode())
			if err != nil {
				return nil, err
			}
			var raw struct {
				Meta struct {
					TotalCount int `json:"total_count"`
				} `json:"meta"`
				Objects []map[string]any `json:"objects"`
			}
			if err := json.Unmarshal(body, &raw); err != nil {
				return nil, fmt.Errorf("person decode: %w", err)
			}
			out.TotalCount = raw.Meta.TotalCount
			for _, p := range raw.Objects {
				out.Members = append(out.Members, convertGTMemberFromPerson(p))
			}
		} else {
			params.Set("limit", fmt.Sprintf("%d", limit))
			body, err := gtGet(ctx, cli, "https://www.govtrack.us/api/v2/role?"+params.Encode())
			if err != nil {
				return nil, err
			}
			var raw struct {
				Meta struct {
					TotalCount int `json:"total_count"`
				} `json:"meta"`
				Objects []map[string]any `json:"objects"`
			}
			if err := json.Unmarshal(body, &raw); err != nil {
				return nil, fmt.Errorf("role decode: %w", err)
			}
			out.TotalCount = raw.Meta.TotalCount
			for _, r := range raw.Objects {
				out.Members = append(out.Members, convertGTMemberFromRole(r))
			}
		}
		out.Returned = len(out.Members)

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: bill_search, bill_detail, vote_recent, member_search", mode)
	}

	out.HighlightFindings = buildGovTrackHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// ---------- Helpers ----------

func decodeGTBillSearch(body []byte, out *GovTrackSearchOutput) error {
	var raw struct {
		Meta struct {
			TotalCount int `json:"total_count"`
		} `json:"meta"`
		Objects []map[string]any `json:"objects"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("bill search decode: %w", err)
	}
	out.TotalCount = raw.Meta.TotalCount
	for _, b := range raw.Objects {
		out.Bills = append(out.Bills, convertGTBill(b))
	}
	out.Returned = len(out.Bills)
	return nil
}

func convertGTBill(raw map[string]any) GTBill {
	b := GTBill{
		Title:              gtString(raw, "title"),
		TitleNoNumber:      gtString(raw, "title_without_number"),
		BillType:           gtString(raw, "bill_type"),
		BillTypeLabel:      gtString(raw, "bill_type_label"),
		Congress:           gtInt(raw, "congress"),
		Number:             gtInt(raw, "number"),
		DisplayNumber:      gtString(raw, "display_number"),
		CurrentStatus:      gtString(raw, "current_status"),
		CurrentStatusLabel: gtString(raw, "current_status_label"),
		CurrentStatusDate:  gtString(raw, "current_status_date"),
		IntroducedDate:     gtString(raw, "introduced_date"),
		IsAlive:            gtBool(raw, "is_alive"),
		Link:               gtString(raw, "link"),
		ID:                 gtInt(raw, "id"),
	}
	// Sponsor — can be nested object or numeric id
	if sp, ok := raw["sponsor"]; ok {
		switch v := sp.(type) {
		case map[string]any:
			b.Sponsor = gtString(v, "name")
		case float64:
			b.Sponsor = fmt.Sprintf("(person id %d)", int(v))
		}
	}
	// Text info: extract gpo_pdf_url + numpages
	if ti, ok := raw["text_info"].(map[string]any); ok {
		b.GPOPdfURL = gtString(ti, "gpo_pdf_url")
		b.NumPages = gtInt(ti, "numpages")
		if cites, ok := ti["citations"].([]any); ok {
			for _, c := range cites {
				if cm, ok := c.(map[string]any); ok {
					if t := gtString(cm, "text"); t != "" {
						b.Citations = append(b.Citations, t)
					}
				}
			}
		}
	}
	return b
}

func convertGTMemberFromPerson(p map[string]any) GTMember {
	link := gtString(p, "link")
	bioguide := gtString(p, "bioguideid")
	return GTMember{
		BioguideID:    bioguide,
		Name:          gtString(p, "name"),
		FirstName:     gtString(p, "firstname"),
		LastName:      gtString(p, "lastname"),
		Nickname:      gtString(p, "nickname"),
		Birthday:      gtString(p, "birthday"),
		Gender:        gtString(p, "gender_label"),
		CSPANID:       gtInt(p, "cspanid"),
		OpenSecretsID: gtString(p, "osid"),
		TwitterID:     gtString(p, "twitterid"),
		YouTubeID:     gtString(p, "youtubeid"),
		Link:          link,
	}
}

func convertGTMemberFromRole(r map[string]any) GTMember {
	m := GTMember{
		RoleType:        gtString(r, "role_type"),
		RoleTypeLabel:   gtString(r, "role_type_label"),
		State:           gtString(r, "state"),
		District:        gtInt(r, "district"),
		Party:           gtString(r, "party"),
		LeadershipTitle: gtString(r, "leadership_title"),
		StartDate:       gtString(r, "startdate"),
		EndDate:         gtString(r, "enddate"),
	}
	if pp, ok := r["person"].(map[string]any); ok {
		m.BioguideID = gtString(pp, "bioguideid")
		m.Name = gtString(pp, "name")
		m.FirstName = gtString(pp, "firstname")
		m.LastName = gtString(pp, "lastname")
		m.Nickname = gtString(pp, "nickname")
		m.Birthday = gtString(pp, "birthday")
		m.Gender = gtString(pp, "gender_label")
		m.CSPANID = gtInt(pp, "cspanid")
		m.OpenSecretsID = gtString(pp, "osid")
		m.TwitterID = gtString(pp, "twitterid")
		m.YouTubeID = gtString(pp, "youtubeid")
		m.Link = gtString(pp, "link")
	}
	return m
}

func convertGTVote(v map[string]any) GTVote {
	out := GTVote{
		Chamber:    gtString(v, "chamber"),
		Congress:   gtInt(v, "congress"),
		Session:    gtString(v, "session"),
		Number:     gtInt(v, "number"),
		Question:   gtString(v, "question"),
		Result:     gtString(v, "result"),
		TotalPlus:  gtInt(v, "total_plus"),
		TotalMinus: gtInt(v, "total_minus"),
		TotalOther: gtInt(v, "total_other"),
		Created:    gtString(v, "created"),
		Link:       gtString(v, "link"),
	}
	if rb, ok := v["related_bill"].(map[string]any); ok {
		bill := convertGTBill(rb)
		out.RelatedBill = &bill
	}
	return out
}

// gtString extracts a string from m[key] across the wide range of value
// shapes that real upstream APIs serve. Mirrors the iter-4 gtFloat fix
// for the symmetric defect on the string side.
//
// Handled shapes:
//   - string (verbatim)
//   - nil → ""
//   - bool → "true" / "false"
//   - all integer types → base-10 string (no scientific notation)
//   - float64/float32 → trimmed numeric (so 123.0 → "123", 1.5 → "1.5",
//     not "123.000000" or "1.500000")
//   - json.Number → its String() form (preserves the upstream's
//     original numeric textual representation)
//
// Maps and slices return "" (callers that need them should fetch the
// raw value directly). See TestGtString_BroadTypeCoverageQuantitative
// for the proof.
func gtString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int8:
		return strconv.FormatInt(int64(x), 10)
	case int16:
		return strconv.FormatInt(int64(x), 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint8:
		return strconv.FormatUint(uint64(x), 10)
	case uint16:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case json.Number:
		return x.String()
	}
	return ""
}

// gtInt extracts an integer from m[key] across the wide range of value
// shapes that real upstream APIs serve. Mirrors the iter-4 gtFloat
// and iter-8 gtString fixes.
//
// Float values are truncated toward zero (the standard Go int conversion).
// String values are parsed via strconv.ParseInt with comma stripping and
// trailing-percent stripping. bool true → 1, false → 0.
//
// See TestGtInt_BroadTypeCoverageQuantitative for the proof.
func gtInt(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case float32:
		return int(x)
	case int:
		return x
	case int8:
		return int(x)
	case int16:
		return int(x)
	case int32:
		return int(x)
	case int64:
		return int(x)
	case uint:
		return int(x)
	case uint8:
		return int(x)
	case uint16:
		return int(x)
	case uint32:
		return int(x)
	case uint64:
		return int(x)
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i)
		}
		if f, err := x.Float64(); err == nil {
			return int(f)
		}
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		s = strings.ReplaceAll(s, ",", "")
		s = strings.TrimSuffix(s, "%")
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return int(i)
		}
		// Fall back to float (handles "42.5" → 42, "1.5e3" → 1500)
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f)
		}
	}
	return 0
}

// gtBool extracts a boolean from m[key] across shapes real upstreams
// emit. The legacy version only handled native `bool`; APIs commonly
// serve booleans as int 0/1 (RapidAPI, MarineTraffic) or strings
// "true"/"false"/"yes"/"no"/"1"/"0" (form-encoded responses, CSV
// imports). See TestGtBool_BroadTypeCoverageQuantitative.
func gtBool(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		// non-zero numbers are truthy
		return gtInt(map[string]any{"v": v}, "v") != 0
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i != 0
		}
		if f, err := x.Float64(); err == nil {
			return f != 0
		}
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		switch s {
		case "true", "yes", "y", "t", "1", "on":
			return true
		case "false", "no", "n", "f", "0", "off", "":
			return false
		}
	}
	return false
}

func gtGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("govtrack: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("govtrack HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildGovTrackHighlights(o *GovTrackSearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "bill_search":
		hi = append(hi, fmt.Sprintf("✓ %d bills match '%s' (returning %d, newest first)", o.TotalCount, o.Query, o.Returned))
		// Status breakdown
		statusCount := map[string]int{}
		for _, b := range o.Bills {
			statusCount[b.CurrentStatus]++
		}
		statuses := make([]string, 0, len(statusCount))
		for s := range statusCount {
			statuses = append(statuses, s)
		}
		sort.SliceStable(statuses, func(i, j int) bool { return statusCount[statuses[i]] > statusCount[statuses[j]] })
		breakdown := []string{}
		for _, s := range statuses {
			breakdown = append(breakdown, fmt.Sprintf("%s×%d", s, statusCount[s]))
		}
		if len(breakdown) > 0 {
			hi = append(hi, "  by status: "+strings.Join(breakdown, "  "))
		}
		for i, b := range o.Bills {
			if i >= 6 {
				break
			}
			alive := ""
			if !b.IsAlive {
				alive = " [DEAD]"
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s — %s · sponsor: %s · status: %s%s",
				b.IntroducedDate, b.DisplayNumber, hfTruncate(b.TitleNoNumber, 80), b.Sponsor, b.CurrentStatusLabel, alive))
		}

	case "bill_detail":
		if o.Bill == nil {
			hi = append(hi, fmt.Sprintf("✗ no bill for id %s", o.Query))
			break
		}
		b := o.Bill
		hi = append(hi, fmt.Sprintf("✓ %s — %s", b.DisplayNumber, hfTruncate(b.TitleNoNumber, 100)))
		hi = append(hi, fmt.Sprintf("  congress: %d · introduced: %s · current status: %s (%s)", b.Congress, b.IntroducedDate, b.CurrentStatusLabel, b.CurrentStatusDate))
		if b.Sponsor != "" {
			hi = append(hi, "  sponsor: "+b.Sponsor)
		}
		if b.GPOPdfURL != "" {
			hi = append(hi, fmt.Sprintf("  text: %s (%d pages)", b.GPOPdfURL, b.NumPages))
		}
		if len(b.Citations) > 0 {
			hi = append(hi, "  citations: "+strings.Join(b.Citations, ", "))
		}

	case "vote_recent":
		hi = append(hi, fmt.Sprintf("✓ %d recent floor votes", o.Returned))
		for i, v := range o.Votes {
			if i >= 8 {
				break
			}
			created := v.Created
			if len(created) > 16 {
				created = created[:16]
			}
			tally := fmt.Sprintf("%d–%d", v.TotalPlus, v.TotalMinus)
			rel := ""
			if v.RelatedBill != nil && v.RelatedBill.DisplayNumber != "" {
				rel = " · " + v.RelatedBill.DisplayNumber
			}
			hi = append(hi, fmt.Sprintf("  • [%s] %s #%d %s — %s — result: %s%s", created, v.Chamber, v.Number, hfTruncate(v.Question, 60), tally, v.Result, rel))
		}

	case "member_search":
		hi = append(hi, fmt.Sprintf("✓ %d members match '%s' (total %d)", o.Returned, o.Query, o.TotalCount))
		for i, m := range o.Members {
			if i >= 8 {
				break
			}
			role := m.RoleTypeLabel
			if role == "" {
				role = m.RoleType
			}
			geo := ""
			if m.State != "" {
				geo = " " + m.State
				if m.District > 0 {
					geo += fmt.Sprintf("-%d", m.District)
				}
				if m.Party != "" {
					geo += " " + m.Party
				}
			}
			led := ""
			if m.LeadershipTitle != "" {
				led = " · " + m.LeadershipTitle
			}
			ids := []string{}
			if m.BioguideID != "" {
				ids = append(ids, "bioguide:"+m.BioguideID)
			}
			if m.OpenSecretsID != "" {
				ids = append(ids, "opensecrets:"+m.OpenSecretsID)
			}
			if m.TwitterID != "" {
				ids = append(ids, "@"+m.TwitterID)
			}
			idStr := ""
			if len(ids) > 0 {
				idStr = " · " + strings.Join(ids, " · ")
			}
			hi = append(hi, fmt.Sprintf("  • %s%s%s%s", m.Name, geo, led, idStr))
		}
	}
	return hi
}
