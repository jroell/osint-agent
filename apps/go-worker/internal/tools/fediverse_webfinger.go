package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// WebFingerLink is one link from the WebFinger response.
type WebFingerLink struct {
	Rel  string `json:"rel"`
	Type string `json:"type,omitempty"`
	Href string `json:"href,omitempty"`
}

// FediverseProfile is the resolved identity.
type FediverseProfile struct {
	Handle         string   `json:"handle"` // @user@instance
	Username       string   `json:"username"`
	Instance       string   `json:"instance"`
	ProfileURL     string   `json:"profile_url,omitempty"`
	ActorURL       string   `json:"actor_url,omitempty"` // ActivityPub actor URL
	DisplayName    string   `json:"display_name,omitempty"`
	BioPlain       string   `json:"bio_plain,omitempty"` // HTML-stripped
	BioHTML        string   `json:"bio_html,omitempty"`
	AvatarURL      string   `json:"avatar_url,omitempty"`
	HeaderURL      string   `json:"header_url,omitempty"`
	Published      string   `json:"published,omitempty"` // account creation
	FollowersCount int      `json:"followers_count,omitempty"`
	FollowingCount int      `json:"following_count,omitempty"`
	StatusesCount  int      `json:"statuses_count,omitempty"`
	Locked         bool     `json:"manually_approves_followers,omitempty"`
	Discoverable   bool     `json:"discoverable,omitempty"`
	URLs           []string `json:"declared_urls,omitempty"` // Profile-card URLs
	InboxURL       string   `json:"inbox_url,omitempty"`
	OutboxURL      string   `json:"outbox_url,omitempty"`
	PublicKeyPEM   string   `json:"public_key_pem,omitempty"`
}

// FediverseInstance is detected server software.
type FediverseInstance struct {
	Hostname       string `json:"hostname"`
	Software       string `json:"software,omitempty"`
	Version        string `json:"version,omitempty"`
	OpenRegs       bool   `json:"open_registrations,omitempty"`
	TotalUsers     int    `json:"total_users,omitempty"`
	ActiveMonth    int    `json:"active_month,omitempty"`
	ActiveHalfyear int    `json:"active_halfyear,omitempty"`
	NodeInfoURL    string `json:"nodeinfo_url,omitempty"`
}

// FediverseOutput is the response.
type FediverseOutput struct {
	Handle            string             `json:"handle"`
	Profile           *FediverseProfile  `json:"profile,omitempty"`
	WebFingerLinks    []WebFingerLink    `json:"webfinger_links,omitempty"`
	Instance          *FediverseInstance `json:"instance,omitempty"`
	HighlightFindings []string           `json:"highlight_findings"`
	Source            string             `json:"source"`
	TookMs            int64              `json:"tookMs"`
	Note              string             `json:"note,omitempty"`
}

type wfRaw struct {
	Subject string          `json:"subject"`
	Aliases []string        `json:"aliases"`
	Links   []WebFingerLink `json:"links"`
}

type apActorRaw struct {
	Type                      string `json:"type"`
	ID                        string `json:"id"`
	PreferredUsername         string `json:"preferredUsername"`
	Name                      string `json:"name"`
	URL                       any    `json:"url"` // can be string or array of Link objects (PeerTube)
	Summary                   string `json:"summary"`
	Inbox                     string `json:"inbox"`
	Outbox                    string `json:"outbox"`
	Followers                 string `json:"followers"`
	Following                 string `json:"following"`
	Published                 string `json:"published"`
	ManuallyApprovesFollowers bool   `json:"manuallyApprovesFollowers"`
	Discoverable              bool   `json:"discoverable"`
	Icon                      any    `json:"icon"`  // can be {url} or [{url}, ...]
	Image                     any    `json:"image"` // same — flexible
	PublicKey                 struct {
		PEM string `json:"publicKeyPem"`
	} `json:"publicKey"`
	Attachment []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"attachment"`
}

type apCollectionRaw struct {
	TotalItems int `json:"totalItems"`
}

type nodeinfoLinksRaw struct {
	Links []struct {
		Rel  string `json:"rel"`
		Href string `json:"href"`
	} `json:"links"`
}

type nodeinfoRaw struct {
	Software struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"software"`
	OpenRegistrations bool `json:"openRegistrations"`
	Usage             struct {
		Users struct {
			Total          int `json:"total"`
			ActiveMonth    int `json:"activeMonth"`
			ActiveHalfYear int `json:"activeHalfyear"`
		} `json:"users"`
	} `json:"usage"`
}

var htmlStripRe = regexp.MustCompile(`<[^>]+>`)
var multiSpaceRe = regexp.MustCompile(`\s+`)

// FediverseWebFinger resolves a Fediverse handle (@user@instance) into a
// canonical profile via WebFinger + ActivityPub. Works across the entire
// Fediverse: Mastodon, Pleroma, Akkoma, Misskey, PixelFed (federated IG),
// PeerTube (federated YouTube), Lemmy (federated Reddit), Friendica,
// GoToSocial, kbin/Mbin, and any other ActivityPub-compliant server.
//
// Why this matters for ER:
//   - The Fediverse is a major cross-platform identity space invisible to
//     centralized social tools (Twitter API, etc.).
//   - WebFinger is decentralized identity discovery — no anti-bot, no auth.
//   - ActivityPub `publicKey` is a CRYPTOGRAPHIC identity — provable
//     across-instance migration (same key surviving server moves = hard ER).
//   - Server software detection (via nodeinfo) reveals platform-specific
//     ER signals: Lemmy = community moderation/posts, PixelFed = photos,
//     PeerTube = videos, Misskey = Japanese-leaning subculture.
//   - Outbox `totalItems` is a strong "active vs dormant" signal.
//
// The handle must be in `@user@instance`, `user@instance`, or
// `https://instance/@user` form.
func FediverseWebFinger(ctx context.Context, input map[string]any) (*FediverseOutput, error) {
	handle, _ := input["handle"].(string)
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return nil, fmt.Errorf("input.handle required (e.g. '@Mastodon@mastodon.social' or 'Mastodon@mastodon.social')")
	}

	username, instance, err := parseFediverseHandle(handle)
	if err != nil {
		return nil, err
	}

	probeOutbox := true
	if v, ok := input["probe_outbox"].(bool); ok {
		probeOutbox = v
	}
	probeNodeInfo := true
	if v, ok := input["probe_nodeinfo"].(bool); ok {
		probeNodeInfo = v
	}

	out := &FediverseOutput{
		Handle: fmt.Sprintf("@%s@%s", username, instance),
		Source: "WebFinger + ActivityPub (decentralized identity resolution)",
	}
	start := time.Now()

	client := &http.Client{Timeout: 15 * time.Second}

	// Step 1: WebFinger
	wfURL := fmt.Sprintf("https://%s/.well-known/webfinger?resource=acct:%s@%s",
		instance, url.PathEscape(username), instance)
	req, _ := http.NewRequestWithContext(ctx, "GET", wfURL, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/jrd+json,application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webfinger: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		out.Note = fmt.Sprintf("user '%s@%s' not found via WebFinger (404)", username, instance)
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("webfinger %d: %s", resp.StatusCode, string(body))
	}
	var wf wfRaw
	if err := json.NewDecoder(resp.Body).Decode(&wf); err != nil {
		return nil, fmt.Errorf("webfinger decode: %w", err)
	}
	out.WebFingerLinks = wf.Links

	// Find self link (ActivityPub actor)
	actorURL := ""
	profileURL := ""
	avatarURL := ""
	for _, l := range wf.Links {
		switch l.Rel {
		case "self":
			if l.Type == "application/activity+json" || l.Type == "application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\"" {
				actorURL = l.Href
			} else if actorURL == "" {
				actorURL = l.Href
			}
		case "http://webfinger.net/rel/profile-page":
			profileURL = l.Href
		case "http://webfinger.net/rel/avatar":
			avatarURL = l.Href
		}
	}

	prof := &FediverseProfile{
		Handle:     out.Handle,
		Username:   username,
		Instance:   instance,
		ProfileURL: profileURL,
		ActorURL:   actorURL,
		AvatarURL:  avatarURL,
	}

	// Step 2: Fetch ActivityPub actor JSON
	if actorURL != "" {
		actor, err := fetchActor(ctx, client, actorURL)
		if err == nil && actor != nil {
			prof.DisplayName = actor.Name
			prof.BioHTML = actor.Summary
			prof.BioPlain = stripHTMLFed(actor.Summary)
			if prof.AvatarURL == "" {
				prof.AvatarURL = extractImageURL(actor.Icon)
			}
			prof.HeaderURL = extractImageURL(actor.Image)
			prof.Published = actor.Published
			prof.Locked = actor.ManuallyApprovesFollowers
			prof.Discoverable = actor.Discoverable
			prof.InboxURL = actor.Inbox
			prof.OutboxURL = actor.Outbox
			if prof.ProfileURL == "" {
				prof.ProfileURL = extractActorURL(actor.URL)
			}
			if actor.PublicKey.PEM != "" {
				prof.PublicKeyPEM = actor.PublicKey.PEM
			}
			// Collect declared profile-card URLs (Mastodon "verified links")
			for _, a := range actor.Attachment {
				if a.Type == "PropertyValue" && a.Value != "" {
					// strip HTML, look for href values
					txt := stripHTMLFed(a.Value)
					// Also extract raw href= URLs
					for _, m := range regexp.MustCompile(`href="([^"]+)"`).FindAllStringSubmatch(a.Value, -1) {
						if len(m) > 1 {
							prof.URLs = append(prof.URLs, m[1])
						}
					}
					if strings.HasPrefix(txt, "http") {
						prof.URLs = append(prof.URLs, txt)
					}
				}
			}
			prof.URLs = uniqueStrings(prof.URLs)

			// Fetch followers/following counts (ActivityPub OrderedCollection.totalItems)
			if actor.Followers != "" {
				if c, _ := fetchCollectionTotal(ctx, client, actor.Followers); c > 0 {
					prof.FollowersCount = c
				}
			}
			if actor.Following != "" {
				if c, _ := fetchCollectionTotal(ctx, client, actor.Following); c > 0 {
					prof.FollowingCount = c
				}
			}
			if probeOutbox && actor.Outbox != "" {
				if c, _ := fetchCollectionTotal(ctx, client, actor.Outbox); c > 0 {
					prof.StatusesCount = c
				}
			}
		}
	}

	out.Profile = prof

	// Step 3: NodeInfo for server software detection
	if probeNodeInfo {
		if inst := fetchNodeInfo(ctx, client, instance); inst != nil {
			out.Instance = inst
		}
	}

	// Highlights
	out.HighlightFindings = buildFedHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseFediverseHandle(s string) (username, instance string, err error) {
	s = strings.TrimSpace(s)
	// Form: https://instance/@user OR https://instance/users/user
	if strings.HasPrefix(s, "http") {
		u, perr := url.Parse(s)
		if perr != nil {
			return "", "", perr
		}
		instance = u.Host
		path := strings.TrimPrefix(u.Path, "/")
		path = strings.TrimPrefix(path, "@")
		if strings.HasPrefix(path, "users/") {
			username = strings.TrimPrefix(path, "users/")
		} else {
			username = strings.SplitN(path, "/", 2)[0]
		}
		username = strings.TrimSpace(username)
		if username == "" || instance == "" {
			return "", "", fmt.Errorf("could not parse '%s' as Fediverse URL", s)
		}
		return username, instance, nil
	}
	// Form: @user@instance OR user@instance
	s = strings.TrimPrefix(s, "@")
	parts := strings.SplitN(s, "@", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("handle '%s' must be in form '@user@instance' or 'user@instance'", s)
	}
	username = strings.TrimSpace(parts[0])
	instance = strings.TrimSpace(parts[1])
	if username == "" || instance == "" {
		return "", "", fmt.Errorf("invalid handle: empty username or instance")
	}
	return username, instance, nil
}

func fetchActor(ctx context.Context, client *http.Client, actorURL string) (*apActorRaw, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", actorURL, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/activity+json,application/ld+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("actor fetch %d", resp.StatusCode)
	}
	var raw apActorRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

func fetchCollectionTotal(ctx context.Context, client *http.Client, collectionURL string) (int, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", collectionURL, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/activity+json,application/ld+json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("%d", resp.StatusCode)
	}
	var raw apCollectionRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return 0, err
	}
	return raw.TotalItems, nil
}

func fetchNodeInfo(ctx context.Context, client *http.Client, instance string) *FediverseInstance {
	// Step 1: well-known/nodeinfo for the discovery doc
	wkURL := fmt.Sprintf("https://%s/.well-known/nodeinfo", instance)
	req, _ := http.NewRequestWithContext(ctx, "GET", wkURL, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var wkRaw nodeinfoLinksRaw
	if err := json.NewDecoder(resp.Body).Decode(&wkRaw); err != nil {
		return nil
	}
	if len(wkRaw.Links) == 0 {
		return nil
	}
	// Pick highest-version nodeinfo doc (typically last)
	niURL := wkRaw.Links[len(wkRaw.Links)-1].Href
	req2, _ := http.NewRequestWithContext(ctx, "GET", niURL, nil)
	req2.Header.Set("User-Agent", "osint-agent/0.1")
	resp2, err := client.Do(req2)
	if err != nil {
		return nil
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		return nil
	}
	var ni nodeinfoRaw
	if err := json.NewDecoder(resp2.Body).Decode(&ni); err != nil {
		return nil
	}
	return &FediverseInstance{
		Hostname:       instance,
		Software:       ni.Software.Name,
		Version:        ni.Software.Version,
		OpenRegs:       ni.OpenRegistrations,
		TotalUsers:     ni.Usage.Users.Total,
		ActiveMonth:    ni.Usage.Users.ActiveMonth,
		ActiveHalfyear: ni.Usage.Users.ActiveHalfYear,
		NodeInfoURL:    niURL,
	}
}

// extractImageURL handles ActivityPub icon/image fields which may be a
// single Image object {url, ...} (Mastodon) or an array of them (PeerTube).
func extractImageURL(v any) string {
	switch x := v.(type) {
	case map[string]any:
		if u, _ := x["url"].(string); u != "" {
			return u
		}
	case []any:
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				if u, _ := m["url"].(string); u != "" {
					return u
				}
			}
		}
	}
	return ""
}

// extractActorURL handles ActivityPub actors where `url` may be a string
// (Mastodon/Pleroma) or an array of Link objects (PeerTube).
func extractActorURL(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				if mt, _ := m["mediaType"].(string); mt == "text/html" {
					if href, _ := m["href"].(string); href != "" {
						return href
					}
				}
			}
		}
		// fallback: first array item with an href
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				if href, _ := m["href"].(string); href != "" {
					return href
				}
			}
		}
	}
	return ""
}

// stripHTMLFed strips tags AND decodes HTML character references
// (&amp;, &#39;, &nbsp;, etc.) — the latter was missing pre-fix and
// silently corrupted Mastodon bio fields and account-property values.
// See TestStripHTMLVariants_EntityDecoding.
func stripHTMLFed(s string) string {
	s = htmlStripRe.ReplaceAllString(s, " ")
	s = htmlPkgUnescape(s)
	s = normalizeHTMLWhitespace(s)
	s = multiSpaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func buildFedHighlights(o *FediverseOutput) []string {
	hi := []string{}
	if o.Profile != nil {
		p := o.Profile
		hi = append(hi, fmt.Sprintf("✓ resolved %s → %s", o.Handle, p.ActorURL))
		if p.DisplayName != "" {
			hi = append(hi, fmt.Sprintf("display name: %s", p.DisplayName))
		}
		if p.Published != "" {
			hi = append(hi, fmt.Sprintf("account published: %s", p.Published))
		}
		if p.FollowersCount > 0 || p.FollowingCount > 0 || p.StatusesCount > 0 {
			hi = append(hi, fmt.Sprintf("metrics: %d followers, %d following, %d posts", p.FollowersCount, p.FollowingCount, p.StatusesCount))
		}
		if len(p.URLs) > 0 {
			hi = append(hi, fmt.Sprintf("⚡ %d declared profile-card URLs (verified links): %s", len(p.URLs), strings.Join(p.URLs, " | ")))
		}
		if p.Locked {
			hi = append(hi, "🔒 manually-approves-followers (locked account)")
		}
		if !p.Discoverable && p.Discoverable == false {
			// only flag if explicitly false (Mastodon)
		}
		if p.PublicKeyPEM != "" {
			hi = append(hi, "✓ ActivityPub publicKey present — can verify cross-instance migrations cryptographically")
		}
	}
	if o.Instance != nil {
		i := o.Instance
		hi = append(hi, fmt.Sprintf("📡 instance: %s — software: %s %s — open_regs=%v — %d total users (%d active/month)",
			i.Hostname, i.Software, i.Version, i.OpenRegs, i.TotalUsers, i.ActiveMonth))
		// Software-specific ER context
		switch strings.ToLower(i.Software) {
		case "lemmy":
			hi = append(hi, "ℹ️  Lemmy = federated Reddit-clone — posts/communities are the primary identity signal")
		case "pixelfed":
			hi = append(hi, "ℹ️  PixelFed = federated Instagram-clone — photo uploads are primary content")
		case "peertube":
			hi = append(hi, "ℹ️  PeerTube = federated YouTube-clone — videos are primary content")
		case "misskey", "calckey", "firefish", "iceshrimp", "sharkey":
			hi = append(hi, "ℹ️  Misskey-family — Japanese-leaning culture, drive feature, MFM markup")
		case "pleroma", "akkoma":
			hi = append(hi, "ℹ️  Pleroma/Akkoma — Mastodon-compatible but with extended features (longer posts, quote-posts)")
		}
	}
	return hi
}
