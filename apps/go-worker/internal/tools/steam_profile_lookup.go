package tools

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SteamGroup is a Steam community group.
type SteamGroup struct {
	GroupID64  string `json:"group_id_64,omitempty"`
	GroupName  string `json:"group_name,omitempty"`
	GroupURL   string `json:"group_url,omitempty"`
	IsPrimary  bool   `json:"is_primary,omitempty"`
	Headline   string `json:"headline,omitempty"`
}

// SteamGameStat is a most-played game entry.
type SteamGameStat struct {
	Name             string `json:"name"`
	HoursPlayedTotal float64 `json:"hours_played_total"`
	HoursLast2Weeks  float64 `json:"hours_played_recent"`
}

// SteamProfile is the resolved Steam profile.
type SteamProfile struct {
	SteamID64       string  `json:"steam_id_64"`
	SteamID         string  `json:"steam_id_visible,omitempty"` // display name
	CustomURL       string  `json:"custom_url,omitempty"`       // vanity URL slug
	ProfileURL      string  `json:"profile_url"`
	AvatarFull      string  `json:"avatar_full,omitempty"`
	RealName        string  `json:"real_name,omitempty"`
	Location        string  `json:"location,omitempty"`
	MemberSince     string  `json:"member_since,omitempty"`
	AccountAgeYears float64 `json:"account_age_years,omitempty"`
	OnlineState     string  `json:"online_state,omitempty"`
	StateMessage    string  `json:"state_message,omitempty"`
	PrivacyState    string  `json:"privacy_state,omitempty"`
	Summary         string  `json:"summary,omitempty"`
	Headline        string  `json:"headline,omitempty"`
	IsLimited       bool    `json:"is_limited_account,omitempty"`
	VACBanned       bool    `json:"vac_banned,omitempty"`
	TradeBanState   string  `json:"trade_ban_state,omitempty"`
	Groups          []SteamGroup    `json:"groups,omitempty"`
	MostPlayedGames []SteamGameStat `json:"most_played_games,omitempty"`
}

// SteamProfileLookupOutput is the response.
type SteamProfileLookupOutput struct {
	Input            string         `json:"input"`
	ResolvedAs       string         `json:"resolved_as"` // "steam_id_64" | "vanity_url"
	Profile          *SteamProfile  `json:"profile,omitempty"`
	HighlightFindings []string      `json:"highlight_findings"`
	Source           string         `json:"source"`
	TookMs           int64          `json:"tookMs"`
	Note             string         `json:"note,omitempty"`
}

// XML schemas for Steam community
type steamProfileXML struct {
	XMLName         xml.Name `xml:"profile"`
	SteamID64       string   `xml:"steamID64"`
	SteamID         string   `xml:"steamID"`
	CustomURL       string   `xml:"customURL"`
	OnlineState     string   `xml:"onlineState"`
	StateMessage    string   `xml:"stateMessage"`
	PrivacyState    string   `xml:"privacyState"`
	VisibilityState int      `xml:"visibilityState"`
	AvatarIcon      string   `xml:"avatarIcon"`
	AvatarMedium    string   `xml:"avatarMedium"`
	AvatarFull      string   `xml:"avatarFull"`
	VACBanned       int      `xml:"vacBanned"`
	TradeBanState   string   `xml:"tradeBanState"`
	IsLimitedAccount int     `xml:"isLimitedAccount"`
	Headline        string   `xml:"headline"`
	Summary         string   `xml:"summary"`
	Location        string   `xml:"location"`
	RealName        string   `xml:"realname"`
	MemberSince     string   `xml:"memberSince"`
	Groups          struct {
		Group []struct {
			IsPrimary string `xml:"isPrimary,attr"`
			GroupID64 string `xml:"groupID64"`
			GroupName string `xml:"groupName"`
			GroupURL  string `xml:"groupURL"`
			Headline  string `xml:"headline"`
		} `xml:"group"`
	} `xml:"groups"`
	MostPlayedGames struct {
		MostPlayedGame []struct {
			GameName     string `xml:"gameName"`
			HoursPlayed  string `xml:"hoursPlayed"`
			HoursOnRecord string `xml:"hoursOnRecord"`
		} `xml:"mostPlayedGame"`
	} `xml:"mostPlayedGames"`
	// Some profiles return <error> if private/unresolvable
	Error string `xml:"error"`
}

// SteamProfileLookup resolves a Steam community profile via the free public
// XML API. Accepts either a SteamID64 (17-digit numeric) or a vanity URL
// (custom_url slug).
//
// Why this matters for ER:
//   - Steam users frequently disclose REAL NAME and COUNTRY in their profile
//     (visible if privacy=public).
//   - Account age (memberSince) is verifiable — ground-truth temporal signal.
//   - Group memberships reveal interest graph (gaming, politics, regional,
//     gender, language).
//   - VAC ban / trade ban flags are public regardless of privacy setting —
//     adversarial signal (cheating, scamming).
//   - Custom URL slug is often reused as username on other platforms (Discord,
//     Twitch, GitHub) — strong cross-platform ER pivot.
//   - SteamID64 is a 17-digit immutable identifier (76561197960287930-style)
//     which can be cross-referenced in various Steam-related leak databases.
//
// Strong complement to other gamer ER tools.
func SteamProfileLookup(ctx context.Context, input map[string]any) (*SteamProfileLookupOutput, error) {
	rawIn, _ := input["id"].(string)
	rawIn = strings.TrimSpace(rawIn)
	if rawIn == "" {
		return nil, fmt.Errorf("input.id required (SteamID64 e.g. '76561197960287930' or vanity URL e.g. 'gabelogannewell')")
	}

	// Strip URLs if user pasted full Steam URLs
	if strings.HasPrefix(rawIn, "http") {
		// extract trailing slug
		clean := strings.TrimRight(rawIn, "/")
		parts := strings.Split(clean, "/")
		if len(parts) > 0 {
			rawIn = parts[len(parts)-1]
		}
	}
	rawIn = strings.TrimPrefix(rawIn, "@")

	// Detect: 17-digit numeric = SteamID64; otherwise vanity
	isNumeric := false
	if len(rawIn) == 17 {
		if _, err := strconv.ParseInt(rawIn, 10, 64); err == nil {
			isNumeric = true
		}
	}

	out := &SteamProfileLookupOutput{
		Input:  rawIn,
		Source: "steamcommunity.com (public XML API)",
	}
	var profileURL string
	if isNumeric {
		out.ResolvedAs = "steam_id_64"
		profileURL = "https://steamcommunity.com/profiles/" + rawIn
	} else {
		out.ResolvedAs = "vanity_url"
		profileURL = "https://steamcommunity.com/id/" + rawIn
	}

	start := time.Now()
	xmlURL := profileURL + "?xml=1"
	req, _ := http.NewRequestWithContext(ctx, "GET", xmlURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; osint-agent/0.1)")
	req.Header.Set("Accept", "application/xml,text/xml")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("steam fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2_000_000))
	if err != nil {
		return nil, fmt.Errorf("steam read: %w", err)
	}

	// Steam returns 200 even for missing profiles. The "not found" response
	// has root element <response><error>...</error></response> instead of <profile>.
	bodyStr := string(body)
	if strings.Contains(bodyStr, "<response>") && strings.Contains(bodyStr, "<error>") {
		// extract error text
		errText := ""
		if m := regexp.MustCompile(`(?s)<error>(?:<!\[CDATA\[)?(.+?)(?:\]\]>)?</error>`).FindStringSubmatch(bodyStr); len(m) > 1 {
			errText = strings.TrimSpace(m[1])
		}
		out.Note = fmt.Sprintf("steam returned no profile for '%s' — %s", rawIn, errText)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	var raw steamProfileXML
	if err := xml.Unmarshal(body, &raw); err != nil {
		out.Note = fmt.Sprintf("XML decode failed (likely not an XML response): %v", err)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	if raw.Error != "" {
		out.Note = fmt.Sprintf("steam returned: %s", raw.Error)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	if raw.SteamID64 == "" {
		out.Note = fmt.Sprintf("no profile found for '%s' (or fully private)", rawIn)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	prof := &SteamProfile{
		SteamID64:        raw.SteamID64,
		SteamID:          raw.SteamID,
		CustomURL:        raw.CustomURL,
		ProfileURL:       profileURL,
		AvatarFull:       raw.AvatarFull,
		RealName:         strings.TrimSpace(raw.RealName),
		Location:         strings.TrimSpace(raw.Location),
		MemberSince:      strings.TrimSpace(raw.MemberSince),
		OnlineState:      raw.OnlineState,
		StateMessage:     stripBasicHTML(raw.StateMessage),
		PrivacyState:     raw.PrivacyState,
		Summary:          stripBasicHTML(raw.Summary),
		Headline:         stripBasicHTML(raw.Headline),
		IsLimited:        raw.IsLimitedAccount == 1,
		VACBanned:        raw.VACBanned == 1,
		TradeBanState:    raw.TradeBanState,
	}
	// Derive account age from "September 12, 2003" format
	if prof.MemberSince != "" {
		if t, err := time.Parse("January 2, 2006", prof.MemberSince); err == nil {
			prof.AccountAgeYears = time.Since(t).Hours() / (24 * 365.25)
		}
	}
	for _, g := range raw.Groups.Group {
		prof.Groups = append(prof.Groups, SteamGroup{
			GroupID64: strings.TrimSpace(g.GroupID64),
			GroupName: strings.TrimSpace(g.GroupName),
			GroupURL:  strings.TrimSpace(g.GroupURL),
			IsPrimary: g.IsPrimary == "1",
			Headline:  strings.TrimSpace(g.Headline),
		})
	}
	if len(prof.Groups) > 30 {
		prof.Groups = prof.Groups[:30]
	}
	for _, g := range raw.MostPlayedGames.MostPlayedGame {
		var hours, recent float64
		if h := strings.ReplaceAll(strings.TrimSpace(g.HoursOnRecord), ",", ""); h != "" {
			fmt.Sscanf(h, "%f", &hours)
		}
		if h := strings.ReplaceAll(strings.TrimSpace(g.HoursPlayed), ",", ""); h != "" {
			fmt.Sscanf(h, "%f", &recent)
		}
		prof.MostPlayedGames = append(prof.MostPlayedGames, SteamGameStat{
			Name:             strings.TrimSpace(g.GameName),
			HoursPlayedTotal: hours,
			HoursLast2Weeks:  recent,
		})
	}

	out.Profile = prof
	out.HighlightFindings = buildSteamHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

var basicHTMLRe = regexp.MustCompile(`<[^>]+>`)

func stripBasicHTML(s string) string {
	s = basicHTMLRe.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func buildSteamHighlights(o *SteamProfileLookupOutput) []string {
	hi := []string{}
	if o.Profile == nil {
		return hi
	}
	p := o.Profile
	hi = append(hi, fmt.Sprintf("✓ resolved %s → SteamID64 %s (display: %s, custom: %s)",
		o.Input, p.SteamID64, p.SteamID, p.CustomURL))
	if p.MemberSince != "" {
		hi = append(hi, fmt.Sprintf("📅 member since %s (~%.1f years)", p.MemberSince, p.AccountAgeYears))
	}
	if p.PrivacyState != "" {
		hi = append(hi, fmt.Sprintf("🔓 privacy: %s", p.PrivacyState))
	}
	disclosed := []string{}
	if p.RealName != "" {
		disclosed = append(disclosed, "real_name='"+p.RealName+"'")
	}
	if p.Location != "" {
		disclosed = append(disclosed, "location='"+p.Location+"'")
	}
	if p.Summary != "" && len(p.Summary) > 4 {
		summ := p.Summary
		if len(summ) > 100 {
			summ = summ[:100] + "..."
		}
		disclosed = append(disclosed, "summary='"+summ+"'")
	}
	if len(disclosed) > 0 {
		hi = append(hi, fmt.Sprintf("⚡ public self-disclosure: %s", strings.Join(disclosed, " | ")))
	}
	if p.VACBanned {
		hi = append(hi, "🚫 VAC banned — anti-cheat violation on record (adversarial signal)")
	}
	if p.TradeBanState != "" && p.TradeBanState != "None" {
		hi = append(hi, "🚫 trade ban: "+p.TradeBanState+" — scam/abuse signal")
	}
	if p.IsLimited {
		hi = append(hi, "ℹ️  limited account — has not spent $5+ (commonly used for alt accounts/throwaways)")
	}
	if len(p.Groups) > 0 {
		groupNames := []string{}
		for _, g := range p.Groups[:min2(5, len(p.Groups))] {
			if g.IsPrimary {
				groupNames = append(groupNames, "★"+g.GroupName)
			} else {
				groupNames = append(groupNames, g.GroupName)
			}
		}
		hi = append(hi, fmt.Sprintf("👥 %d groups (top: %s)", len(p.Groups), strings.Join(groupNames, ", ")))
	}
	if len(p.MostPlayedGames) > 0 {
		topGame := p.MostPlayedGames[0]
		hi = append(hi, fmt.Sprintf("🎮 most-played: %s (%.1f hrs total, %.1f hrs last 2 weeks)",
			topGame.Name, topGame.HoursPlayedTotal, topGame.HoursLast2Weeks))
	}
	if p.CustomURL != "" {
		hi = append(hi, fmt.Sprintf("🔗 custom_url '%s' often reused as username on Discord/Twitch/Reddit/GitHub — pivot for cross-platform ER", p.CustomURL))
	}
	return hi
}
