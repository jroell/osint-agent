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

// WikiUserProfile is the user metadata.
type WikiUserProfile struct {
	Username        string   `json:"username"`
	UserID          int64    `json:"user_id,omitempty"`
	EditCount       int      `json:"edit_count,omitempty"`
	Registration    string   `json:"registration_iso,omitempty"`
	AccountAgeDays  int      `json:"account_age_days,omitempty"`
	AccountAgeYears float64  `json:"account_age_years,omitempty"`
	Groups          []string `json:"groups,omitempty"`
	Rights          []string `json:"rights,omitempty"`
	Gender          string   `json:"gender,omitempty"`
	Blocked         bool     `json:"blocked,omitempty"`
	BlockReason     string   `json:"block_reason,omitempty"`
}

// WikiContrib is a single edit.
type WikiContrib struct {
	Title     string `json:"title"`
	Namespace int    `json:"namespace,omitempty"`
	Timestamp string `json:"timestamp"`
	Comment   string `json:"comment,omitempty"`
	SizeDiff  int    `json:"size_diff,omitempty"`
	Minor     bool   `json:"minor,omitempty"`
	Top       bool   `json:"top,omitempty"`
	RevID     int64  `json:"rev_id,omitempty"`
}

// WikiArticleAggregate counts edits per article.
type WikiArticleAggregate struct {
	Title    string `json:"title"`
	Edits    int    `json:"edits"`
	NetSize  int    `json:"net_size_change"`
	LastEdit string `json:"last_edit_iso,omitempty"`
}

// WikiNamespaceAggregate counts edits per namespace.
type WikiNamespaceAggregate struct {
	Namespace int    `json:"namespace_id"`
	Name      string `json:"namespace_name"`
	Edits     int    `json:"edits"`
}

// WikiHourBucket counts edits per UTC hour.
type WikiHourBucket struct {
	HourUTC int `json:"hour_utc"`
	Count   int `json:"count"`
}

// WikipediaUserIntelOutput is the response.
type WikipediaUserIntelOutput struct {
	WikiHost          string                  `json:"wiki_host"`
	Profile           *WikiUserProfile        `json:"profile,omitempty"`
	RecentContribs    []WikiContrib           `json:"recent_contribs,omitempty"`
	TopArticles       []WikiArticleAggregate  `json:"top_articles,omitempty"`
	TopNamespaces     []WikiNamespaceAggregate `json:"top_namespaces,omitempty"`
	HourDistribution  []WikiHourBucket        `json:"hour_distribution_utc,omitempty"`
	InferredTimezone  string                  `json:"inferred_timezone,omitempty"`
	OldestContribISO  string                  `json:"oldest_contrib_iso,omitempty"`
	NewestContribISO  string                  `json:"newest_contrib_iso,omitempty"`
	HighlightFindings []string                `json:"highlight_findings"`
	Source            string                  `json:"source"`
	TookMs            int64                   `json:"tookMs"`
	Note              string                  `json:"note,omitempty"`
}

// raw API response shapes
type wpQueryUsers struct {
	Query struct {
		Users []struct {
			Name         string   `json:"name"`
			UserID       int64    `json:"userid"`
			EditCount    int      `json:"editcount"`
			Registration string   `json:"registration"`
			Groups       []string `json:"groups"`
			Rights       []string `json:"rights"`
			Gender       string   `json:"gender"`
			BlockedBy    string   `json:"blockedby"`
			BlockReason  string   `json:"blockreason"`
			// MediaWiki uses empty-string sentinel `"missing": ""` for missing users; boolean decode would fail
			Missing      any `json:"missing"`
		} `json:"users"`
	} `json:"query"`
}

type wpQueryContribs struct {
	Query struct {
		UserContribs []struct {
			RevID     int64  `json:"revid"`
			Title     string `json:"title"`
			Namespace int    `json:"ns"`
			Timestamp string `json:"timestamp"`
			Comment   string `json:"comment"`
			SizeDiff  int    `json:"sizediff"`
			Minor     string `json:"minor"`
			Top       string `json:"top"`
		} `json:"usercontribs"`
	} `json:"query"`
	Continue map[string]any `json:"continue"`
}

// canonical namespace IDs → human names
var wikiNamespaceNames = map[int]string{
	0:   "(article)",
	1:   "Talk",
	2:   "User",
	3:   "User talk",
	4:   "Project",
	5:   "Project talk",
	6:   "File",
	7:   "File talk",
	8:   "MediaWiki",
	9:   "MediaWiki talk",
	10:  "Template",
	11:  "Template talk",
	12:  "Help",
	13:  "Help talk",
	14:  "Category",
	15:  "Category talk",
	100: "Portal",
	118: "Draft",
}

// WikipediaUserIntel fetches Wikipedia user metadata + recent contributions
// and aggregates them into an ER-ready signal set. Defaults to en.wikipedia.org;
// pass `wiki_lang` to query other Wikipedias (fr, de, ja, etc.) or
// `wiki_host` for non-Wikipedia MediaWiki sites (e.g. Wiktionary, Wikidata).
//
// Why this matters for ER:
//   - Wikipedia editors leak interest graphs through which articles they edit.
//   - Account-creation dates are immutable + verifiable (Wikipedia's logs are
//     tamper-evident).
//   - Edit-time distributions narrow timezone in much the same way Reddit
//     posting times do — and Wikipedia editors are typically more methodical,
//     so the signal is cleaner.
//   - Conflict-of-interest patterns: editing one's own employer/biography
//     repeatedly = SOC actor or a paid editor. Surfaces from `top_articles`.
//   - Sockpuppet flagging: very young account + immediate edits to
//     controversial topics = potential sockpuppet (highlighted).
//   - User groups (sysop, bureaucrat, etc.) reveal community rank.
//   - The "founder" group on en.wikipedia.org is unique to userid=24 —
//     useful as ground-truth check.
func WikipediaUserIntel(ctx context.Context, input map[string]any) (*WikipediaUserIntelOutput, error) {
	username, _ := input["username"].(string)
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "User:")
	if username == "" {
		return nil, fmt.Errorf("input.username required")
	}

	wikiLang, _ := input["wiki_lang"].(string)
	wikiLang = strings.TrimSpace(wikiLang)
	wikiHost, _ := input["wiki_host"].(string)
	wikiHost = strings.TrimSpace(wikiHost)
	if wikiHost == "" {
		if wikiLang == "" {
			wikiLang = "en"
		}
		wikiHost = wikiLang + ".wikipedia.org"
	}

	contribLimit := 100
	if v, ok := input["contrib_limit"].(float64); ok && int(v) > 0 && int(v) <= 500 {
		contribLimit = int(v)
	}

	out := &WikipediaUserIntelOutput{
		WikiHost: wikiHost,
		Source:   "MediaWiki API (action=query)",
	}
	start := time.Now()

	client := &http.Client{Timeout: 25 * time.Second}
	apiBase := "https://" + wikiHost + "/w/api.php"

	// 1. User info
	prof, err := fetchWikiUserInfo(ctx, client, apiBase, username)
	if err != nil {
		return nil, err
	}
	if prof == nil {
		out.Note = fmt.Sprintf("user '%s' not found on %s", username, wikiHost)
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	out.Profile = prof

	// 2. Recent contributions
	contribs, err := fetchWikiContribs(ctx, client, apiBase, username, contribLimit)
	if err != nil {
		// Profile is already populated, return what we have plus the error note
		out.Note = fmt.Sprintf("contrib fetch failed: %v", err)
		out.HighlightFindings = buildWikiHighlights(out)
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	out.RecentContribs = contribs

	// 3. Aggregations
	articleAgg := map[string]*WikiArticleAggregate{}
	nsAgg := map[int]*WikiNamespaceAggregate{}
	hourCount := [24]int{}
	var minTs, maxTs string

	for _, c := range contribs {
		// per-article
		ag, ok := articleAgg[c.Title]
		if !ok {
			ag = &WikiArticleAggregate{Title: c.Title, LastEdit: c.Timestamp}
			articleAgg[c.Title] = ag
		}
		ag.Edits++
		ag.NetSize += c.SizeDiff
		// keep first-seen timestamp as last_edit (since contribs are descending)
		if ag.LastEdit == "" || c.Timestamp > ag.LastEdit {
			ag.LastEdit = c.Timestamp
		}

		// per-namespace
		nsAg, ok := nsAgg[c.Namespace]
		if !ok {
			nsAg = &WikiNamespaceAggregate{Namespace: c.Namespace, Name: wikiNamespaceName(c.Namespace)}
			nsAgg[c.Namespace] = nsAg
		}
		nsAg.Edits++

		// hour distribution
		if t, err := time.Parse(time.RFC3339, c.Timestamp); err == nil {
			hourCount[t.UTC().Hour()]++
		}

		// time range tracking (string compare works for ISO 8601)
		if minTs == "" || c.Timestamp < minTs {
			minTs = c.Timestamp
		}
		if c.Timestamp > maxTs {
			maxTs = c.Timestamp
		}
	}

	for _, ag := range articleAgg {
		out.TopArticles = append(out.TopArticles, *ag)
	}
	sort.SliceStable(out.TopArticles, func(i, j int) bool {
		return out.TopArticles[i].Edits > out.TopArticles[j].Edits
	})
	if len(out.TopArticles) > 15 {
		out.TopArticles = out.TopArticles[:15]
	}

	for _, ag := range nsAgg {
		out.TopNamespaces = append(out.TopNamespaces, *ag)
	}
	sort.SliceStable(out.TopNamespaces, func(i, j int) bool {
		return out.TopNamespaces[i].Edits > out.TopNamespaces[j].Edits
	})

	for h, c := range hourCount {
		if c > 0 {
			out.HourDistribution = append(out.HourDistribution, WikiHourBucket{HourUTC: h, Count: c})
		}
	}
	sort.SliceStable(out.HourDistribution, func(i, j int) bool {
		return out.HourDistribution[i].HourUTC < out.HourDistribution[j].HourUTC
	})

	out.InferredTimezone = inferTimezoneFromHours(hourCount[:])
	out.OldestContribISO = minTs
	out.NewestContribISO = maxTs

	out.HighlightFindings = buildWikiHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func fetchWikiUserInfo(ctx context.Context, client *http.Client, apiBase, username string) (*WikiUserProfile, error) {
	params := url.Values{}
	params.Set("action", "query")
	params.Set("format", "json")
	params.Set("list", "users")
	params.Set("ususers", username)
	params.Set("usprop", "blockinfo|groups|editcount|registration|gender|rights")

	req, _ := http.NewRequestWithContext(ctx, "GET", apiBase+"?"+params.Encode(), nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("user info fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("mediawiki %d: %s", resp.StatusCode, string(body))
	}
	var raw wpQueryUsers
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("user info decode: %w", err)
	}
	if len(raw.Query.Users) == 0 {
		return nil, nil
	}
	// `missing` is present (any type — empty string or true) when the user doesn't exist.
	// User exists if userid > 0 OR (no missing field AND name is set).
	u0 := raw.Query.Users[0]
	if u0.Missing != nil {
		return nil, nil
	}
	if u0.UserID == 0 && u0.Name == "" {
		return nil, nil
	}
	u := raw.Query.Users[0]
	prof := &WikiUserProfile{
		Username:     u.Name,
		UserID:       u.UserID,
		EditCount:    u.EditCount,
		Registration: u.Registration,
		Groups:       u.Groups,
		Rights:       u.Rights,
		Gender:       u.Gender,
	}
	if u.BlockedBy != "" {
		prof.Blocked = true
		prof.BlockReason = u.BlockReason
	}
	if u.Registration != "" {
		if t, err := time.Parse(time.RFC3339, u.Registration); err == nil {
			ageDays := int(time.Since(t).Hours() / 24)
			prof.AccountAgeDays = ageDays
			prof.AccountAgeYears = float64(ageDays) / 365.25
		}
	}
	return prof, nil
}

func fetchWikiContribs(ctx context.Context, client *http.Client, apiBase, username string, limit int) ([]WikiContrib, error) {
	params := url.Values{}
	params.Set("action", "query")
	params.Set("format", "json")
	params.Set("list", "usercontribs")
	params.Set("ucuser", username)
	params.Set("uclimit", fmt.Sprintf("%d", limit))
	params.Set("ucprop", "ids|title|timestamp|comment|sizediff|flags")

	req, _ := http.NewRequestWithContext(ctx, "GET", apiBase+"?"+params.Encode(), nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contribs fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("mediawiki %d: %s", resp.StatusCode, string(body))
	}
	var raw wpQueryContribs
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("contribs decode: %w", err)
	}
	var out []WikiContrib
	for _, c := range raw.Query.UserContribs {
		out = append(out, WikiContrib{
			RevID:     c.RevID,
			Title:     c.Title,
			Namespace: c.Namespace,
			Timestamp: c.Timestamp,
			Comment:   c.Comment,
			SizeDiff:  c.SizeDiff,
			Minor:     c.Minor != "",
			Top:       c.Top != "",
		})
	}
	return out, nil
}

func wikiNamespaceName(id int) string {
	if name, ok := wikiNamespaceNames[id]; ok {
		return name
	}
	return fmt.Sprintf("ns:%d", id)
}

func buildWikiHighlights(o *WikipediaUserIntelOutput) []string {
	hi := []string{}
	if o.Profile != nil {
		p := o.Profile
		regDate := ""
		if p.Registration != "" && len(p.Registration) >= 10 {
			regDate = p.Registration[:10]
		}
		hi = append(hi, fmt.Sprintf("✓ User:%s on %s — %d total edits since %s (%.1f years)",
			p.Username, o.WikiHost, p.EditCount, regDate, p.AccountAgeYears))
		if p.UserID > 0 {
			hi = append(hi, fmt.Sprintf("user_id: %d (lower = older account on this wiki)", p.UserID))
		}
		if len(p.Groups) > 0 {
			// filter out the *, user, autoconfirmed defaults
			interesting := []string{}
			for _, g := range p.Groups {
				if g == "*" || g == "user" || g == "autoconfirmed" {
					continue
				}
				interesting = append(interesting, g)
			}
			if len(interesting) > 0 {
				hi = append(hi, "🛡  notable groups: "+strings.Join(interesting, ", "))
			}
		}
		if p.Blocked {
			hi = append(hi, fmt.Sprintf("🚫 BLOCKED — reason: %s", p.BlockReason))
		}
		if p.AccountAgeDays > 0 && p.AccountAgeDays < 30 && p.EditCount > 50 {
			hi = append(hi, "⚠️  young account (<30 days) with high edit count — possible sockpuppet pattern")
		}
		if p.EditCount > 50000 {
			hi = append(hi, fmt.Sprintf("✓ extremely prolific editor (%d edits) — likely topical SME or paid editor", p.EditCount))
		}
	}
	if len(o.TopArticles) > 0 {
		topNames := []string{}
		for _, a := range o.TopArticles[:min2(5, len(o.TopArticles))] {
			topNames = append(topNames, fmt.Sprintf("'%s' (%dx)", a.Title, a.Edits))
		}
		hi = append(hi, "📚 interest graph (top edited): "+strings.Join(topNames, ", "))
	}
	if o.InferredTimezone != "" {
		hi = append(hi, "📍 timezone inference: "+o.InferredTimezone)
	}
	if len(o.TopNamespaces) > 0 {
		nsNames := []string{}
		for _, n := range o.TopNamespaces[:min2(4, len(o.TopNamespaces))] {
			nsNames = append(nsNames, fmt.Sprintf("%s=%d", n.Name, n.Edits))
		}
		hi = append(hi, "namespace breakdown: "+strings.Join(nsNames, ", "))
		// SPI / talk-page heavy users are often advocates
		if len(o.TopNamespaces) > 0 && o.TopNamespaces[0].Namespace == 3 { // User talk
			hi = append(hi, "ℹ️  most edits in User talk: namespace — heavy talk-page communicator (advocate, mentor, or controversy)")
		}
	}
	if o.OldestContribISO != "" && o.NewestContribISO != "" {
		hi = append(hi, fmt.Sprintf("recent activity range: %s → %s", o.OldestContribISO[:10], o.NewestContribISO[:10]))
	}
	return hi
}
