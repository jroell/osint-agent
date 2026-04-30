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
	"sync"
	"time"
)

type IOSAppMetadata struct {
	BundleID            string   `json:"bundle_id"`
	TrackID             int64    `json:"track_id,omitempty"`
	TrackName           string   `json:"track_name,omitempty"`
	SellerName          string   `json:"seller_name,omitempty"`
	ArtistName          string   `json:"artist_name,omitempty"`
	Version             string   `json:"version,omitempty"`
	ReleaseDate         string   `json:"release_date,omitempty"`
	CurrentVersionDate  string   `json:"current_version_release_date,omitempty"`
	Description         string   `json:"description,omitempty"`
	ReleaseNotes        string   `json:"release_notes,omitempty"`
	MinimumOSVersion    string   `json:"minimum_os_version,omitempty"`
	Price               float64  `json:"price,omitempty"`
	Currency            string   `json:"currency,omitempty"`
	PrimaryGenre        string   `json:"primary_genre,omitempty"`
	Genres              []string `json:"genres,omitempty"`
	ContentRating       string   `json:"content_rating,omitempty"`
	AvgUserRatingCurrent float64 `json:"avg_rating_current_version,omitempty"`
	AvgUserRatingAll    float64  `json:"avg_rating_all_versions,omitempty"`
	UserRatingCount     int64    `json:"user_rating_count,omitempty"`
	TrackViewURL        string   `json:"track_view_url,omitempty"`
	IconURL             string   `json:"icon_url,omitempty"`
	ScreenshotURLs      []string `json:"screenshot_urls,omitempty"`
	SupportedDevices    []string `json:"supported_devices,omitempty"`
	BundleSize          string   `json:"file_size_bytes,omitempty"`
	Languages           []string `json:"languages,omitempty"`
	NotFound            bool     `json:"not_found,omitempty"`
	Source              string   `json:"source"`
}

type AndroidAppMetadata struct {
	PackageName       string `json:"package_name"`
	AppName           string `json:"app_name,omitempty"`
	Developer         string `json:"developer,omitempty"`
	DeveloperEmail    string `json:"developer_email,omitempty"`
	DeveloperWebsite  string `json:"developer_website,omitempty"`
	Description       string `json:"description,omitempty"`
	CurrentVersion    string `json:"current_version,omitempty"`
	InstallCount      string `json:"install_count,omitempty"`
	LastUpdated       string `json:"last_updated,omitempty"`
	Genre             string `json:"genre,omitempty"`
	ContentRating     string `json:"content_rating,omitempty"`
	Rating            string `json:"rating,omitempty"`
	RatingCount       string `json:"rating_count,omitempty"`
	IconURL           string `json:"icon_url,omitempty"`
	StoreURL          string `json:"store_url,omitempty"`
	NotFound          bool   `json:"not_found,omitempty"`
	Source            string `json:"source"`
	ParseNote         string `json:"parse_note,omitempty"`
}

type MobileAppLookupOutput struct {
	IOSApps     []IOSAppMetadata     `json:"ios_apps,omitempty"`
	AndroidApps []AndroidAppMetadata `json:"android_apps,omitempty"`
	TotalLookups int                 `json:"total_lookups"`
	Successful   int                 `json:"successful"`
	NotFound     int                 `json:"not_found"`
	Errors       map[string]string   `json:"errors,omitempty"`
	Source       string              `json:"source"`
	TookMs       int64               `json:"tookMs"`
}

// MobileAppLookup resolves iOS Bundle IDs and Android package names to full
// app metadata. Designed as the natural follow-up to well_known_recon (which
// surfaces IDs from .well-known/apple-app-site-association and .well-known/
// assetlinks.json) and to JS-extracted/manifest discovered IDs.
//
// Sources:
//   - iOS: Apple iTunes Lookup API (free, no key, no rate limit at this scale)
//          https://itunes.apple.com/lookup?bundleId=<id>
//   - Android: Google Play store HTML scraping (best-effort; most reliable
//              fields: app name, developer, current version, last updated)
//
// Returns rich app metadata: full developer name (often legal entity =
// shell-co revealed), current version + release notes (what's been shipped
// recently), screenshots, content rating, ratings count, install count band.
func MobileAppLookup(ctx context.Context, input map[string]any) (*MobileAppLookupOutput, error) {
	out := &MobileAppLookupOutput{Source: "mobile_app_lookup", Errors: map[string]string{}}
	start := time.Now()

	iosBundles := stringList(input["ios_bundle_ids"])
	if v, ok := input["ios_bundle_id"].(string); ok && v != "" {
		iosBundles = append(iosBundles, v)
	}
	androidPkgs := stringList(input["android_package_names"])
	if v, ok := input["android_package_name"].(string); ok && v != "" {
		androidPkgs = append(androidPkgs, v)
	}

	if len(iosBundles) == 0 && len(androidPkgs) == 0 {
		return nil, errors.New("at least one of ios_bundle_id(s) or android_package_name(s) required")
	}

	// Some IDs come prefixed with the Apple Team ID (e.g. "ABC123.com.example.app")
	// — the iTunes API uses the bare bundle (com.example.app). Strip team prefix.
	cleanIOS := make([]string, 0, len(iosBundles))
	for _, b := range iosBundles {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		// If looks like "TEAMID.com.foo.bar" (team is 10 alphanumerics)
		if i := strings.Index(b, "."); i == 10 {
			team := b[:i]
			if isAppleTeamID(team) {
				b = b[i+1:]
			}
		}
		cleanIOS = append(cleanIOS, b)
	}

	out.TotalLookups = len(cleanIOS) + len(androidPkgs)

	var wg sync.WaitGroup
	var mu sync.Mutex

	// iOS lookups (1 batched call — iTunes API takes comma-separated bundleIds)
	if len(cleanIOS) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := lookupIOSBundles(ctx, cleanIOS)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Errors["itunes"] = err.Error()
				return
			}
			for _, r := range results {
				if r.NotFound {
					out.NotFound++
				} else {
					out.Successful++
				}
				out.IOSApps = append(out.IOSApps, r)
			}
		}()
	}

	// Android lookups (parallel HTML scrapes)
	if len(androidPkgs) > 0 {
		results := make([]AndroidAppMetadata, len(androidPkgs))
		var awg sync.WaitGroup
		sem := make(chan struct{}, 6)
		for i, pkg := range androidPkgs {
			awg.Add(1)
			sem <- struct{}{}
			go func(idx int, p string) {
				defer awg.Done()
				defer func() { <-sem }()
				results[idx] = lookupAndroidPackage(ctx, p)
			}(i, pkg)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			awg.Wait()
			mu.Lock()
			defer mu.Unlock()
			for _, r := range results {
				if r.NotFound {
					out.NotFound++
				} else {
					out.Successful++
				}
				out.AndroidApps = append(out.AndroidApps, r)
			}
		}()
	}

	wg.Wait()
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func isAppleTeamID(s string) bool {
	if len(s) != 10 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

func lookupIOSBundles(ctx context.Context, bundles []string) ([]IOSAppMetadata, error) {
	endpoint := fmt.Sprintf("https://itunes.apple.com/lookup?bundleId=%s&entity=software",
		url.QueryEscape(strings.Join(bundles, ",")))
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/mobile-app-lookup")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("itunes status %d", resp.StatusCode)
	}
	var parsed struct {
		ResultCount int `json:"resultCount"`
		Results     []struct {
			BundleID                       string   `json:"bundleId"`
			TrackID                        int64    `json:"trackId"`
			TrackName                      string   `json:"trackName"`
			SellerName                     string   `json:"sellerName"`
			ArtistName                     string   `json:"artistName"`
			Version                        string   `json:"version"`
			ReleaseDate                    string   `json:"releaseDate"`
			CurrentVersionReleaseDate      string   `json:"currentVersionReleaseDate"`
			Description                    string   `json:"description"`
			ReleaseNotes                   string   `json:"releaseNotes"`
			MinimumOSVersion               string   `json:"minimumOsVersion"`
			Price                          float64  `json:"price"`
			Currency                       string   `json:"currency"`
			PrimaryGenreName               string   `json:"primaryGenreName"`
			Genres                         []string `json:"genres"`
			TrackContentRating             string   `json:"trackContentRating"`
			AverageUserRatingForCurrentVer float64  `json:"averageUserRatingForCurrentVersion"`
			AverageUserRating              float64  `json:"averageUserRating"`
			UserRatingCount                int64    `json:"userRatingCount"`
			TrackViewURL                   string   `json:"trackViewUrl"`
			ArtworkURL512                  string   `json:"artworkUrl512"`
			ArtworkURL100                  string   `json:"artworkUrl100"`
			ScreenshotURLs                 []string `json:"screenshotUrls"`
			IpadScreenshotURLs             []string `json:"ipadScreenshotUrls"`
			SupportedDevices               []string `json:"supportedDevices"`
			FileSizeBytes                  string   `json:"fileSizeBytes"`
			Languages                      []string `json:"languageCodesISO2A"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("itunes parse: %w", err)
	}

	results := []IOSAppMetadata{}
	foundSet := map[string]bool{}
	for _, r := range parsed.Results {
		icon := r.ArtworkURL512
		if icon == "" {
			icon = r.ArtworkURL100
		}
		shots := append([]string{}, r.ScreenshotURLs...)
		shots = append(shots, r.IpadScreenshotURLs...)
		if len(shots) > 8 {
			shots = shots[:8]
		}
		results = append(results, IOSAppMetadata{
			BundleID:           r.BundleID,
			TrackID:            r.TrackID,
			TrackName:          r.TrackName,
			SellerName:         r.SellerName,
			ArtistName:         r.ArtistName,
			Version:            r.Version,
			ReleaseDate:        r.ReleaseDate,
			CurrentVersionDate: r.CurrentVersionReleaseDate,
			Description:        truncate(r.Description, 800),
			ReleaseNotes:       truncate(r.ReleaseNotes, 600),
			MinimumOSVersion:   r.MinimumOSVersion,
			Price:              r.Price,
			Currency:           r.Currency,
			PrimaryGenre:       r.PrimaryGenreName,
			Genres:             r.Genres,
			ContentRating:      r.TrackContentRating,
			AvgUserRatingCurrent: r.AverageUserRatingForCurrentVer,
			AvgUserRatingAll:   r.AverageUserRating,
			UserRatingCount:    r.UserRatingCount,
			TrackViewURL:       r.TrackViewURL,
			IconURL:            icon,
			ScreenshotURLs:     shots,
			SupportedDevices:   r.SupportedDevices,
			BundleSize:         r.FileSizeBytes,
			Languages:          r.Languages,
			Source:             "itunes-lookup",
		})
		foundSet[r.BundleID] = true
	}
	// Mark not-founds
	for _, b := range bundles {
		if !foundSet[b] {
			results = append(results, IOSAppMetadata{
				BundleID: b,
				NotFound: true,
				Source:   "itunes-lookup",
			})
		}
	}
	return results, nil
}

// Google Play HTML scrape — extract robust metadata via OpenGraph + itemprop hints.
var (
	ogTitleRE       = regexp.MustCompile(`<meta[^>]+property="og:title"[^>]+content="([^"]+)"`)
	ogDescRE        = regexp.MustCompile(`<meta[^>]+property="og:description"[^>]+content="([^"]+)"`)
	ogImageRE       = regexp.MustCompile(`<meta[^>]+property="og:image"[^>]+content="([^"]+)"`)
	playUpdatedRE   = regexp.MustCompile(`(?i)Updated on[^<]*<[^>]+>([^<]+)`)
	playInstallsRE  = regexp.MustCompile(`>([\d,.+KMB]+)\s*<[^>]*>(?:downloads|installs)`)
	playDevEmailRE  = regexp.MustCompile(`mailto:([^"&]+)`)
	playDevSiteRE   = regexp.MustCompile(`href="(https?://[^"]+)"[^>]*>(?:Visit website|Website|developer\.[a-z]+)`)
	playRatingRE    = regexp.MustCompile(`(?i)\b(\d\.\d)\s*(?:stars?|<[^>]+>)\s*(?:[A-Za-z]{2,}\s+)?(\d[\d.,]*[KMB]?\+?)?\s*reviews`)
	playGenreRE     = regexp.MustCompile(`<a[^>]+href="/store/apps/category/([A-Z_]+)"[^>]*>([^<]+)</a>`)
	playVersionJSONRE = regexp.MustCompile(`"version_string":"([^"]+)"`)
	playDevNameJSONRE = regexp.MustCompile(`"developer_name":\s*"([^"]+)"`)
)

func lookupAndroidPackage(ctx context.Context, pkg string) AndroidAppMetadata {
	rec := AndroidAppMetadata{
		PackageName: pkg,
		StoreURL:    fmt.Sprintf("https://play.google.com/store/apps/details?id=%s", url.QueryEscape(pkg)),
		Source:      "google-play-scrape",
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, rec.StoreURL+"&hl=en&gl=US", nil)
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		rec.NotFound = true
		rec.ParseNote = "fetch failed: " + err.Error()
		return rec
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == 404 {
		rec.NotFound = true
		rec.ParseNote = "404 — package not in Play Store"
		return rec
	}
	if resp.StatusCode != 200 {
		rec.NotFound = true
		rec.ParseNote = fmt.Sprintf("status %d", resp.StatusCode)
		return rec
	}
	html := string(body)

	// app name + descr from og: tags
	if m := ogTitleRE.FindStringSubmatch(html); len(m) >= 2 {
		// "AppName - Apps on Google Play" → strip suffix
		title := m[1]
		title = strings.TrimSuffix(title, " - Apps on Google Play")
		title = strings.TrimSuffix(title, " - Google Play")
		rec.AppName = title
	}
	if m := ogDescRE.FindStringSubmatch(html); len(m) >= 2 {
		rec.Description = truncate(m[1], 600)
	}
	if m := ogImageRE.FindStringSubmatch(html); len(m) >= 2 {
		rec.IconURL = m[1]
	}

	// Version
	if m := playVersionJSONRE.FindStringSubmatch(html); len(m) >= 2 {
		rec.CurrentVersion = m[1]
	}

	// Developer name from JSON-ish blob
	if m := playDevNameJSONRE.FindStringSubmatch(html); len(m) >= 2 {
		rec.Developer = m[1]
	}

	// Updated date
	if m := playUpdatedRE.FindStringSubmatch(html); len(m) >= 2 {
		rec.LastUpdated = strings.TrimSpace(m[1])
	}

	// Installs
	if m := playInstallsRE.FindStringSubmatch(html); len(m) >= 2 {
		rec.InstallCount = strings.TrimSpace(m[1]) + "+"
	}

	// Developer email
	if m := playDevEmailRE.FindStringSubmatch(html); len(m) >= 2 {
		em := m[1]
		// Filter out googleplay's own email
		if !strings.Contains(em, "google.com") {
			rec.DeveloperEmail = em
		}
	}

	// Genre
	if m := playGenreRE.FindStringSubmatch(html); len(m) >= 3 {
		rec.Genre = m[2]
	}

	// Detect "We're sorry, the requested URL was not found" — Play Store soft 404
	if strings.Contains(strings.ToLower(html), "we're sorry, the requested url was not found") ||
		strings.Contains(strings.ToLower(html), "we couldn't find that page") {
		rec.NotFound = true
		rec.ParseNote = "soft 404 — package may be private/internal/region-restricted"
	}

	if rec.AppName == "" && !rec.NotFound {
		rec.ParseNote = "page parse incomplete — Play Store HTML format may have changed"
	}
	return rec
}

func stringList(v any) []string {
	out := []string{}
	if arr, ok := v.([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
