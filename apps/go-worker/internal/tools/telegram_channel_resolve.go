package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type TelegramMessage struct {
	Text       string `json:"text"`
	Date       string `json:"date,omitempty"`
	Views      string `json:"views,omitempty"`
	URL        string `json:"url,omitempty"`
}

type TelegramChannelOutput struct {
	Username        string            `json:"username"`
	URL             string            `json:"url"`
	Title           string            `json:"title,omitempty"`
	Bio             string            `json:"bio,omitempty"`
	IsVerified      bool              `json:"is_verified"`
	IsPrivate       bool              `json:"is_private"`
	NotFound        bool              `json:"not_found,omitempty"`
	PhotoURL        string            `json:"photo_url,omitempty"`
	Counters        map[string]string `json:"counters"`           // subscribers, photos, videos, links, etc.
	SubscriberCount string            `json:"subscriber_count,omitempty"`
	RecentMessages  []TelegramMessage `json:"recent_messages,omitempty"`
	HighlightFindings []string        `json:"highlight_findings"`
	Source          string            `json:"source"`
	TookMs          int64             `json:"tookMs"`
	Note            string            `json:"note,omitempty"`
}

var (
	tgOgTitleRE        = regexp.MustCompile(`<meta property="og:title"\s+content="([^"]+)"`)
	tgOgDescRE         = regexp.MustCompile(`<meta property="og:description"\s+content="([^"]+)"`)
	tgOgImageRE        = regexp.MustCompile(`<meta property="og:image"\s+content="([^"]+)"`)
	tgCounterRE        = regexp.MustCompile(`<div class="tgme_channel_info_counter">\s*<span class="counter_value">([^<]+)</span>\s*<span class="counter_type">([^<]+)</span>`)
	tgVerifiedRE       = regexp.MustCompile(`tgme_verified_icon|class="verified-icon"`)
	tgPrivateRE        = regexp.MustCompile(`tgme_page_status_text[^>]*>(?:Private|Channel)|This channel is private|If you have <a [^>]*>Telegram</a>, you can view`)
	tgMessageTextRE    = regexp.MustCompile(`(?s)tgme_widget_message_text[^"]*"\s+dir="auto">(.*?)</div>`)
	tgMessageDateRE    = regexp.MustCompile(`<time[^>]+datetime="([^"]+)"`)
	tgMessageViewsRE   = regexp.MustCompile(`tgme_widget_message_views">([^<]+)<`)
	tgMessageHrefRE    = regexp.MustCompile(`tgme_widget_message_date"\s+href="([^"]+)"`)
	tgMessageBlockRE   = regexp.MustCompile(`(?s)<div class="tgme_widget_message_wrap[^"]*"[^>]*>.*?</div>\s*</div>\s*</div>\s*</div>`)
)

// TelegramChannelResolve fetches the public preview of a Telegram channel/user
// via t.me/s/<username> (channel-style preview that shows recent messages
// even when not logged in). Falls back to t.me/<username> for users with no
// public message stream.
//
// Use cases:
//   - Threat actor research: many APT groups + crypto scams operate Telegram
//     channels publicly
//   - News-source distribution maps: which media orgs run Telegram
//   - Influencer recon: subscriber counts, post frequency, recent topics
//   - Fills the gap between Discord (iter-29) and federated social tools
//
// Free, no auth. Returns name, bio, subscriber count, recent message snippets
// (with dates+views), verified/private flags, photo URL.
func TelegramChannelResolve(ctx context.Context, input map[string]any) (*TelegramChannelOutput, error) {
	raw, _ := input["channel"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("input.channel required (e.g. 'durov', '@durov', 'https://t.me/durov')")
	}

	// Extract username from various inputs.
	username := raw
	for _, prefix := range []string{
		"https://t.me/", "http://t.me/", "t.me/",
		"https://telegram.me/", "telegram.me/",
		"https://telegram.dog/", "telegram.dog/",
		"@",
	} {
		if strings.HasPrefix(strings.ToLower(username), prefix) {
			username = username[len(prefix):]
			break
		}
	}
	username = strings.TrimPrefix(username, "s/")
	username = strings.TrimPrefix(username, "joinchat/")
	if i := strings.IndexAny(username, "?/#"); i >= 0 {
		username = username[:i]
	}
	if username == "" {
		return nil, errors.New("could not extract username from input")
	}

	start := time.Now()
	out := &TelegramChannelOutput{
		Username: username,
		URL:      "https://t.me/" + username,
		Source:   "t.me/s public preview",
		Counters: map[string]string{},
	}

	// Try /s/<username> first (channel preview with messages).
	previewURL := "https://t.me/s/" + url.PathEscape(username)
	body, err := tgFetch(ctx, previewURL)
	if err != nil {
		return nil, fmt.Errorf("preview fetch: %w", err)
	}

	// Detect "channel doesn't exist" — t.me returns the homepage HTML for invalid usernames
	if strings.Contains(body, "Telegram: Contact @") && !strings.Contains(body, "tgme_channel_info") && !strings.Contains(body, "tgme_widget_message") {
		// Try /<username> bare URL for user-style preview
		userURL := "https://t.me/" + url.PathEscape(username)
		body, _ = tgFetch(ctx, userURL)
		if !strings.Contains(body, "og:title") || strings.Contains(body, "If you have") {
			out.NotFound = true
			out.Note = "Channel/user not found or no public preview"
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
	}

	// Title + bio
	if m := tgOgTitleRE.FindStringSubmatch(body); len(m) > 1 {
		out.Title = strings.TrimSpace(m[1])
	}
	if m := tgOgDescRE.FindStringSubmatch(body); len(m) > 1 {
		out.Bio = strings.TrimSpace(m[1])
	}
	if m := tgOgImageRE.FindStringSubmatch(body); len(m) > 1 {
		out.PhotoURL = m[1]
	}

	// Verified
	out.IsVerified = tgVerifiedRE.MatchString(body)

	// Private
	if tgPrivateRE.MatchString(body) {
		out.IsPrivate = true
	}

	// Counters
	for _, m := range tgCounterRE.FindAllStringSubmatch(body, -1) {
		if len(m) >= 3 {
			value := strings.TrimSpace(m[1])
			label := strings.TrimSpace(strings.ToLower(m[2]))
			out.Counters[label] = value
			if label == "subscribers" || label == "members" {
				out.SubscriberCount = value
			}
		}
	}

	// Recent messages
	for _, m := range tgMessageTextRE.FindAllStringSubmatch(body, -1) {
		if len(m) < 2 {
			continue
		}
		text := stripHTMLBare(m[1])
		if len(text) > 400 {
			text = text[:400] + "…"
		}
		out.RecentMessages = append(out.RecentMessages, TelegramMessage{Text: text})
		if len(out.RecentMessages) >= 10 {
			break
		}
	}
	// Try to enrich with date/views/url by matching message blocks
	dateMatches := tgMessageDateRE.FindAllStringSubmatch(body, -1)
	viewsMatches := tgMessageViewsRE.FindAllStringSubmatch(body, -1)
	hrefMatches := tgMessageHrefRE.FindAllStringSubmatch(body, -1)
	for i := range out.RecentMessages {
		if i < len(dateMatches) && len(dateMatches[i]) > 1 {
			out.RecentMessages[i].Date = dateMatches[i][1]
		}
		if i < len(viewsMatches) && len(viewsMatches[i]) > 1 {
			out.RecentMessages[i].Views = viewsMatches[i][1]
		}
		if i < len(hrefMatches) && len(hrefMatches[i]) > 1 {
			out.RecentMessages[i].URL = hrefMatches[i][1]
		}
	}

	// Highlights
	highlights := []string{}
	if out.Title != "" {
		highlights = append(highlights, fmt.Sprintf("'%s' (@%s)", out.Title, out.Username))
	}
	if out.SubscriberCount != "" {
		highlights = append(highlights, fmt.Sprintf("subscribers: %s", out.SubscriberCount))
	}
	flags := []string{}
	if out.IsVerified {
		flags = append(flags, "✓ verified")
	}
	if out.IsPrivate {
		flags = append(flags, "🔒 private")
	}
	if len(flags) > 0 {
		highlights = append(highlights, strings.Join(flags, " "))
	}
	if len(out.RecentMessages) > 0 {
		highlights = append(highlights, fmt.Sprintf("%d recent messages extracted", len(out.RecentMessages)))
	}
	if len(out.Counters) > 0 {
		ctr := []string{}
		for k, v := range out.Counters {
			ctr = append(ctr, fmt.Sprintf("%s=%s", k, v))
		}
		highlights = append(highlights, "counters: "+strings.Join(ctr, ", "))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func tgFetch(ctx context.Context, url string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return string(body), nil
}
