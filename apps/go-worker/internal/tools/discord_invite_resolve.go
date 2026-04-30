package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DiscordGuildInfo struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	IconURL             string   `json:"icon_url,omitempty"`
	BannerURL           string   `json:"banner_url,omitempty"`
	Features            []string `json:"features,omitempty"`
	VanityURLCode       string   `json:"vanity_url_code,omitempty"`
	VerificationLevel   int      `json:"verification_level"`
	NSFWLevel           int      `json:"nsfw_level"`
	PremiumTier         int      `json:"premium_subscription_count_tier"`
	PremiumBoostCount   int      `json:"premium_subscription_count"`
	HasPartnered        bool     `json:"is_partnered"`
	HasVerified         bool     `json:"is_verified"`
	HasCommunity        bool     `json:"is_community"`
}

type DiscordChannelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type int    `json:"type"`
}

type DiscordInviterInfo struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	GlobalName    string `json:"global_name,omitempty"`
	AvatarURL     string `json:"avatar_url,omitempty"`
	IsBot         bool   `json:"is_bot"`
	IsSystem      bool   `json:"is_system"`
}

type DiscordInviteResolveOutput struct {
	Code              string              `json:"invite_code"`
	URL               string              `json:"invite_url"`
	Type              int                 `json:"invite_type"`
	IsVanity          bool                `json:"is_vanity_url"`
	ExpiresAt         string              `json:"expires_at,omitempty"`
	Guild             *DiscordGuildInfo   `json:"guild,omitempty"`
	Channel           *DiscordChannelInfo `json:"channel,omitempty"`
	Inviter           *DiscordInviterInfo `json:"inviter,omitempty"`
	ApproxMemberCount int                 `json:"approximate_member_count"`
	ApproxOnlineCount int                 `json:"approximate_presence_count"`
	HighlightFindings []string            `json:"highlight_findings"`
	RawResponse       map[string]any      `json:"-"`
	Source            string              `json:"source"`
	TookMs            int64               `json:"tookMs"`
	Note              string              `json:"note,omitempty"`
}

// DiscordInviteResolve resolves a public Discord invite code to server
// metadata via Discord's free no-auth API endpoint.
//
// Use cases:
//   - Threat actor research: many crypto scams + APT groups operate Discord
//     servers; resolving invite codes reveals server name, size, and
//     features without joining
//   - Community mapping: brand support servers, gaming communities
//   - Phishing investigation: scammers post Discord invites in tweets/comments;
//     this tool reveals what server they're funneling victims to
//
// Free, no auth, no rate limit issues. The endpoint:
//   GET https://discord.com/api/v10/invites/{code}?with_counts=true&with_expiration=true
//
// Accepts: bare code ("abc123"), invite URL ("discord.gg/abc123" or
// "discord.com/invite/abc123"), or vanity URL.
func DiscordInviteResolve(ctx context.Context, input map[string]any) (*DiscordInviteResolveOutput, error) {
	rawCode, _ := input["invite_code"].(string)
	rawCode = strings.TrimSpace(rawCode)
	if rawCode == "" {
		return nil, errors.New("input.invite_code required (e.g. 'abc123' or 'https://discord.gg/abc123')")
	}
	// Extract code from various formats
	code := rawCode
	for _, prefix := range []string{"https://discord.gg/", "http://discord.gg/", "discord.gg/",
		"https://discord.com/invite/", "http://discord.com/invite/", "discord.com/invite/",
		"https://discordapp.com/invite/", "discordapp.com/invite/"} {
		if strings.HasPrefix(strings.ToLower(rawCode), prefix) {
			code = rawCode[len(prefix):]
			break
		}
	}
	// Strip query string / fragments
	if i := strings.IndexAny(code, "?#"); i >= 0 {
		code = code[:i]
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errors.New("could not extract invite code from input")
	}

	start := time.Now()
	out := &DiscordInviteResolveOutput{
		Code:   code,
		URL:    "https://discord.gg/" + code,
		Source: "discord.com/api/v10",
	}

	endpoint := fmt.Sprintf("https://discord.com/api/v10/invites/%s?with_counts=true&with_expiration=true",
		url.PathEscape(code))
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/discord-invite")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discord fetch failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == 404 {
		out.Note = "Invite code not found, expired, or revoked"
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("discord status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("discord parse: %w", err)
	}
	out.RawResponse = raw

	// Top-level fields
	if v, ok := raw["type"].(float64); ok {
		out.Type = int(v)
	}
	if v, ok := raw["expires_at"].(string); ok {
		out.ExpiresAt = v
	}
	if v, ok := raw["approximate_member_count"].(float64); ok {
		out.ApproxMemberCount = int(v)
	}
	if v, ok := raw["approximate_presence_count"].(float64); ok {
		out.ApproxOnlineCount = int(v)
	}

	// Guild
	if g, ok := raw["guild"].(map[string]any); ok {
		guild := &DiscordGuildInfo{}
		if v, ok := g["id"].(string); ok {
			guild.ID = v
		}
		if v, ok := g["name"].(string); ok {
			guild.Name = v
		}
		if v, ok := g["description"].(string); ok {
			guild.Description = v
		}
		if v, ok := g["vanity_url_code"].(string); ok {
			guild.VanityURLCode = v
			out.IsVanity = (v == code)
		}
		if v, ok := g["icon"].(string); ok && v != "" {
			ext := "png"
			if strings.HasPrefix(v, "a_") {
				ext = "gif" // animated
			}
			guild.IconURL = fmt.Sprintf("https://cdn.discordapp.com/icons/%s/%s.%s?size=512", guild.ID, v, ext)
		}
		if v, ok := g["banner"].(string); ok && v != "" {
			guild.BannerURL = fmt.Sprintf("https://cdn.discordapp.com/banners/%s/%s.png?size=512", guild.ID, v)
		}
		if v, ok := g["features"].([]any); ok {
			for _, f := range v {
				if fs, ok := f.(string); ok {
					guild.Features = append(guild.Features, fs)
				}
			}
		}
		// Derive flags from features
		for _, f := range guild.Features {
			switch f {
			case "PARTNERED":
				guild.HasPartnered = true
			case "VERIFIED":
				guild.HasVerified = true
			case "COMMUNITY":
				guild.HasCommunity = true
			}
		}
		if v, ok := g["verification_level"].(float64); ok {
			guild.VerificationLevel = int(v)
		}
		if v, ok := g["nsfw_level"].(float64); ok {
			guild.NSFWLevel = int(v)
		}
		if v, ok := g["premium_subscription_count"].(float64); ok {
			guild.PremiumBoostCount = int(v)
		}
		out.Guild = guild
	}

	// Channel
	if c, ok := raw["channel"].(map[string]any); ok {
		ch := &DiscordChannelInfo{}
		if v, ok := c["id"].(string); ok {
			ch.ID = v
		}
		if v, ok := c["name"].(string); ok {
			ch.Name = v
		}
		if v, ok := c["type"].(float64); ok {
			ch.Type = int(v)
		}
		out.Channel = ch
	}

	// Inviter
	if u, ok := raw["inviter"].(map[string]any); ok {
		inv := &DiscordInviterInfo{}
		if v, ok := u["id"].(string); ok {
			inv.ID = v
		}
		if v, ok := u["username"].(string); ok {
			inv.Username = v
		}
		if v, ok := u["global_name"].(string); ok {
			inv.GlobalName = v
		}
		if v, ok := u["avatar"].(string); ok && v != "" {
			inv.AvatarURL = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png?size=256", inv.ID, v)
		}
		if v, ok := u["bot"].(bool); ok {
			inv.IsBot = v
		}
		if v, ok := u["system"].(bool); ok {
			inv.IsSystem = v
		}
		out.Inviter = inv
	}

	// Highlights
	highlights := []string{}
	if out.Guild != nil {
		highlights = append(highlights, fmt.Sprintf("Server: %s (id %s) — %d members, %d online",
			out.Guild.Name, out.Guild.ID, out.ApproxMemberCount, out.ApproxOnlineCount))
		if out.Guild.HasPartnered {
			highlights = append(highlights, "✓ PARTNERED server")
		}
		if out.Guild.HasVerified {
			highlights = append(highlights, "✓ VERIFIED server")
		}
		if out.Guild.HasCommunity {
			highlights = append(highlights, "✓ COMMUNITY server")
		}
		if out.Guild.PremiumBoostCount > 0 {
			highlights = append(highlights, fmt.Sprintf("%d Nitro boosts (paid premium tier)", out.Guild.PremiumBoostCount))
		}
		if out.Guild.VerificationLevel >= 3 {
			highlights = append(highlights, fmt.Sprintf("HIGH verification level (%d) — phone/email required", out.Guild.VerificationLevel))
		}
	}
	if out.Channel != nil {
		highlights = append(highlights, fmt.Sprintf("Funneling to channel: #%s", out.Channel.Name))
	}
	if out.Inviter != nil && out.Inviter.Username != "" {
		highlights = append(highlights, fmt.Sprintf("Invite created by: %s (id %s)", out.Inviter.Username, out.Inviter.ID))
	}
	if out.ExpiresAt != "" {
		highlights = append(highlights, fmt.Sprintf("Expires: %s", out.ExpiresAt))
	} else {
		highlights = append(highlights, "Permanent invite (never expires)")
	}
	out.HighlightFindings = highlights

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
