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

type ENSIdentity struct {
	Identity      string            `json:"identity"`        // e.g. vitalik.eth
	Platform      string            `json:"platform"`        // ens | lens | farcaster | dotbit | ...
	DisplayName   string            `json:"display_name,omitempty"`
	Address       string            `json:"address"`
	Avatar        string            `json:"avatar,omitempty"`
	Description   string            `json:"description,omitempty"`
	CreatedAt     string            `json:"created_at,omitempty"`
	Email         string            `json:"email,omitempty"`
	Location      string            `json:"location,omitempty"`
	ContentHash   string            `json:"content_hash,omitempty"`
	Header        string            `json:"header,omitempty"`
	Links         map[string]string `json:"links,omitempty"` // platform → handle
	Followers     int               `json:"followers,omitempty"`
	Following     int               `json:"following,omitempty"`
}

type ENSResolveOutput struct {
	Query             string         `json:"query"`
	IsAddress         bool           `json:"is_address"`
	IsENSName         bool           `json:"is_ens_name"`
	PrimaryAddress    string         `json:"primary_address,omitempty"`
	Identities        []ENSIdentity  `json:"identities"` // all identities tied to the address
	IdentityCount     int            `json:"identity_count"`
	UniquePlatforms   []string       `json:"unique_platforms"`
	SocialHandles     map[string]string `json:"social_handles,omitempty"` // aggregated: twitter, github, website
	HighlightFindings []string       `json:"highlight_findings"`
	Source            string         `json:"source"`
	TookMs            int64          `json:"tookMs"`
	Note              string         `json:"note,omitempty"`
}

// ENSResolve performs cross-platform Web3 identity resolution.
// Given an ENS name (e.g. "vitalik.eth") OR an Ethereum address (0x...),
// returns:
//   - All identities tied to the address (ENS, Lens, Farcaster, dotbit, etc.)
//   - Aggregated social handles (Twitter, GitHub, website, email)
//   - Bio/avatar/header
//   - Multiple ENS names if owner controls multiple
//
// Uses web3.bio meta-resolver (free, no key). Falls back to ensdata.net.
//
// Use case: Web3 identity correlation. Crypto-native social/identity is
// fragmented across ENS / Lens / Farcaster / dotbit — this tool unifies them.
// For ER, getting from a wallet address to a Twitter handle is the Web3
// equivalent of `tracker_correlate`'s operator-binding.
func ENSResolve(ctx context.Context, input map[string]any) (*ENSResolveOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (ENS name like 'vitalik.eth' or 0x address)")
	}

	out := &ENSResolveOutput{
		Query:           q,
		Source:          "web3.bio + ensdata.net fallback",
		SocialHandles:   map[string]string{},
		IsAddress:       strings.HasPrefix(strings.ToLower(q), "0x") && len(q) == 42,
		IsENSName:       strings.HasSuffix(strings.ToLower(q), ".eth") || strings.Contains(q, "."),
	}
	start := time.Now()

	// Primary: web3.bio (returns array of identities for an address)
	if err := ensResolveViaWeb3Bio(ctx, q, out); err == nil && len(out.Identities) > 0 {
		// success
	} else {
		// Fallback to ensdata.net (single-identity but reliable)
		if err2 := ensResolveViaEnsdata(ctx, q, out); err2 != nil {
			if err != nil {
				return nil, fmt.Errorf("both resolvers failed — web3.bio: %v ; ensdata.net: %v", err, err2)
			}
			return nil, err2
		}
	}

	// Aggregate
	platSet := map[string]bool{}
	for _, id := range out.Identities {
		platSet[id.Platform] = true
		if out.PrimaryAddress == "" && id.Address != "" {
			out.PrimaryAddress = id.Address
		}
		for k, v := range id.Links {
			if v != "" && out.SocialHandles[k] == "" {
				out.SocialHandles[k] = v
			}
		}
		if id.Email != "" && out.SocialHandles["email"] == "" {
			out.SocialHandles["email"] = id.Email
		}
	}
	for p := range platSet {
		out.UniquePlatforms = append(out.UniquePlatforms, p)
	}
	out.IdentityCount = len(out.Identities)

	// Highlights
	highlights := []string{}
	if out.PrimaryAddress != "" {
		highlights = append(highlights, fmt.Sprintf("primary address: %s", out.PrimaryAddress))
	}
	if len(out.Identities) > 1 {
		names := []string{}
		for i, id := range out.Identities {
			if i >= 5 {
				break
			}
			names = append(names, id.Identity)
		}
		highlights = append(highlights, fmt.Sprintf("%d identities tied to this address: %s%s",
			len(out.Identities), strings.Join(names, ", "),
			func() string { if len(out.Identities) > 5 { return "..." }; return "" }()))
	}
	if len(out.SocialHandles) > 0 {
		hs := []string{}
		for k, v := range out.SocialHandles {
			hs = append(hs, fmt.Sprintf("%s=%s", k, v))
		}
		highlights = append(highlights, "social handles: "+strings.Join(hs, ", "))
	}
	if len(out.UniquePlatforms) > 1 {
		highlights = append(highlights, "active on platforms: "+strings.Join(out.UniquePlatforms, ", "))
	}
	if len(out.Identities) == 0 {
		highlights = append(highlights, "No identities found — wallet may be new, ENS unset, or tx-only")
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func ensResolveViaWeb3Bio(ctx context.Context, q string, out *ENSResolveOutput) error {
	endpoint := "https://api.web3.bio/profile/" + url.PathEscape(q)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/ens-resolve")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("web3.bio status %d", resp.StatusCode)
	}

	var raw []struct {
		Identity    string `json:"identity"`
		Platform    string `json:"platform"`
		DisplayName string `json:"displayName"`
		Address     string `json:"address"`
		Avatar      string `json:"avatar"`
		Description string `json:"description"`
		CreatedAt   string `json:"createdAt"`
		Email       string `json:"email"`
		Location    string `json:"location"`
		ContentHash string `json:"contenthash"`
		Header      string `json:"header"`
		Links       map[string]struct {
			Link   string `json:"link"`
			Handle string `json:"handle"`
		} `json:"links"`
		Social struct {
			Follower  int `json:"follower"`
			Following int `json:"following"`
		} `json:"social"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("web3.bio parse: %w", err)
	}
	for _, r := range raw {
		id := ENSIdentity{
			Identity: r.Identity, Platform: r.Platform, DisplayName: r.DisplayName,
			Address: r.Address, Avatar: r.Avatar, Description: r.Description,
			CreatedAt: r.CreatedAt, Email: r.Email, Location: r.Location,
			ContentHash: r.ContentHash, Header: r.Header,
			Followers: r.Social.Follower, Following: r.Social.Following,
			Links: map[string]string{},
		}
		for k, v := range r.Links {
			if v.Handle != "" {
				id.Links[k] = v.Handle
			} else if v.Link != "" {
				id.Links[k] = v.Link
			}
		}
		out.Identities = append(out.Identities, id)
	}
	return nil
}

func ensResolveViaEnsdata(ctx context.Context, q string, out *ENSResolveOutput) error {
	endpoint := "https://api.ensdata.net/" + url.PathEscape(q)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/ens-resolve")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("ensdata.net status %d", resp.StatusCode)
	}

	var raw struct {
		Address     string `json:"address"`
		Avatar      string `json:"avatar"`
		ContentHash string `json:"contentHash"`
		Description string `json:"description"`
		ENS         string `json:"ens"`
		ENSPrimary  string `json:"ens_primary"`
		Github      string `json:"github"`
		Header      string `json:"header"`
		Twitter     string `json:"twitter"`
		URL         string `json:"url"`
		Email       string `json:"email"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("ensdata.net parse: %w", err)
	}
	links := map[string]string{}
	if raw.Twitter != "" {
		links["twitter"] = raw.Twitter
	}
	if raw.Github != "" {
		links["github"] = raw.Github
	}
	if raw.URL != "" {
		links["website"] = raw.URL
	}
	out.Identities = append(out.Identities, ENSIdentity{
		Identity:    raw.ENS,
		Platform:    "ens",
		DisplayName: raw.ENSPrimary,
		Address:     raw.Address,
		Avatar:      raw.Avatar,
		Description: raw.Description,
		ContentHash: raw.ContentHash,
		Header:      raw.Header,
		Email:       raw.Email,
		Links:       links,
	})
	return nil
}
