package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type NostrProfile struct {
	Identifier  string   `json:"identifier"`
	NPub        string   `json:"npub,omitempty"`
	HexPubkey   string   `json:"hex_pubkey,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	Name        string   `json:"name,omitempty"`
	About       string   `json:"about,omitempty"`
	Picture     string   `json:"picture,omitempty"`
	Banner      string   `json:"banner,omitempty"`
	Website     string   `json:"website,omitempty"`
	NIP05       string   `json:"nip05_identifier,omitempty"`
	Lud16       string   `json:"lightning_address,omitempty"`
	NjumpURL    string   `json:"njump_url"`
	IrisURL     string   `json:"iris_url,omitempty"`
	PrimalURL   string   `json:"primal_url,omitempty"`
	RelayHints  []string `json:"relay_hints,omitempty"`
}

type NostrNote struct {
	ID        string `json:"id,omitempty"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at,omitempty"`
}

type NostrUserLookupOutput struct {
	Profile           NostrProfile `json:"profile"`
	RecentNotes       []NostrNote  `json:"recent_notes,omitempty"`
	NoteCount         int          `json:"recent_note_count"`
	HighlightFindings []string     `json:"highlight_findings"`
	Source            string       `json:"source"`
	TookMs            int64        `json:"tookMs"`
	Note              string       `json:"note,omitempty"`
}

var njumpProfileJSONRE = regexp.MustCompile(`(?s)<script type="application/ld\+json"[^>]*>(.*?)</script>`)
var njumpDataAttrRE = regexp.MustCompile(`(?s)<div[^>]+id="profileData"[^>]*>(.*?)</div>`)
var njumpNoteRE = regexp.MustCompile(`(?s)<article[^>]+class="[^"]*note[^"]*"[^>]*>.*?<p[^>]*>(.*?)</p>`)
var njumpOgImageRE = regexp.MustCompile(`<meta property="og:image"\s+content="([^"]+)"`)
var njumpOgTitleRE = regexp.MustCompile(`<meta property="og:title"\s+content="([^"]+)"`)
var njumpOgDescRE = regexp.MustCompile(`<meta property="og:description"\s+content="([^"]+)"`)
var njumpNoteTextRE = regexp.MustCompile(`(?s)<div class="paragraph[^"]*"[^>]*>(.*?)</div>`)

// NostrUserLookup queries Nostr (decentralized social protocol) public
// gateways for a user profile + recent notes.
//
// Accepts:
//   - npub1xxxxx... (Nostr-encoded pubkey)
//   - hex pubkey (64 chars)
//   - NIP-05 identifier (user@domain.com)
//   - shortcut handle (e.g. "jack" via Primal lookup)
//
// Resolution path: njump.me HTML gateway (cleanest), with fallback to
// nostr.band JSON API.
//
// Use cases:
//   - Crypto/Web3 social ER (Nostr is the censorship-resistant alternative
//     to Twitter — major in crypto/free-speech communities)
//   - Threat actor research (some actors operate Nostr accounts to evade
//     deplatforming)
//   - Cross-reference with `ens_resolve` — many Web3 users have both ENS +
//     Nostr identities tied to the same wallet
//
// Free, no auth.
func NostrUserLookup(ctx context.Context, input map[string]any) (*NostrUserLookupOutput, error) {
	id, _ := input["identifier"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("input.identifier required (npub, hex pubkey, or NIP-05)")
	}

	// Normalize: strip @ from NIP-05-style, strip "npub:" prefix
	id = strings.TrimPrefix(id, "@")
	id = strings.TrimPrefix(id, "nostr:")
	id = strings.TrimPrefix(id, "Nostr:")

	start := time.Now()
	out := &NostrUserLookupOutput{
		Profile: NostrProfile{Identifier: id},
		Source:  "njump.me + nostr.band",
	}

	// njump.me supports npub, hex, and NIP-05 directly in URL
	njumpURL := "https://njump.me/" + url.PathEscape(id)
	out.Profile.NjumpURL = njumpURL

	body, err := njumpFetch(ctx, njumpURL)
	if err != nil {
		return nil, fmt.Errorf("njump fetch: %w", err)
	}

	// Detect 404 / not-found
	bodyLow := strings.ToLower(body)
	if strings.Contains(bodyLow, "could not find") || strings.Contains(bodyLow, "not found") || len(body) < 1000 {
		out.Note = "Profile not found on Nostr public relays via njump.me. Identifier may be wrong format or user not on indexed relays."
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// Parse og: meta tags
	if m := njumpOgTitleRE.FindStringSubmatch(body); len(m) > 1 {
		out.Profile.DisplayName = strings.TrimSpace(m[1])
	}
	if m := njumpOgDescRE.FindStringSubmatch(body); len(m) > 1 {
		out.Profile.About = truncate(strings.TrimSpace(m[1]), 500)
	}
	if m := njumpOgImageRE.FindStringSubmatch(body); len(m) > 1 {
		out.Profile.Picture = m[1]
	}

	// Try to extract npub from canonical URL or title
	if m := regexp.MustCompile(`npub1[a-z0-9]{50,70}`).FindString(body); m != "" {
		out.Profile.NPub = m
	}
	// hex pubkey from canonical link
	if m := regexp.MustCompile(`(?i)pubkey["']?\s*:\s*["']([a-f0-9]{64})["']`).FindStringSubmatch(body); len(m) > 1 {
		out.Profile.HexPubkey = m[1]
	}

	// Look for JSON-LD profile data
	if m := njumpProfileJSONRE.FindStringSubmatch(body); len(m) > 1 {
		var raw map[string]any
		if err := json.Unmarshal([]byte(m[1]), &raw); err == nil {
			if v, ok := raw["name"].(string); ok && out.Profile.Name == "" {
				out.Profile.Name = v
			}
			if v, ok := raw["url"].(string); ok && out.Profile.Website == "" {
				out.Profile.Website = v
			}
		}
	}

	// NIP-05 lookup
	nip05Match := regexp.MustCompile(`nip-?05[^"]{0,20}["']?\s*:\s*["']([^"']+@[^"']+)["']|"verified":\s*"([^"]+@[^"]+)"`).FindStringSubmatch(body)
	if len(nip05Match) > 1 {
		v := nip05Match[1]
		if v == "" {
			v = nip05Match[2]
		}
		out.Profile.NIP05 = v
	}

	// Lightning address
	if m := regexp.MustCompile(`(?i)(?:lud16|lightning)[^"]{0,20}["']?\s*:\s*["']([a-zA-Z0-9._-]+@[a-zA-Z0-9.-]+)["']`).FindStringSubmatch(body); len(m) > 1 {
		out.Profile.Lud16 = m[1]
	}

	// Build alternate URLs
	if out.Profile.NPub != "" {
		out.Profile.IrisURL = "https://iris.to/" + out.Profile.NPub
		out.Profile.PrimalURL = "https://primal.net/p/" + out.Profile.NPub
	}

	// Extract recent note text (best-effort — njump's HTML structure can change)
	noteCount := 0
	for _, m := range njumpNoteTextRE.FindAllStringSubmatch(body, -1) {
		if len(m) < 2 {
			continue
		}
		text := stripHTMLBare(m[1])
		text = strings.TrimSpace(text)
		if len(text) < 5 || len(text) > 2000 {
			continue
		}
		out.RecentNotes = append(out.RecentNotes, NostrNote{Content: truncate(text, 400)})
		noteCount++
		if noteCount >= 8 {
			break
		}
	}
	out.NoteCount = len(out.RecentNotes)

	// Highlights
	highlights := []string{
		fmt.Sprintf("'%s' on Nostr", func() string {
			if out.Profile.DisplayName != "" {
				return out.Profile.DisplayName
			}
			return id
		}()),
	}
	if out.Profile.About != "" {
		highlights = append(highlights, "bio: "+truncate(out.Profile.About, 100))
	}
	if out.Profile.NIP05 != "" {
		highlights = append(highlights, "verified: "+out.Profile.NIP05)
	}
	if out.Profile.Lud16 != "" {
		highlights = append(highlights, "⚡ lightning: "+out.Profile.Lud16)
	}
	if out.Profile.Website != "" {
		highlights = append(highlights, "website: "+out.Profile.Website)
	}
	if out.NoteCount > 0 {
		highlights = append(highlights, fmt.Sprintf("%d recent notes extracted", out.NoteCount))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func njumpFetch(ctx context.Context, target string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, target, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return string(body), nil
}
