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

// WaybackHistorySnapshot is a slim CDX row.
type WaybackHistorySnapshot struct {
	Timestamp     string `json:"timestamp"`           // YYYYMMDDHHMMSS
	ISODate       string `json:"iso_date,omitempty"`  // YYYY-MM-DD
	URL           string `json:"url"`
	StatusCode    string `json:"status_code,omitempty"`
	MimeType      string `json:"mime_type,omitempty"`
	Digest        string `json:"digest,omitempty"`
	SnapshotURL   string `json:"snapshot_url,omitempty"`
}

// WaybackYearAggregate counts snapshots per year.
type WaybackYearAggregate struct {
	Year  int `json:"year"`
	Count int `json:"count"`
}

// WaybackURLHistoryOutput is the response.
type WaybackURLHistoryOutput struct {
	URL                   string             `json:"url"`
	TotalSnapshots        int                `json:"total_snapshots"`
	UniqueContentVersions int                `json:"unique_content_versions"`
	FirstSeen             *WaybackHistorySnapshot   `json:"first_seen,omitempty"`
	LastSeen              *WaybackHistorySnapshot   `json:"last_seen,omitempty"`
	UniqueDigestSnapshots []WaybackHistorySnapshot  `json:"unique_digest_snapshots,omitempty"`
	YearlyDistribution    []WaybackYearAggregate `json:"yearly_distribution,omitempty"`
	StatusCodeBreakdown   map[string]int     `json:"status_code_breakdown,omitempty"`
	MimeTypeBreakdown     map[string]int     `json:"mime_type_breakdown,omitempty"`
	YearGaps              []string           `json:"year_gaps,omitempty"`
	HighlightFindings     []string           `json:"highlight_findings"`
	Source                string             `json:"source"`
	TookMs                int64              `json:"tookMs"`
	Note                  string             `json:"note,omitempty"`
}

// WaybackURLHistory queries Wayback's CDX API for full snapshot timeline of
// a URL or domain. Free, no auth. Strong temporal-recon ER tool.
//
// Why this matters for ER:
//   - First-seen date establishes domain age. If anthropic.com has snapshots
//     from 2013, but Anthropic was founded in 2021, the domain had a
//     PREVIOUS OWNER — a strong ownership-transition signal.
//   - Last-seen date detects dead domains (parking page, takedown).
//   - "Unique content versions" via digest collapse counts how many times
//     the page substantively changed — high churn = active site, low
//     churn after long activity = abandoned.
//   - Yearly distribution reveals **gaps** (year with zero snapshots = the
//     domain was offline/redirected/parked that year).
//   - Status code breakdown reveals 4xx/5xx history (DDoS-induced outages,
//     migration windows).
//   - Pairs with `wayback_endpoint_extract` (which extracts paths from
//     archived JS bundles) and `wayback` (which retrieves a specific
//     archived page).
func WaybackURLHistory(ctx context.Context, input map[string]any) (*WaybackURLHistoryOutput, error) {
	target, _ := input["url"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("input.url required")
	}

	// Default scope to root URL only; users can ask for whole subtree
	matchType := "exact"
	if v, ok := input["match_type"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "exact", "host", "domain", "prefix":
			matchType = strings.ToLower(strings.TrimSpace(v))
		}
	}

	from := ""
	if v, ok := input["from"].(string); ok {
		from = strings.TrimSpace(v)
	}
	to := ""
	if v, ok := input["to"].(string); ok {
		to = strings.TrimSpace(v)
	}

	// Cap to keep responses reasonable
	limit := 200
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 5000 {
		limit = int(v)
	}

	// Build CDX URL
	params := url.Values{}
	params.Set("url", target)
	params.Set("output", "json")
	params.Set("fl", "timestamp,original,statuscode,mimetype,digest")
	params.Set("matchType", matchType)
	if from != "" {
		params.Set("from", from)
	}
	if to != "" {
		params.Set("to", to)
	}
	params.Set("limit", fmt.Sprintf("%d", limit))

	// Use collapse=digest to deduplicate identical-content snapshots — this
	// gives us "unique content versions" naturally.
	params.Set("collapse", "digest")

	out := &WaybackURLHistoryOutput{
		URL:    target,
		Source: "web.archive.org/cdx/search/cdx",
	}
	start := time.Now()

	cdxURL := "https://web.archive.org/cdx/search/cdx?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", cdxURL, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cdx request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("cdx %d: %s", resp.StatusCode, string(body))
	}

	var rows [][]string
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("cdx decode: %w", err)
	}
	if len(rows) <= 1 { // first row is the header
		out.Note = fmt.Sprintf("no Wayback snapshots found for '%s' (matchType=%s)", target, matchType)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	// Skip header row
	rows = rows[1:]

	yearAgg := map[int]int{}
	statusAgg := map[string]int{}
	mimeAgg := map[string]int{}

	var snapshots []WaybackHistorySnapshot
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		ts := r[0]
		origURL := r[1]
		statusCode := r[2]
		mimeType := r[3]
		digest := r[4]

		s := WaybackHistorySnapshot{
			Timestamp:   ts,
			URL:         origURL,
			StatusCode:  statusCode,
			MimeType:    mimeType,
			Digest:      digest,
			ISODate:     waybackISO(ts),
			SnapshotURL: fmt.Sprintf("https://web.archive.org/web/%s/%s", ts, origURL),
		}
		snapshots = append(snapshots, s)

		// per-year
		if y, err := waybackParseYear(ts); err == nil && y > 1990 && y < 2100 {
			yearAgg[y]++
		}
		statusAgg[statusCode]++
		mimeAgg[mimeType]++
	}

	out.UniqueContentVersions = len(snapshots)
	// total_snapshots requires a separate (uncollapsed) call OR we estimate:
	// since we collapsed on digest, total >= unique. Mark as "unique_content_versions".
	// For the headline "total" we'll do a fast count probe.
	out.TotalSnapshots = countAllWaybackHistorySnapshots(ctx, client, target, matchType, from, to)

	// Sort by timestamp ascending
	sort.SliceStable(snapshots, func(i, j int) bool {
		return snapshots[i].Timestamp < snapshots[j].Timestamp
	})
	out.UniqueDigestSnapshots = snapshots
	if len(snapshots) > 30 {
		// Trim middle, keep first 10 + last 20 to limit response size
		head := snapshots[:10]
		tail := snapshots[len(snapshots)-20:]
		out.UniqueDigestSnapshots = append(head, tail...)
	}
	if len(snapshots) > 0 {
		f := snapshots[0]
		out.FirstSeen = &f
	}

	// CDX defaults to chronological-ascending. With `limit=N` we get the FIRST N
	// snapshots — so the last entry in our slice is NOT necessarily the most
	// recent overall. Issue a second negative-limit query to get the actual
	// most-recent snapshot (CDX supports `limit=-1` to reverse).
	if latest := fetchLatestWaybackSnapshot(ctx, client, target, matchType); latest != nil {
		out.LastSeen = latest
	} else if len(snapshots) > 0 {
		// fallback: if the second probe failed, use the last we have
		l := snapshots[len(snapshots)-1]
		out.LastSeen = &l
	}

	// Yearly distribution
	for y, c := range yearAgg {
		out.YearlyDistribution = append(out.YearlyDistribution, WaybackYearAggregate{Year: y, Count: c})
	}
	sort.SliceStable(out.YearlyDistribution, func(i, j int) bool {
		return out.YearlyDistribution[i].Year < out.YearlyDistribution[j].Year
	})

	// Detect year gaps in active range
	if len(out.YearlyDistribution) > 1 {
		first := out.YearlyDistribution[0].Year
		last := out.YearlyDistribution[len(out.YearlyDistribution)-1].Year
		seen := map[int]bool{}
		for _, ya := range out.YearlyDistribution {
			seen[ya.Year] = true
		}
		gapStart := 0
		for y := first + 1; y < last; y++ {
			if !seen[y] {
				if gapStart == 0 {
					gapStart = y
				}
			} else if gapStart != 0 {
				if gapStart == y-1 {
					out.YearGaps = append(out.YearGaps, fmt.Sprintf("%d", gapStart))
				} else {
					out.YearGaps = append(out.YearGaps, fmt.Sprintf("%d-%d", gapStart, y-1))
				}
				gapStart = 0
			}
		}
	}

	out.StatusCodeBreakdown = statusAgg
	out.MimeTypeBreakdown = mimeAgg
	out.HighlightFindings = buildWaybackHistoryHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func waybackISO(ts string) string {
	if len(ts) < 8 {
		return ""
	}
	return ts[:4] + "-" + ts[4:6] + "-" + ts[6:8]
}

func waybackParseYear(ts string) (int, error) {
	if len(ts) < 4 {
		return 0, fmt.Errorf("short")
	}
	var y int
	_, err := fmt.Sscanf(ts[:4], "%d", &y)
	return y, err
}

// fetchLatestWaybackSnapshot uses CDX's negative-limit (limit=-1) to retrieve
// the MOST RECENT snapshot (CDX defaults to chronological-ascending; negative
// limit reverses).
func fetchLatestWaybackSnapshot(ctx context.Context, client *http.Client, target, matchType string) *WaybackHistorySnapshot {
	params := url.Values{}
	params.Set("url", target)
	params.Set("output", "json")
	params.Set("fl", "timestamp,original,statuscode,mimetype,digest")
	params.Set("matchType", matchType)
	params.Set("limit", "-1")
	cdxURL := "https://web.archive.org/cdx/search/cdx?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", cdxURL, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var rows [][]string
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil
	}
	if len(rows) < 2 {
		return nil
	}
	r := rows[1]
	if len(r) < 5 {
		return nil
	}
	return &WaybackHistorySnapshot{
		Timestamp:   r[0],
		URL:         r[1],
		StatusCode:  r[2],
		MimeType:    r[3],
		Digest:      r[4],
		ISODate:     waybackISO(r[0]),
		SnapshotURL: fmt.Sprintf("https://web.archive.org/web/%s/%s", r[0], r[1]),
	}
}

// countAllWaybackHistorySnapshots issues a fast no-collapse count query.
func countAllWaybackHistorySnapshots(ctx context.Context, client *http.Client, target, matchType, from, to string) int {
	params := url.Values{}
	params.Set("url", target)
	params.Set("output", "json")
	params.Set("fl", "timestamp")
	params.Set("matchType", matchType)
	if from != "" {
		params.Set("from", from)
	}
	if to != "" {
		params.Set("to", to)
	}
	params.Set("showResumeKey", "true")
	params.Set("limit", "10000") // upper bound; counts pages above this won't be exact
	cdxURL := "https://web.archive.org/cdx/search/cdx?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", cdxURL, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0
	}
	var rows [][]string
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return 0
	}
	// Subtract header + resume key row
	count := len(rows) - 1
	if count < 0 {
		count = 0
	}
	// The last row may be a resume key (single-element row), strip it
	if count > 0 && len(rows[len(rows)-1]) == 1 {
		count--
	}
	return count
}

func buildWaybackHistoryHighlights(o *WaybackURLHistoryOutput) []string {
	hi := []string{}
	if o.FirstSeen != nil {
		hi = append(hi, fmt.Sprintf("✓ first archived %s — %s", o.FirstSeen.ISODate, o.FirstSeen.SnapshotURL))
	}
	if o.LastSeen != nil {
		hi = append(hi, fmt.Sprintf("most recent archive %s — %s", o.LastSeen.ISODate, o.LastSeen.SnapshotURL))
	}
	hi = append(hi, fmt.Sprintf("📊 %d total snapshots, %d unique content versions", o.TotalSnapshots, o.UniqueContentVersions))

	if len(o.YearlyDistribution) > 0 {
		first := o.YearlyDistribution[0].Year
		last := o.YearlyDistribution[len(o.YearlyDistribution)-1].Year
		years := last - first + 1
		hi = append(hi, fmt.Sprintf("⏳ active span: %d years (%d-%d, %d distinct years observed)", years, first, last, len(o.YearlyDistribution)))
		if len(o.YearGaps) > 0 {
			hi = append(hi, fmt.Sprintf("⚠️  dormancy gaps: %s — possible offline/parked/redirected periods", strings.Join(o.YearGaps, ", ")))
		}
	}
	// Status code anomalies
	if codes := o.StatusCodeBreakdown; len(codes) > 0 {
		bad := 0
		good := 0
		for code, n := range codes {
			if strings.HasPrefix(code, "4") || strings.HasPrefix(code, "5") {
				bad += n
			}
			if code == "200" {
				good += n
			}
		}
		if good == 0 && bad > 0 {
			hi = append(hi, fmt.Sprintf("🚫 zero 200 responses across %d snapshots — domain consistently broken/redirected", bad))
		} else if bad > 0 && bad >= good/2 {
			hi = append(hi, fmt.Sprintf("⚠️  high error-status ratio: %d errors vs %d 200s — unstable history", bad, good))
		}
	}
	// MIME type anomaly: if dominant mime is text/plain or warc/revisit, may be a parking page
	if mimes := o.MimeTypeBreakdown; len(mimes) > 0 {
		dom := ""
		domCount := 0
		for m, n := range mimes {
			if n > domCount {
				domCount = n
				dom = m
			}
		}
		if dom != "" && dom != "text/html" {
			hi = append(hi, fmt.Sprintf("dominant MIME type: %s (%d snapshots)", dom, domCount))
		}
	}
	return hi
}
