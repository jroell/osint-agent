package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// LichessPerformance is rating + game count for one time control.
type LichessPerformance struct {
	TimeControl string `json:"time_control"`
	Rating      int    `json:"rating"`
	Games       int    `json:"games"`
	Provisional bool   `json:"provisional,omitempty"`
}

// LichessProfile is the profile data block.
type LichessProfile struct {
	Bio          string `json:"bio,omitempty"`
	Country      string `json:"country,omitempty"` // ISO country code (e.g. "US"); also surfaced as flag
	Location     string `json:"location,omitempty"`
	RealName     string `json:"real_name,omitempty"`
	FirstName    string `json:"first_name,omitempty"`
	LastName     string `json:"last_name,omitempty"`
	FIDERating   int    `json:"fide_rating,omitempty"`
	USCFRating   int    `json:"uscf_rating,omitempty"`
	ECFRating    int    `json:"ecf_rating,omitempty"`
	Links        string `json:"links,omitempty"`
}

// LichessUserOutput is the response.
type LichessUserOutput struct {
	ID             string               `json:"id"`
	Username       string               `json:"username"`
	Title          string               `json:"title,omitempty"` // GM | IM | WGM | FM | CM | NM
	Online         bool                 `json:"online,omitempty"`
	Disabled       bool                 `json:"disabled,omitempty"`
	Patron         bool                 `json:"patron,omitempty"`
	Verified       bool                 `json:"verified,omitempty"`
	TosViolation   bool                 `json:"tos_violation,omitempty"`
	CreatedISO     string               `json:"created_iso,omitempty"`
	SeenISO        string               `json:"seen_iso,omitempty"`
	AccountAgeYears float64             `json:"account_age_years,omitempty"`
	TotalGames     int                  `json:"total_games,omitempty"`
	RatedGames     int                  `json:"rated_games,omitempty"`
	Wins           int                  `json:"wins,omitempty"`
	Losses         int                  `json:"losses,omitempty"`
	Draws          int                  `json:"draws,omitempty"`
	PlayTimeSec    int64                `json:"play_time_seconds,omitempty"`
	Performance    []LichessPerformance `json:"performance,omitempty"`
	Profile        *LichessProfile      `json:"profile,omitempty"`
	URL            string               `json:"profile_url,omitempty"`
	Followers      int                  `json:"followers,omitempty"`
	Following      int                  `json:"following,omitempty"`
	BioEmails      []string             `json:"bio_emails,omitempty"`
	BioURLs        []string             `json:"bio_urls,omitempty"`
	HighlightFindings []string          `json:"highlight_findings"`
	Source         string               `json:"source"`
	TookMs         int64                `json:"tookMs"`
	Note           string               `json:"note,omitempty"`
}

type lichessRawProfile struct {
	Bio        string `json:"bio"`
	Country    string `json:"country"`
	Location   string `json:"location"`
	RealName   string `json:"realName"`
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	FIDERating int    `json:"fideRating"`
	USCFRating int    `json:"uscfRating"`
	ECFRating  int    `json:"ecfRating"`
	Links      string `json:"links"`
}

type lichessRawUser struct {
	ID           string                       `json:"id"`
	Username     string                       `json:"username"`
	Title        string                       `json:"title"`
	Online       bool                         `json:"online"`
	Patron       bool                         `json:"patron"`
	Disabled     bool                         `json:"disabled"`
	Verified     bool                         `json:"verified"`
	TosViolation bool                         `json:"tosViolation"`
	CreatedAt    int64                        `json:"createdAt"`
	SeenAt       int64                        `json:"seenAt"`
	URL          string                       `json:"url"`
	Followers    int                          `json:"nbFollowers"`
	Following    int                          `json:"nbFollowing"`
	Profile      lichessRawProfile            `json:"profile"`
	PlayTime     struct {
		Total int64 `json:"total"`
		TV    int64 `json:"tv"`
	} `json:"playTime"`
	Count struct {
		All     int `json:"all"`
		Rated   int `json:"rated"`
		Win     int `json:"win"`
		Loss    int `json:"loss"`
		Draw    int `json:"draw"`
	} `json:"count"`
	Perfs map[string]struct {
		Games       int  `json:"games"`
		Rating      int  `json:"rating"`
		Provisional bool `json:"prov"`
	} `json:"perfs"`
}

// LichessUserLookup queries Lichess.org's public API for chess player ER.
// No auth required for public profiles.
//
// Why this matters for ER:
//   - Lichess is open-source + free; ~200M+ registered players globally.
//   - Profile fields are SELF-DISCLOSED but commonly populated:
//     real_name, country (with ISO code → flag), city, bio (often emails!),
//     FIDE rating, USCF rating, ECF rating.
//   - FIDE rating is verifiable in FIDE's official database (fide.com)
//     and grants chess-title pivot (GM/IM/FM/etc are awarded by FIDE).
//   - Account age + total game count + play time = serious-player signal.
//   - Performance ratings across time controls (bullet/blitz/rapid/classical)
//     reveal preferred game style — different from over-the-board chess.
//   - Bio is unstructured — frequently contains emails, Twitter handles,
//     YouTube/Twitch links — strong cross-platform ER pivot. We extract
//     emails + URLs separately for easy follow-up.
func LichessUserLookup(ctx context.Context, input map[string]any) (*LichessUserOutput, error) {
	username, _ := input["username"].(string)
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")

	// Strip URL if pasted
	if strings.HasPrefix(username, "http") {
		// e.g. https://lichess.org/@/penguingm1 → penguingm1
		clean := strings.TrimRight(username, "/")
		idx := strings.LastIndex(clean, "/")
		if idx >= 0 {
			username = clean[idx+1:]
		}
	}
	if username == "" {
		return nil, fmt.Errorf("input.username required (Lichess username, e.g. 'DrNykterstein')")
	}

	out := &LichessUserOutput{
		Source: "lichess.org/api/user",
	}
	start := time.Now()

	endpoint := "https://lichess.org/api/user/" + username
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lichess fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		out.Note = fmt.Sprintf("user '%s' not found on Lichess", username)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("lichess %d: %s", resp.StatusCode, string(body))
	}

	var raw lichessRawUser
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("lichess decode: %w", err)
	}
	if raw.ID == "" {
		out.Note = "lichess returned empty user object"
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	out.ID = raw.ID
	out.Username = raw.Username
	out.Title = raw.Title
	out.Online = raw.Online
	out.Disabled = raw.Disabled
	out.Patron = raw.Patron
	out.Verified = raw.Verified
	out.TosViolation = raw.TosViolation
	out.URL = raw.URL
	out.Followers = raw.Followers
	out.Following = raw.Following

	if raw.CreatedAt > 0 {
		t := time.Unix(raw.CreatedAt/1000, 0).UTC()
		out.CreatedISO = t.Format(time.RFC3339)
		out.AccountAgeYears = time.Since(t).Hours() / (24 * 365.25)
	}
	if raw.SeenAt > 0 {
		out.SeenISO = time.Unix(raw.SeenAt/1000, 0).UTC().Format(time.RFC3339)
	}

	out.TotalGames = raw.Count.All
	out.RatedGames = raw.Count.Rated
	out.Wins = raw.Count.Win
	out.Losses = raw.Count.Loss
	out.Draws = raw.Count.Draw
	out.PlayTimeSec = raw.PlayTime.Total

	// Performance — sort by games desc to surface preferred time control first
	for tc, p := range raw.Perfs {
		// Skip empty perfs (provisional + 0 games)
		if p.Games == 0 && p.Provisional {
			continue
		}
		out.Performance = append(out.Performance, LichessPerformance{
			TimeControl: tc,
			Rating:      p.Rating,
			Games:       p.Games,
			Provisional: p.Provisional,
		})
	}
	sort.SliceStable(out.Performance, func(i, j int) bool {
		return out.Performance[i].Games > out.Performance[j].Games
	})

	// Profile
	rp := raw.Profile
	hasProfile := rp.Bio != "" || rp.Country != "" || rp.Location != "" || rp.RealName != "" ||
		rp.FirstName != "" || rp.LastName != "" || rp.FIDERating > 0 || rp.USCFRating > 0 || rp.ECFRating > 0
	if hasProfile {
		realName := rp.RealName
		if realName == "" && (rp.FirstName != "" || rp.LastName != "") {
			realName = strings.TrimSpace(rp.FirstName + " " + rp.LastName)
		}
		out.Profile = &LichessProfile{
			Bio:        rp.Bio,
			Country:    rp.Country,
			Location:   rp.Location,
			RealName:   realName,
			FirstName:  rp.FirstName,
			LastName:   rp.LastName,
			FIDERating: rp.FIDERating,
			USCFRating: rp.USCFRating,
			ECFRating:  rp.ECFRating,
			Links:      rp.Links,
		}

		// Extract emails + URLs from bio (and links field)
		searchText := rp.Bio + " " + rp.Links
		out.BioEmails = uniqueStrings(emailRegex.FindAllString(searchText, -1))
		// lower-case emails for canonicalization
		for i, e := range out.BioEmails {
			out.BioEmails[i] = strings.ToLower(e)
		}
		// URL pattern (avoid duplicate vs urlRegex from another file by using local copy)
		urls := lichessExtractURLs(searchText)
		out.BioURLs = uniqueStrings(urls)
	}

	out.HighlightFindings = buildLichessHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

var lichessURLRe = regexp.MustCompile(`https?://[^\s)\]"'<>]+`)

func lichessExtractURLs(text string) []string {
	matches := lichessURLRe.FindAllString(text, -1)
	cleaned := []string{}
	for _, m := range matches {
		// strip trailing punctuation
		m = strings.TrimRight(m, ".,);]'\"!?")
		if m != "" {
			cleaned = append(cleaned, m)
		}
	}
	return cleaned
}

func buildLichessHighlights(o *LichessUserOutput) []string {
	hi := []string{}
	titleStr := ""
	if o.Title != "" {
		titleStr = o.Title + " "
	}
	createdDate := ""
	if len(o.CreatedISO) >= 10 {
		createdDate = o.CreatedISO[:10]
	}
	hi = append(hi, fmt.Sprintf("✓ %s%s — created %s (%.1fy), %d games (%d rated), %d wins / %d losses / %d draws",
		titleStr, o.Username, createdDate, o.AccountAgeYears, o.TotalGames, o.RatedGames, o.Wins, o.Losses, o.Draws))

	// Title flag — chess titles are FIDE-awarded, verifiable
	if o.Title != "" {
		flagsByTitle := map[string]string{
			"GM":  "🏆 GM (Grandmaster) title — FIDE-verified, top ~2000 players globally",
			"IM":  "🏆 IM (International Master) title — FIDE-verified",
			"WGM": "🏆 Woman Grandmaster title — FIDE-verified",
			"WIM": "🏆 Woman International Master title — FIDE-verified",
			"FM":  "🏅 FM (FIDE Master) title — FIDE-verified",
			"WFM": "🏅 Woman FIDE Master title — FIDE-verified",
			"CM":  "🏅 CM (Candidate Master) title — FIDE-verified",
			"WCM": "🏅 Woman Candidate Master title — FIDE-verified",
			"NM":  "🏅 NM (National Master) — Lichess-awarded based on play strength",
			"BOT": "🤖 BOT account — automated play",
			"LM":  "🏅 LM (Lichess Master) — community honorific",
		}
		if msg, ok := flagsByTitle[o.Title]; ok {
			hi = append(hi, msg)
		} else {
			hi = append(hi, "title: "+o.Title)
		}
	}

	if o.Profile != nil {
		p := o.Profile
		disclosed := []string{}
		if p.RealName != "" {
			disclosed = append(disclosed, "real_name='"+p.RealName+"'")
		}
		if p.Country != "" {
			disclosed = append(disclosed, "country="+p.Country)
		}
		if p.Location != "" {
			disclosed = append(disclosed, "location='"+p.Location+"'")
		}
		if len(disclosed) > 0 {
			hi = append(hi, "⚡ self-disclosure: "+strings.Join(disclosed, " | "))
		}
		ratings := []string{}
		if p.FIDERating > 0 {
			ratings = append(ratings, fmt.Sprintf("FIDE=%d", p.FIDERating))
		}
		if p.USCFRating > 0 {
			ratings = append(ratings, fmt.Sprintf("USCF=%d", p.USCFRating))
		}
		if p.ECFRating > 0 {
			ratings = append(ratings, fmt.Sprintf("ECF=%d", p.ECFRating))
		}
		if len(ratings) > 0 {
			hi = append(hi, "♟ external ratings: "+strings.Join(ratings, ", ")+" — verifiable via FIDE / USCF / ECF databases")
		}
	}

	if len(o.Performance) > 0 {
		topPerfs := []string{}
		for _, p := range o.Performance[:min2(4, len(o.Performance))] {
			topPerfs = append(topPerfs, fmt.Sprintf("%s=%d (%dg)", p.TimeControl, p.Rating, p.Games))
		}
		hi = append(hi, "🎯 top time controls: "+strings.Join(topPerfs, ", "))
	}

	if len(o.BioEmails) > 0 {
		hi = append(hi, fmt.Sprintf("📧 %d email(s) extracted from bio: %s", len(o.BioEmails), strings.Join(o.BioEmails, ", ")))
	}
	if len(o.BioURLs) > 0 {
		hi = append(hi, fmt.Sprintf("🔗 %d URL(s) in bio: %s", len(o.BioURLs), strings.Join(o.BioURLs, " | ")))
	}

	if o.PlayTimeSec > 3600*24*7 {
		days := o.PlayTimeSec / 86400
		hi = append(hi, fmt.Sprintf("⏰ %d days of total play time — heavy investment signal", days))
	}
	if o.TosViolation {
		hi = append(hi, "🚫 TOS violation flag — possible cheating/account abuse history")
	}
	if o.Disabled {
		hi = append(hi, "🚫 account disabled")
	}
	if o.Patron {
		hi = append(hi, "💝 Patron — financial supporter of Lichess")
	}
	if o.Followers > 100 {
		hi = append(hi, fmt.Sprintf("👥 %d followers — public chess identity", o.Followers))
	}
	return hi
}
