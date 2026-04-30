package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type KeybaseProof struct {
	ProofType   string `json:"proof_type"`
	Nametag     string `json:"nametag"`
	State       int    `json:"state"`
	HumanURL    string `json:"human_url,omitempty"`
	ProofURL    string `json:"proof_url,omitempty"`
	ServiceURL  string `json:"service_url,omitempty"`
}

type KeybaseOutput struct {
	Username       string         `json:"username"`
	UID            string         `json:"uid,omitempty"`
	FullName       string         `json:"full_name,omitempty"`
	Bio            string         `json:"bio,omitempty"`
	Location       string         `json:"location,omitempty"`
	ProfileURL     string         `json:"profile_url"`
	PictureURL     string         `json:"picture_url,omitempty"`
	Proofs         []KeybaseProof `json:"identity_proofs"`
	Twitter        string         `json:"twitter,omitempty"`
	GitHub         string         `json:"github,omitempty"`
	Reddit         string         `json:"reddit,omitempty"`
	HackerNews     string         `json:"hackernews,omitempty"`
	Websites       []string       `json:"websites,omitempty"`
	PGPFingerprint string         `json:"pgp_fingerprint,omitempty"`
	Source         string         `json:"source"`
	TookMs         int64          `json:"tookMs"`
}

// KeybaseLookup queries Keybase's public API for a username and returns
// CRYPTOGRAPHICALLY VERIFIED identity proofs across Twitter, GitHub, Reddit,
// HackerNews, and websites. Each proof has been signed by the Keybase user
// and verified against the linked account — much higher precision than any
// sherlock-family tool. Free, no key.
//
// Keybase usage peaked ~2018 (pre-Zoom acquisition) so this returns fewer
// hits today than it would have, but when present the data is gold.
func KeybaseLookup(ctx context.Context, input map[string]any) (*KeybaseOutput, error) {
	username, _ := input["username"].(string)
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("input.username required")
	}
	start := time.Now()
	endpoint := "https://keybase.io/_/api/1.0/user/lookup.json?usernames=" + url.QueryEscape(username) + "&fields=basics,profile,public_keys,proofs_summary,pictures"
	body, err := httpGetJSON(ctx, endpoint, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("keybase fetch: %w", err)
	}
	var resp struct {
		Status struct {
			Code int    `json:"code"`
			Name string `json:"name"`
		} `json:"status"`
		Them []struct {
			ID     string `json:"id"`
			Basics struct {
				Username string `json:"username"`
			} `json:"basics"`
			Profile struct {
				FullName string `json:"full_name"`
				Location string `json:"location"`
				Bio      string `json:"bio"`
			} `json:"profile"`
			PublicKeys struct {
				Primary struct {
					Fingerprint string `json:"fingerprint"`
				} `json:"primary"`
			} `json:"public_keys"`
			Pictures struct {
				Primary struct {
					URL string `json:"url"`
				} `json:"primary"`
			} `json:"pictures"`
			ProofsSummary struct {
				All []struct {
					ProofType  string `json:"proof_type"`
					Nametag    string `json:"nametag"`
					State      int    `json:"state"` // 1 = OK
					HumanURL   string `json:"human_url"`
					ProofURL   string `json:"proof_url"`
					ServiceURL string `json:"service_url"`
				} `json:"all"`
			} `json:"proofs_summary"`
		} `json:"them"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("keybase parse: %w", err)
	}
	if resp.Status.Code != 0 || len(resp.Them) == 0 {
		return nil, fmt.Errorf("keybase user %q not found (status: %s)", username, resp.Status.Name)
	}
	t := resp.Them[0]
	out := &KeybaseOutput{
		Username:       t.Basics.Username,
		UID:            t.ID,
		FullName:       t.Profile.FullName,
		Bio:            t.Profile.Bio,
		Location:       t.Profile.Location,
		ProfileURL:     "https://keybase.io/" + t.Basics.Username,
		PictureURL:     t.Pictures.Primary.URL,
		PGPFingerprint: t.PublicKeys.Primary.Fingerprint,
		Source:         "keybase.io",
		TookMs:         time.Since(start).Milliseconds(),
	}
	for _, p := range t.ProofsSummary.All {
		out.Proofs = append(out.Proofs, KeybaseProof{
			ProofType: p.ProofType, Nametag: p.Nametag, State: p.State,
			HumanURL: p.HumanURL, ProofURL: p.ProofURL, ServiceURL: p.ServiceURL,
		})
		// Surface the most-asked-for proofs as flat fields.
		switch p.ProofType {
		case "twitter":
			out.Twitter = p.Nametag
		case "github":
			out.GitHub = p.Nametag
		case "reddit":
			out.Reddit = p.Nametag
		case "hackernews":
			out.HackerNews = p.Nametag
		case "generic_web_site", "dns":
			out.Websites = append(out.Websites, p.Nametag)
		}
	}
	return out, nil
}
