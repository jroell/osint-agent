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

// SEBadgeCounts mirrors the API.
type SEBadgeCounts struct {
	Bronze int `json:"bronze"`
	Silver int `json:"silver"`
	Gold   int `json:"gold"`
}

// SESiteActivity is one site in the user's network footprint.
type SESiteActivity struct {
	SiteName       string `json:"site_name"`
	SiteURL        string `json:"site_url"`
	APISiteParam   string `json:"api_site_param,omitempty"`
	UserID         int    `json:"user_id_on_site,omitempty"`
	Reputation     int    `json:"reputation"`
	AnswerCount    int    `json:"answer_count,omitempty"`
	QuestionCount  int    `json:"question_count,omitempty"`
	BadgeCounts    SEBadgeCounts `json:"badge_counts,omitempty"`
	LastAccessISO  string `json:"last_access_iso,omitempty"`
	CreationISO    string `json:"creation_iso,omitempty"`
}

// SETopTag is a top tag the user has answered.
type SETopTag struct {
	TagName       string `json:"tag_name"`
	AnswerCount   int    `json:"answer_count"`
	AnswerScore   int    `json:"answer_score"`
	QuestionCount int    `json:"question_count,omitempty"`
	QuestionScore int    `json:"question_score,omitempty"`
}

// SEProfile is the per-site profile data.
type SEProfile struct {
	UserID          int           `json:"user_id"`
	AccountID       int           `json:"account_id"`
	DisplayName     string        `json:"display_name"`
	Reputation      int           `json:"reputation"`
	UserType        string        `json:"user_type,omitempty"`
	WebsiteURL      string        `json:"website_url,omitempty"`
	Location        string        `json:"location,omitempty"`
	AboutMe         string        `json:"about_me,omitempty"`
	AcceptRate      int           `json:"accept_rate,omitempty"`
	BadgeCounts     SEBadgeCounts `json:"badge_counts,omitempty"`
	CreationISO     string        `json:"creation_iso,omitempty"`
	LastAccessISO   string        `json:"last_access_iso,omitempty"`
	AccountAgeYears float64       `json:"account_age_years,omitempty"`
	ProfileImage    string        `json:"profile_image,omitempty"`
	ProfileURL      string        `json:"profile_url,omitempty"`
}

// SEUserIntelOutput is the response.
type SEUserIntelOutput struct {
	UserID            int               `json:"user_id"`
	Site              string            `json:"site"`
	Profile           *SEProfile        `json:"profile,omitempty"`
	CrossSiteActivity []SESiteActivity  `json:"cross_site_activity,omitempty"`
	NetworkSiteCount  int               `json:"network_site_count"`
	TotalNetworkRep   int               `json:"total_network_reputation"`
	TopTags           []SETopTag        `json:"top_tags,omitempty"`
	HighlightFindings []string          `json:"highlight_findings"`
	Source            string            `json:"source"`
	TookMs            int64             `json:"tookMs"`
	Note              string            `json:"note,omitempty"`
}

type seUserRaw struct {
	Items []struct {
		UserID         int    `json:"user_id"`
		AccountID      int    `json:"account_id"`
		DisplayName    string `json:"display_name"`
		Reputation     int    `json:"reputation"`
		UserType       string `json:"user_type"`
		WebsiteURL     string `json:"website_url"`
		Location       string `json:"location"`
		AboutMe        string `json:"about_me"`
		AcceptRate     int    `json:"accept_rate"`
		BadgeCounts    struct {
			Bronze int `json:"bronze"`
			Silver int `json:"silver"`
			Gold   int `json:"gold"`
		} `json:"badge_counts"`
		CreationDate     int64  `json:"creation_date"`
		LastAccessDate   int64  `json:"last_access_date"`
		ProfileImage     string `json:"profile_image"`
		Link             string `json:"link"`
	} `json:"items"`
}

type seAssociatedRaw struct {
	Items []struct {
		SiteName        string `json:"site_name"`
		SiteURL         string `json:"site_url"`
		APISiteParam    string `json:"api_site_parameter"`
		UserID          int    `json:"user_id"`
		Reputation      int    `json:"reputation"`
		AnswerCount     int    `json:"answer_count"`
		QuestionCount   int    `json:"question_count"`
		BadgeCounts     struct {
			Bronze int `json:"bronze"`
			Silver int `json:"silver"`
			Gold   int `json:"gold"`
		} `json:"badge_counts"`
		LastAccessDate int64 `json:"last_access_date"`
		CreationDate   int64 `json:"creation_date"`
	} `json:"items"`
}

type seTopTagsRaw struct {
	Items []struct {
		TagName       string `json:"tag_name"`
		AnswerCount   int    `json:"answer_count"`
		AnswerScore   int    `json:"answer_score"`
		QuestionCount int    `json:"question_count"`
		QuestionScore int    `json:"question_score"`
	} `json:"items"`
}

// StackExchangeUserIntel performs a deep dive on a Stack Exchange user
// across the entire SE network (170+ sites). Free public API (no auth
// needed for this volume), with a 10K req/day key bump if SE_API_KEY is
// set.
//
// Why this matters for ER:
//   - SE user_id is per-site; account_id is network-wide. /associated
//     endpoint returns every site that account has activity on. This
//     reveals **niche personal interests** invisible to SO-only analytics
//     (e.g., a programmer with low rep on Mi Yodeya = Jewish religious
//     interest; with rep on Bicycles SE = cycling hobby; on Politics SE
//     = political engagement).
//   - top_tags reveals expertise depth: 19,981 answers tagged C# = THE
//     domain expert on a topic.
//   - Badge counts (especially gold badges, requiring rep + tag-level
//     achievements) are competence signals.
//   - Real names + locations + websites are commonly disclosed.
//   - Account age is verifiable temporal signal.
//   - Uniquely enables "what other niche communities is this engineer
//     in?" queries — a strong COI / personal-affiliation ER vector.
func StackExchangeUserIntel(ctx context.Context, input map[string]any) (*SEUserIntelOutput, error) {
	uidF, ok := input["user_id"].(float64)
	if !ok {
		return nil, fmt.Errorf("input.user_id required (numeric SE user ID on the primary site)")
	}
	userID := int(uidF)
	if userID <= 0 {
		return nil, fmt.Errorf("input.user_id must be > 0")
	}

	site, _ := input["site"].(string)
	site = strings.TrimSpace(site)
	if site == "" {
		site = "stackoverflow"
	}

	out := &SEUserIntelOutput{
		UserID: userID,
		Site:   site,
		Source: "api.stackexchange.com",
	}
	start := time.Now()
	client := &http.Client{Timeout: 25 * time.Second}

	// 1. Profile on the primary site
	prof, err := fetchSEUser(ctx, client, userID, site)
	if err != nil {
		return nil, err
	}
	if prof == nil {
		out.Note = fmt.Sprintf("no SE user with id=%d on site=%s", userID, site)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	out.Profile = prof

	// 2. Cross-site activity (using account_id, not user_id)
	if prof.AccountID > 0 {
		activity, err := fetchSEAssociated(ctx, client, prof.AccountID)
		if err == nil {
			out.CrossSiteActivity = activity
			out.NetworkSiteCount = len(activity)
			for _, a := range activity {
				out.TotalNetworkRep += a.Reputation
			}
		}
	}

	// 3. Top tags on the primary site
	tags, err := fetchSETopTags(ctx, client, userID, site)
	if err == nil {
		out.TopTags = tags
	}

	out.HighlightFindings = buildSEHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func fetchSEUser(ctx context.Context, client *http.Client, userID int, site string) (*SEProfile, error) {
	params := url.Values{}
	params.Set("site", site)
	// Custom filter to include account_id (default filter excludes it)
	params.Set("filter", "!9_bDDxJY5")
	endpoint := fmt.Sprintf("https://api.stackexchange.com/2.3/users/%d?%s", userID, params.Encode())
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("se user: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("se %d: %s", resp.StatusCode, string(body))
	}
	var raw seUserRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if len(raw.Items) == 0 {
		return nil, nil
	}
	u := raw.Items[0]
	prof := &SEProfile{
		UserID:        u.UserID,
		AccountID:     u.AccountID,
		DisplayName:   stripBasicHTML(u.DisplayName),
		Reputation:    u.Reputation,
		UserType:      u.UserType,
		WebsiteURL:    u.WebsiteURL,
		Location:      u.Location,
		AboutMe:       hfTruncate(stripBasicHTML(u.AboutMe), 500),
		AcceptRate:    u.AcceptRate,
		BadgeCounts:   SEBadgeCounts{Bronze: u.BadgeCounts.Bronze, Silver: u.BadgeCounts.Silver, Gold: u.BadgeCounts.Gold},
		ProfileImage:  u.ProfileImage,
		ProfileURL:    u.Link,
	}
	if u.CreationDate > 0 {
		t := time.Unix(u.CreationDate, 0).UTC()
		prof.CreationISO = t.Format(time.RFC3339)
		prof.AccountAgeYears = time.Since(t).Hours() / (24 * 365.25)
	}
	if u.LastAccessDate > 0 {
		prof.LastAccessISO = time.Unix(u.LastAccessDate, 0).UTC().Format(time.RFC3339)
	}
	return prof, nil
}

func fetchSEAssociated(ctx context.Context, client *http.Client, accountID int) ([]SESiteActivity, error) {
	params := url.Values{}
	params.Set("pagesize", "100")
	endpoint := fmt.Sprintf("https://api.stackexchange.com/2.3/users/%d/associated?%s", accountID, params.Encode())
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("se associated %d", resp.StatusCode)
	}
	var raw seAssociatedRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]SESiteActivity, 0, len(raw.Items))
	for _, s := range raw.Items {
		a := SESiteActivity{
			SiteName:      stripBasicHTML(s.SiteName),
			SiteURL:       s.SiteURL,
			APISiteParam:  s.APISiteParam,
			UserID:        s.UserID,
			Reputation:    s.Reputation,
			AnswerCount:   s.AnswerCount,
			QuestionCount: s.QuestionCount,
			BadgeCounts:   SEBadgeCounts{Bronze: s.BadgeCounts.Bronze, Silver: s.BadgeCounts.Silver, Gold: s.BadgeCounts.Gold},
		}
		if s.LastAccessDate > 0 {
			a.LastAccessISO = time.Unix(s.LastAccessDate, 0).UTC().Format(time.RFC3339)
		}
		if s.CreationDate > 0 {
			a.CreationISO = time.Unix(s.CreationDate, 0).UTC().Format(time.RFC3339)
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Reputation > out[j].Reputation })
	return out, nil
}

func fetchSETopTags(ctx context.Context, client *http.Client, userID int, site string) ([]SETopTag, error) {
	params := url.Values{}
	params.Set("site", site)
	params.Set("pagesize", "20")
	endpoint := fmt.Sprintf("https://api.stackexchange.com/2.3/users/%d/top-tags?%s", userID, params.Encode())
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("se top-tags %d", resp.StatusCode)
	}
	var raw seTopTagsRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]SETopTag, 0, len(raw.Items))
	for _, t := range raw.Items {
		out = append(out, SETopTag{
			TagName:       t.TagName,
			AnswerCount:   t.AnswerCount,
			AnswerScore:   t.AnswerScore,
			QuestionCount: t.QuestionCount,
			QuestionScore: t.QuestionScore,
		})
	}
	return out, nil
}

func buildSEHighlights(o *SEUserIntelOutput) []string {
	hi := []string{}
	if o.Profile != nil {
		p := o.Profile
		regDate := ""
		if len(p.CreationISO) >= 10 {
			regDate = p.CreationISO[:10]
		}
		hi = append(hi, fmt.Sprintf("✓ %s (user_id=%d, account_id=%d) — rep=%d on %s, registered %s (%.1fy)",
			p.DisplayName, p.UserID, p.AccountID, p.Reputation, o.Site, regDate, p.AccountAgeYears))
		if p.Location != "" {
			hi = append(hi, "📍 location: "+p.Location)
		}
		if p.WebsiteURL != "" {
			hi = append(hi, "🔗 personal website: "+p.WebsiteURL)
		}
		hi = append(hi, fmt.Sprintf("🏅 badges: %d bronze, %d silver, %d gold", p.BadgeCounts.Bronze, p.BadgeCounts.Silver, p.BadgeCounts.Gold))
	}
	if o.NetworkSiteCount > 0 {
		hi = append(hi, fmt.Sprintf("🌐 active on %d SE-network sites — total network rep %d", o.NetworkSiteCount, o.TotalNetworkRep))
		// Surface the niche/non-tech sites for ER value
		nicheList := []string{}
		for _, a := range o.CrossSiteActivity {
			site := a.SiteName
			if a.Reputation < 50 {
				continue
			}
			// crude tech-vs-niche detector
			if isNicheSEsite(site) {
				nicheList = append(nicheList, fmt.Sprintf("%s (rep=%d)", site, a.Reputation))
			}
		}
		if len(nicheList) > 0 {
			hi = append(hi, "⚡ NON-TECH activity (interest disclosure): "+strings.Join(nicheList, " | "))
		}
	}
	if len(o.TopTags) > 0 {
		topTags := []string{}
		for _, t := range o.TopTags[:min2(8, len(o.TopTags))] {
			topTags = append(topTags, fmt.Sprintf("%s(%d)", t.TagName, t.AnswerCount))
		}
		hi = append(hi, "🛠 expertise (top tags by answer count): "+strings.Join(topTags, ", "))
	}
	return hi
}

func isNicheSEsite(siteName string) bool {
	tech := []string{"stack overflow", "server fault", "super user", "meta stack exchange", "stack apps",
		"software engineering", "code review", "code golf", "database administrators",
		"sharepoint", "salesforce", "magento", "drupal", "wordpress",
		"unix &amp; linux", "ubuntu", "ask different", "android enthusiasts",
		"information security", "tor", "open source", "site reliability engineering",
		"electrical engineering", "blockchain", "ethereum", "bitcoin",
		"web applications"}
	low := strings.ToLower(siteName)
	for _, t := range tech {
		if strings.Contains(low, t) {
			return false
		}
	}
	return true
}
