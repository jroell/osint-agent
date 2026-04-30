package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// =============================================================================
// twitter_user — REQUIRES X_API_BEARER_TOKEN (X API v2; paid tier $100+/mo)
// =============================================================================

type TwitterUserOutput struct {
	Username        string                 `json:"username"`
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Description     string                 `json:"description,omitempty"`
	Location        string                 `json:"location,omitempty"`
	Verified        bool                   `json:"verified,omitempty"`
	VerifiedType    string                 `json:"verified_type,omitempty"`
	CreatedAt       string                 `json:"created_at,omitempty"`
	URL             string                 `json:"url,omitempty"`
	ProfileImageURL string                 `json:"profile_image_url,omitempty"`
	Metrics         map[string]interface{} `json:"public_metrics,omitempty"`
	Source          string                 `json:"source"`
	TookMs          int64                  `json:"tookMs"`
}

func TwitterUser(ctx context.Context, input map[string]any) (*TwitterUserOutput, error) {
	username, _ := input["username"].(string)
	username = strings.TrimSpace(strings.TrimPrefix(username, "@"))
	if username == "" {
		return nil, errors.New("input.username required (X/Twitter handle, with or without @)")
	}
	bearer := os.Getenv("X_API_BEARER_TOKEN")
	if bearer == "" {
		return nil, errors.New(
			"X_API_BEARER_TOKEN env var required (X API v2 — paid Premium tier $100+/mo, https://developer.x.com/en/portal/products). " +
				"There is no reliable free path: snscrape is dead, Nitter is mostly broken, and X aggressively blocks scrapers.")
	}
	start := time.Now()
	endpoint := fmt.Sprintf(
		"https://api.twitter.com/2/users/by/username/%s?user.fields=created_at,description,location,profile_image_url,public_metrics,url,verified,verified_type",
		url.PathEscape(username))
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("x api: %w", err)
	}
	defer resp.Body.Close()
	body, _ := readAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("x api %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Data struct {
			ID              string                 `json:"id"`
			Name            string                 `json:"name"`
			Username        string                 `json:"username"`
			Description     string                 `json:"description"`
			Location        string                 `json:"location"`
			Verified        bool                   `json:"verified"`
			VerifiedType    string                 `json:"verified_type"`
			CreatedAt       string                 `json:"created_at"`
			URL             string                 `json:"url"`
			ProfileImageURL string                 `json:"profile_image_url"`
			PublicMetrics   map[string]interface{} `json:"public_metrics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("x api parse: %w", err)
	}
	d := parsed.Data
	return &TwitterUserOutput{
		Username: d.Username, ID: d.ID, Name: d.Name, Description: d.Description,
		Location: d.Location, Verified: d.Verified, VerifiedType: d.VerifiedType,
		CreatedAt: d.CreatedAt, URL: d.URL, ProfileImageURL: d.ProfileImageURL,
		Metrics: d.PublicMetrics, Source: "api.twitter.com/2", TookMs: time.Since(start).Milliseconds(),
	}, nil
}

// =============================================================================
// linkedin_proxycurl — REQUIRES PROXYCURL_API_KEY ($49+/mo)
// =============================================================================

type LinkedInOutput struct {
	URL          string                 `json:"url"`
	Slug         string                 `json:"slug,omitempty"`
	FullName     string                 `json:"full_name,omitempty"`
	Headline     string                 `json:"headline,omitempty"`
	Location     string                 `json:"location,omitempty"`
	Country      string                 `json:"country,omitempty"`
	Summary      string                 `json:"summary,omitempty"`
	Connections  int                    `json:"connections,omitempty"`
	Followers    int                    `json:"follower_count,omitempty"`
	OccupationName string               `json:"occupation_name,omitempty"`
	Experiences  []map[string]interface{} `json:"experiences,omitempty"`
	Education    []map[string]interface{} `json:"education,omitempty"`
	Skills       []string               `json:"skills,omitempty"`
	Languages    []string               `json:"languages,omitempty"`
	Source       string                 `json:"source"`
	TookMs       int64                  `json:"tookMs"`
}

// LinkedInProxycurl is now backed by NinjaPear (the successor to Proxycurl —
// the original Proxycurl API was sunset in 2024 and re-launched as NinjaPear
// at https://nubela.co/docs). Honors both NINJAPEAR_API_KEY (preferred) and
// PROXYCURL_API_KEY (legacy alias) for backward compatibility.
func LinkedInProxycurl(ctx context.Context, input map[string]any) (*LinkedInOutput, error) {
	profileURL, _ := input["url"].(string)
	profileURL = strings.TrimSpace(profileURL)
	if profileURL == "" {
		return nil, errors.New("input.url required (full LinkedIn profile URL, e.g. https://www.linkedin.com/in/williamhgates/)")
	}
	if !strings.Contains(profileURL, "linkedin.com/in/") && !strings.Contains(profileURL, "linkedin.com/pub/") {
		return nil, errors.New("input.url must be a public LinkedIn profile URL (linkedin.com/in/<handle>)")
	}
	// Extract the slug — NinjaPear's /employee/profile endpoint accepts ?slug=<>
	// rather than the old ?url= form.
	slug := profileURL
	if i := strings.Index(slug, "/in/"); i >= 0 {
		slug = slug[i+len("/in/"):]
	}
	slug = strings.TrimSuffix(slug, "/")
	slug = strings.SplitN(slug, "?", 2)[0]
	slug = strings.SplitN(slug, "/", 2)[0]

	apiKey := os.Getenv("NINJAPEAR_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("PROXYCURL_API_KEY") // legacy alias — NinjaPear inherited Proxycurl keys
	}
	if apiKey == "" {
		return nil, errors.New("NINJAPEAR_API_KEY (or legacy PROXYCURL_API_KEY) env var required. Proxycurl was sunset in 2024 and replaced by NinjaPear — see https://nubela.co/docs. Existing Proxycurl API keys still work.")
	}
	start := time.Now()
	endpoint := "https://nubela.co/api/v1/employee/profile?slug=" + url.QueryEscape(slug)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ninjapear: %w", err)
	}
	defer resp.Body.Close()
	body, _ := readAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ninjapear %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		FullName       string                 `json:"full_name"`
		Headline       string                 `json:"headline"`
		Country        string                 `json:"country_full_name"`
		City           string                 `json:"city"`
		State          string                 `json:"state"`
		Summary        string                 `json:"summary"`
		Connections    int                    `json:"connections"`
		Followers      int                    `json:"follower_count"`
		OccupationName string                 `json:"occupation"`
		Experiences    []map[string]interface{} `json:"experiences"`
		Education      []map[string]interface{} `json:"education"`
		Skills         []string                 `json:"skills"`
		Languages      []string                 `json:"languages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("ninjapear parse: %w", err)
	}
	loc := strings.TrimSpace(strings.Join(filterEmpty([]string{parsed.City, parsed.State}), ", "))
	return &LinkedInOutput{
		URL: profileURL, Slug: slug, FullName: parsed.FullName, Headline: parsed.Headline,
		Location: loc, Country: parsed.Country, Summary: parsed.Summary,
		Connections: parsed.Connections, Followers: parsed.Followers,
		OccupationName: parsed.OccupationName, Experiences: parsed.Experiences,
		Education: parsed.Education, Skills: parsed.Skills, Languages: parsed.Languages,
		Source: "nubela.co/api/v1/employee/profile (NinjaPear)", TookMs: time.Since(start).Milliseconds(),
	}, nil
}

// =============================================================================
// instagram_user — REQUIRES APIFY_API_TOKEN (Apify instagram-profile-scraper)
// =============================================================================

type InstagramOutput struct {
	Username        string `json:"username"`
	ID              string `json:"id,omitempty"`
	FullName        string `json:"full_name,omitempty"`
	Biography       string `json:"biography,omitempty"`
	ExternalURL     string `json:"external_url,omitempty"`
	IsVerified      bool   `json:"is_verified,omitempty"`
	IsPrivate       bool   `json:"is_private,omitempty"`
	FollowersCount  int    `json:"followers_count,omitempty"`
	FollowingCount  int    `json:"following_count,omitempty"`
	PostsCount      int    `json:"posts_count,omitempty"`
	ProfilePicURL   string `json:"profile_pic_url,omitempty"`
	BusinessCategory string `json:"business_category,omitempty"`
	BusinessEmail   string `json:"business_email,omitempty"`
	BusinessPhone   string `json:"business_phone,omitempty"`
	Source          string `json:"source"`
	TookMs          int64  `json:"tookMs"`
}

func InstagramUser(ctx context.Context, input map[string]any) (*InstagramOutput, error) {
	username, _ := input["username"].(string)
	username = strings.TrimSpace(strings.TrimPrefix(username, "@"))
	if username == "" {
		return nil, errors.New("input.username required")
	}
	apifyToken := os.Getenv("APIFY_API_TOKEN")
	if apifyToken == "" {
		return nil, errors.New(
			"APIFY_API_TOKEN env var required (Apify runs the instagram-profile-scraper actor, $5/mo+, https://apify.com/apify/instagram-profile-scraper). " +
				"Instagram blocks all unauthenticated scrapers; paid Apify is the most reliable path.")
	}
	start := time.Now()
	// Apify run-sync-get-dataset-items endpoint runs the actor and returns results
	// in a single HTTP call (otherwise actor runs are async).
	endpoint := "https://api.apify.com/v2/acts/apify~instagram-profile-scraper/run-sync-get-dataset-items?token=" + url.QueryEscape(apifyToken)
	body, _ := json.Marshal(map[string]interface{}{
		"usernames": []string{username},
		"resultsLimit": 1,
	})
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("apify: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := readAll(resp.Body)
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return nil, fmt.Errorf("apify %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	var items []struct {
		Username        string `json:"username"`
		ID              string `json:"id"`
		FullName        string `json:"fullName"`
		Biography       string `json:"biography"`
		ExternalURL     string `json:"externalUrl"`
		IsVerified      bool   `json:"verified"`
		IsPrivate       bool   `json:"private"`
		FollowersCount  int    `json:"followersCount"`
		FollowingCount  int    `json:"followsCount"`
		PostsCount      int    `json:"postsCount"`
		ProfilePicURL   string `json:"profilePicUrl"`
		BusinessCategory string `json:"businessCategoryName"`
		BusinessEmail   string `json:"publicEmail"`
		BusinessPhone   string `json:"publicPhoneNumber"`
	}
	if err := json.Unmarshal(respBody, &items); err != nil {
		return nil, fmt.Errorf("apify parse: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("instagram user %q not found via Apify scraper", username)
	}
	it := items[0]
	return &InstagramOutput{
		Username: it.Username, ID: it.ID, FullName: it.FullName, Biography: it.Biography,
		ExternalURL: it.ExternalURL, IsVerified: it.IsVerified, IsPrivate: it.IsPrivate,
		FollowersCount: it.FollowersCount, FollowingCount: it.FollowingCount, PostsCount: it.PostsCount,
		ProfilePicURL: it.ProfilePicURL, BusinessCategory: it.BusinessCategory,
		BusinessEmail: it.BusinessEmail, BusinessPhone: it.BusinessPhone,
		Source: "apify (instagram-profile-scraper)", TookMs: time.Since(start).Milliseconds(),
	}, nil
}

// readAll is a small stdlib shim so we don't import io in every file.
func readAll(r interface{ Read(p []byte) (n int, err error) }) ([]byte, error) {
	out := make([]byte, 0, 64<<10)
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if len(out) > 16<<20 {
				break
			}
		}
		if err != nil {
			break
		}
	}
	return out, nil
}

