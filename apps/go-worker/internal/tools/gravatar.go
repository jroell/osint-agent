package tools

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type GravatarAccount struct {
	Domain    string `json:"domain"`
	Username  string `json:"username"`
	URL       string `json:"url,omitempty"`
	Verified  bool   `json:"verified,omitempty"`
}

type GravatarOutput struct {
	Email           string            `json:"email"`
	Hash            string            `json:"sha256_hash"`
	MD5             string            `json:"md5_hash"`
	HasGravatar     bool              `json:"has_gravatar"`
	DisplayName     string            `json:"display_name,omitempty"`
	PreferredUsername string          `json:"preferred_username,omitempty"`
	AboutMe         string            `json:"about_me,omitempty"`
	Location        string            `json:"location,omitempty"`
	JobTitle        string            `json:"job_title,omitempty"`
	Pronouns        string            `json:"pronouns,omitempty"`
	ProfileURL      string            `json:"profile_url,omitempty"`
	AvatarURL       string            `json:"avatar_url,omitempty"`
	VerifiedAccounts []GravatarAccount `json:"verified_accounts,omitempty"`
	Source          string            `json:"source"`
	TookMs          int64             `json:"tookMs"`
}

// GravatarLookup resolves an email to a Gravatar profile if one exists.
// Free, no key, very stable. Excellent OSINT pivot — Gravatar profiles
// frequently link verified accounts on Twitter, GitHub, LinkedIn, etc.
func GravatarLookup(ctx context.Context, input map[string]any) (*GravatarOutput, error) {
	email, _ := input["email"].(string)
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || !strings.Contains(email, "@") {
		return nil, errors.New("input.email required (a valid email address)")
	}
	start := time.Now()
	md5Sum := md5.Sum([]byte(email))
	sha256Sum := sha256.Sum256([]byte(email))
	md5Hex := hex.EncodeToString(md5Sum[:])
	shaHex := hex.EncodeToString(sha256Sum[:])

	out := &GravatarOutput{
		Email:     email,
		Hash:      shaHex,
		MD5:       md5Hex,
		AvatarURL: "https://www.gravatar.com/avatar/" + shaHex + "?d=404",
		Source:    "gravatar.com",
	}

	// Gravatar v3 API uses SHA-256 hash. Try the modern endpoint first;
	// fall back to legacy MD5 endpoint if the v3 path 404s (older accounts).
	body, status, err := httpGetWithStatus(ctx, "https://api.gravatar.com/v3/profiles/"+shaHex, 8*time.Second)
	if err == nil && status == 200 {
		var p struct {
			DisplayName       string `json:"display_name"`
			PreferredUsername string `json:"preferred_username"`
			AboutMe           string `json:"about_me"`
			Location          string `json:"location"`
			JobTitle          string `json:"job_title"`
			Pronouns          string `json:"pronouns"`
			ProfileURL        string `json:"profile_url"`
			AvatarURL         string `json:"avatar_url"`
			VerifiedAccounts  []struct {
				Service  string `json:"service_label"`
				URL      string `json:"url"`
				Username string `json:"service_account"`
			} `json:"verified_accounts"`
		}
		if err := json.Unmarshal(body, &p); err == nil {
			out.HasGravatar = true
			out.DisplayName = p.DisplayName
			out.PreferredUsername = p.PreferredUsername
			out.AboutMe = p.AboutMe
			out.Location = p.Location
			out.JobTitle = p.JobTitle
			out.Pronouns = p.Pronouns
			out.ProfileURL = p.ProfileURL
			if p.AvatarURL != "" {
				out.AvatarURL = p.AvatarURL
			}
			for _, va := range p.VerifiedAccounts {
				out.VerifiedAccounts = append(out.VerifiedAccounts, GravatarAccount{
					Domain: va.Service, URL: va.URL, Username: va.Username, Verified: true,
				})
			}
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
	}

	// Fallback: try legacy MD5 .json endpoint (still serves older accounts).
	legacyBody, legacyStatus, _ := httpGetWithStatus(ctx, "https://www.gravatar.com/"+md5Hex+".json", 8*time.Second)
	if legacyStatus == 200 {
		var legacy struct {
			Entry []struct {
				DisplayName  string `json:"displayName"`
				PreferredUsername string `json:"preferredUsername"`
				AboutMe      string `json:"aboutMe"`
				Name         struct {
					FormattedName string `json:"formatted"`
				} `json:"name"`
				ProfileURL string `json:"profileUrl"`
				ThumbnailURL string `json:"thumbnailUrl"`
				Accounts   []struct {
					Domain   string `json:"domain"`
					Username string `json:"username"`
					URL      string `json:"url"`
					Verified string `json:"verified"`
				} `json:"accounts"`
			} `json:"entry"`
		}
		if err := json.Unmarshal(legacyBody, &legacy); err == nil && len(legacy.Entry) > 0 {
			e := legacy.Entry[0]
			out.HasGravatar = true
			out.DisplayName = firstNonEmpty(e.DisplayName, e.Name.FormattedName)
			out.PreferredUsername = e.PreferredUsername
			out.AboutMe = e.AboutMe
			out.ProfileURL = e.ProfileURL
			if e.ThumbnailURL != "" {
				out.AvatarURL = e.ThumbnailURL
			}
			for _, a := range e.Accounts {
				out.VerifiedAccounts = append(out.VerifiedAccounts, GravatarAccount{
					Domain: a.Domain, Username: a.Username, URL: a.URL, Verified: a.Verified == "true",
				})
			}
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
	}

	out.HasGravatar = false
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func httpGetWithStatus(ctx context.Context, url string, timeout time.Duration) ([]byte, int, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body := make([]byte, 0, 32<<10)
	buf := make([]byte, 16<<10)
	for {
		n, e := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
			if len(body) > 8<<20 {
				break
			}
		}
		if e != nil {
			break
		}
	}
	return body, resp.StatusCode, nil
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

