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

// FarcasterVerification is one verified blockchain address.
type FarcasterVerification struct {
	Protocol string `json:"protocol"` // ethereum | solana
	Address  string `json:"address"`
	Chain    string `json:"chain,omitempty"`
}

// FarcasterCast is one post by the user.
type FarcasterCast struct {
	Hash         string `json:"hash"`
	Text         string `json:"text"`
	Timestamp    int64  `json:"timestamp_ms,omitempty"`
	TimestampISO string `json:"timestamp_iso,omitempty"`
	Replies      int    `json:"replies,omitempty"`
	Recasts      int    `json:"recasts,omitempty"`
	Likes        int    `json:"likes,omitempty"`
	URL          string `json:"url,omitempty"`
}

// FarcasterUserOutput is the response.
type FarcasterUserOutput struct {
	FID                int                      `json:"fid"`
	Username           string                   `json:"username"`
	DisplayName        string                   `json:"display_name,omitempty"`
	Bio                string                   `json:"bio,omitempty"`
	Location           string                   `json:"location,omitempty"`
	ProfileImageURL    string                   `json:"profile_image_url,omitempty"`
	FollowerCount      int                      `json:"follower_count,omitempty"`
	FollowingCount     int                      `json:"following_count,omitempty"`
	IsActive           bool                     `json:"is_active,omitempty"`
	HasPowerBadge      bool                     `json:"has_power_badge,omitempty"`
	IsEarlyWalletAdopter bool                   `json:"is_early_wallet_adopter,omitempty"`
	AccountLevel       string                   `json:"account_level,omitempty"`
	Verifications      []FarcasterVerification  `json:"verifications,omitempty"`
	EthereumAddresses  []string                 `json:"ethereum_addresses,omitempty"`
	SolanaAddresses    []string                 `json:"solana_addresses,omitempty"`
	RecentCasts        []FarcasterCast          `json:"recent_casts,omitempty"`
	CastCount          int                      `json:"cast_count,omitempty"`
	ProfileURL         string                   `json:"profile_url,omitempty"`
	HighlightFindings  []string                 `json:"highlight_findings"`
	Source             string                   `json:"source"`
	TookMs             int64                    `json:"tookMs"`
	Note               string                   `json:"note,omitempty"`
}

type fcUserRaw struct {
	Result struct {
		User struct {
			FID         int    `json:"fid"`
			Username    string `json:"username"`
			DisplayName string `json:"displayName"`
			Pfp         struct {
				URL string `json:"url"`
			} `json:"pfp"`
			Profile struct {
				Bio struct {
					Text string `json:"text"`
				} `json:"bio"`
				Location struct {
					Description string `json:"description"`
				} `json:"location"`
				EarlyWalletAdopter bool   `json:"earlyWalletAdopter"`
				AccountLevel       string `json:"accountLevel"`
			} `json:"profile"`
			FollowerCount   int  `json:"followerCount"`
			FollowingCount  int  `json:"followingCount"`
			ActiveStatus    string `json:"activeStatus"`
			PowerBadge      bool `json:"powerBadge"`
		} `json:"user"`
	} `json:"result"`
}

type fcVerificationsRaw struct {
	Result struct {
		Verifications []struct {
			Protocol string `json:"protocol"`
			Address  string `json:"address"`
			Chain    string `json:"chain"`
		} `json:"verifications"`
	} `json:"result"`
}

type fcCastsRaw struct {
	Result struct {
		Casts []struct {
			Hash      string `json:"hash"`
			Text      string `json:"text"`
			Timestamp int64  `json:"timestamp"`
			Replies struct{ Count int `json:"count"` } `json:"replies"`
			Recasts struct{ Count int `json:"count"` } `json:"recasts"`
			Reactions struct{ Count int `json:"count"` } `json:"reactions"`
		} `json:"casts"`
	} `json:"result"`
}

// FarcasterUserLookup queries Warpcast's public Farcaster API for user
// identity ER. No auth required.
//
// Why this matters for ER:
//   - Farcaster is the open Web3 social protocol — fundamentally different
//     from Mastodon/Bluesky because user IDs (FIDs) are anchored to a
//     blockchain registry contract, making them globally unique and
//     immutable.
//   - VERIFIED ADDRESSES are the killer ER feature: users sign messages
//     with their wallets to prove ownership, then link those wallets to
//     their FID. This means a Farcaster profile's verified wallet list is
//     a CRYPTOGRAPHICALLY-SIGNED hard link between Web3 identity (Ethereum
//     and/or Solana) and Farcaster social identity.
//   - Pairs naturally with ens_resolve (ENS names map to Eth addresses),
//     nostr_user_lookup (alt Web3 social), and onchain_tx_analysis
//     (BigQuery Eth tx history) — together they form a complete Web3
//     identity-resolution stack.
//   - Cast history reveals topical interests, posting cadence, social
//     network depth.
//
// Two input modes (auto-detected):
//   - Numeric FID: looked up directly
//   - Username: resolved via /v2/user-by-username
func FarcasterUserLookup(ctx context.Context, input map[string]any) (*FarcasterUserOutput, error) {
	username, _ := input["username"].(string)
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")

	fid := 0
	if v, ok := input["fid"].(float64); ok {
		fid = int(v)
	}

	if username == "" && fid <= 0 {
		return nil, fmt.Errorf("input.username or input.fid required (e.g. username='dwr' or fid=3)")
	}

	includeCasts := true
	if v, ok := input["include_casts"].(bool); ok {
		includeCasts = v
	}
	includeVerifications := true
	if v, ok := input["include_verifications"].(bool); ok {
		includeVerifications = v
	}
	castLimit := 10
	if v, ok := input["cast_limit"].(float64); ok && int(v) > 0 && int(v) <= 50 {
		castLimit = int(v)
	}

	out := &FarcasterUserOutput{
		Source: "api.warpcast.com (Farcaster public)",
	}
	start := time.Now()
	client := &http.Client{Timeout: 30 * time.Second}

	// Resolve to FID if needed
	if fid <= 0 {
		// Username might already be numeric
		if n, err := strconv.Atoi(username); err == nil && n > 0 {
			fid = n
		} else {
			user, err := fcUserByUsername(ctx, client, username)
			if err != nil {
				return nil, err
			}
			if user == nil {
				out.Note = fmt.Sprintf("no Farcaster user with username '%s'", username)
				out.HighlightFindings = []string{out.Note}
				out.TookMs = time.Since(start).Milliseconds()
				return out, nil
			}
			populateFromUserRaw(out, user)
			fid = out.FID
		}
	}

	// If we still don't have profile (came in via fid), fetch by FID
	if out.FID == 0 {
		out.FID = fid
		// Fetch by FID. Warpcast doesn't expose a clean fid→user endpoint with
		// the same shape, but we can use user-by-fid:
		user, err := fcUserByFID(ctx, client, fid)
		if err == nil && user != nil {
			populateFromUserRaw(out, user)
		}
	}

	out.ProfileURL = fmt.Sprintf("https://warpcast.com/~/profiles/%d", fid)
	if out.Username != "" {
		out.ProfileURL = "https://warpcast.com/" + out.Username
	}

	// Verifications
	if includeVerifications && fid > 0 {
		verifs, err := fcVerifications(ctx, client, fid)
		if err == nil && verifs != nil {
			out.Verifications = verifs
			for _, v := range verifs {
				switch strings.ToLower(v.Protocol) {
				case "ethereum":
					out.EthereumAddresses = append(out.EthereumAddresses, v.Address)
				case "solana":
					out.SolanaAddresses = append(out.SolanaAddresses, v.Address)
				}
			}
		}
	}

	// Casts
	if includeCasts && fid > 0 {
		casts, err := fcCasts(ctx, client, fid, castLimit)
		if err == nil && casts != nil {
			out.RecentCasts = casts
			out.CastCount = len(casts)
		}
	}

	out.HighlightFindings = buildFarcasterHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func fcUserByUsername(ctx context.Context, client *http.Client, username string) (*fcUserRaw, error) {
	endpoint := "https://api.warpcast.com/v2/user-by-username?username=" + url.QueryEscape(username)
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("warpcast user lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode == 400 {
		// Warpcast returns 400 for invalid username patterns AND 404 for valid-looking but missing.
		// Treat both as "not found" rather than propagating the API error.
		return nil, nil
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("warpcast %d: %s", resp.StatusCode, string(body))
	}
	var raw fcUserRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.Result.User.FID == 0 {
		return nil, nil
	}
	return &raw, nil
}

func fcUserByFID(ctx context.Context, client *http.Client, fid int) (*fcUserRaw, error) {
	// Use the v2/user endpoint
	endpoint := fmt.Sprintf("https://api.warpcast.com/v2/user?fid=%d", fid)
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil
	}
	var raw fcUserRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

func fcVerifications(ctx context.Context, client *http.Client, fid int) ([]FarcasterVerification, error) {
	endpoint := fmt.Sprintf("https://api.warpcast.com/v2/verifications?fid=%d", fid)
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil
	}
	var raw fcVerificationsRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := []FarcasterVerification{}
	seen := map[string]bool{}
	for _, v := range raw.Result.Verifications {
		key := v.Protocol + "|" + v.Address
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, FarcasterVerification{Protocol: v.Protocol, Address: v.Address, Chain: v.Chain})
	}
	return out, nil
}

func fcCasts(ctx context.Context, client *http.Client, fid, limit int) ([]FarcasterCast, error) {
	endpoint := fmt.Sprintf("https://api.warpcast.com/v2/casts?fid=%d&limit=%d", fid, limit)
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil
	}
	var raw fcCastsRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := []FarcasterCast{}
	for _, c := range raw.Result.Casts {
		fc := FarcasterCast{
			Hash:      c.Hash,
			Text:      hfTruncate(c.Text, 400),
			Timestamp: c.Timestamp,
			Replies:   c.Replies.Count,
			Recasts:   c.Recasts.Count,
			Likes:     c.Reactions.Count,
		}
		if c.Timestamp > 0 {
			ts := time.Unix(c.Timestamp/1000, 0).UTC()
			fc.TimestampISO = ts.Format(time.RFC3339)
		}
		out = append(out, fc)
	}
	// most recent first (Farcaster API typically already returns in reverse chrono)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp > out[j].Timestamp })
	return out, nil
}

func populateFromUserRaw(out *FarcasterUserOutput, raw *fcUserRaw) {
	u := raw.Result.User
	out.FID = u.FID
	out.Username = u.Username
	out.DisplayName = u.DisplayName
	out.Bio = u.Profile.Bio.Text
	out.Location = u.Profile.Location.Description
	out.ProfileImageURL = u.Pfp.URL
	out.FollowerCount = u.FollowerCount
	out.FollowingCount = u.FollowingCount
	out.HasPowerBadge = u.PowerBadge
	out.IsEarlyWalletAdopter = u.Profile.EarlyWalletAdopter
	out.AccountLevel = u.Profile.AccountLevel
	out.IsActive = u.ActiveStatus == "active"
}

func buildFarcasterHighlights(o *FarcasterUserOutput) []string {
	hi := []string{}
	if o.FID == 0 {
		return hi
	}
	hi = append(hi, fmt.Sprintf("✓ FID=%d  @%s  %q  followers=%d, following=%d",
		o.FID, o.Username, o.DisplayName, o.FollowerCount, o.FollowingCount))
	if o.Bio != "" {
		hi = append(hi, "📝 bio: "+o.Bio)
	}
	if o.Location != "" {
		hi = append(hi, "📍 location: "+o.Location)
	}
	if o.AccountLevel != "" {
		hi = append(hi, "account_level: "+o.AccountLevel)
	}
	if o.HasPowerBadge {
		hi = append(hi, "⚡ POWER BADGE — Warpcast distinguishes power users (high-quality contributors)")
	}
	if o.IsEarlyWalletAdopter {
		hi = append(hi, "🚀 early wallet adopter (Web3-native signal)")
	}
	if len(o.Verifications) > 0 {
		hi = append(hi, fmt.Sprintf("🔐 %d cryptographically-verified wallet(s) linked to FID%d:", len(o.Verifications), o.FID))
		for _, v := range o.Verifications {
			hi = append(hi, fmt.Sprintf("  %s: %s", v.Protocol, v.Address))
		}
	}
	if len(o.EthereumAddresses) > 0 {
		hi = append(hi, fmt.Sprintf("⚡ %d Ethereum address(es) — pivot via ens_resolve / onchain_tx_analysis", len(o.EthereumAddresses)))
	}
	if len(o.SolanaAddresses) > 0 {
		hi = append(hi, fmt.Sprintf("⚡ %d Solana address(es) — cross-chain identity signal", len(o.SolanaAddresses)))
	}
	if len(o.RecentCasts) > 0 {
		hi = append(hi, fmt.Sprintf("💬 %d recent cast(s) recovered", len(o.RecentCasts)))
		// show 1 sample
		c := o.RecentCasts[0]
		hi = append(hi, fmt.Sprintf("most recent cast (%s): %s", c.TimestampISO[:10], c.Text))
	}
	hi = append(hi, "🔗 profile: "+o.ProfileURL)
	return hi
}
