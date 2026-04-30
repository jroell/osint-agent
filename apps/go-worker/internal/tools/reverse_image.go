package tools

import (
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

type ReverseImageMatch struct {
	Source     string  `json:"source"`     // "tineye" | "bing"
	URL        string  `json:"url"`
	Domain     string  `json:"domain,omitempty"`
	Title      string  `json:"title,omitempty"`
	Score      float64 `json:"score,omitempty"`
	Width      int     `json:"width,omitempty"`
	Height     int     `json:"height,omitempty"`
	Thumbnail  string  `json:"thumbnail,omitempty"`
}

type ReverseImageOutput struct {
	ImageURL string              `json:"image_url"`
	Matches  []ReverseImageMatch `json:"matches"`
	Engines  []string            `json:"engines_queried"`
	Source   string              `json:"source"`
	TookMs   int64               `json:"tookMs"`
}

// ReverseImageSearch dispatches to the configured reverse-image-search engines
// and aggregates results. Both supported engines are paid:
//   * TINEYE_API_KEY            (TinEye API, https://services.tineye.com/)
//   * BING_VISUAL_SEARCH_KEY    (Bing Visual Search, Azure Cognitive Services)
// At least one must be set. Yandex's official endpoint is gone (Yandex
// shuttered their public API in 2023); for Yandex coverage, route the URL
// through stealth_http_fetch against yandex.com/images/search?rpt=imageview.
func ReverseImageSearch(ctx context.Context, input map[string]any) (*ReverseImageOutput, error) {
	imgURL, _ := input["image_url"].(string)
	imgURL = strings.TrimSpace(imgURL)
	if imgURL == "" {
		return nil, errors.New("input.image_url required (publicly-reachable image URL)")
	}
	tineyeKey := os.Getenv("TINEYE_API_KEY")
	bingKey := os.Getenv("BING_VISUAL_SEARCH_KEY")
	if tineyeKey == "" && bingKey == "" {
		return nil, errors.New(
			"reverse_image_search requires at least one of TINEYE_API_KEY (https://services.tineye.com/) " +
				"or BING_VISUAL_SEARCH_KEY (Azure Cognitive Services). Both are paid.",
		)
	}
	limit := 30
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	start := time.Now()
	out := &ReverseImageOutput{
		ImageURL: imgURL,
		Matches:  []ReverseImageMatch{},
		Source:   "reverse_image_search",
	}

	if tineyeKey != "" {
		out.Engines = append(out.Engines, "tineye")
		matches, err := queryTinEye(ctx, tineyeKey, imgURL, limit)
		if err == nil {
			out.Matches = append(out.Matches, matches...)
		}
	}
	if bingKey != "" {
		out.Engines = append(out.Engines, "bing")
		matches, err := queryBingVisual(ctx, bingKey, imgURL, limit)
		if err == nil {
			out.Matches = append(out.Matches, matches...)
		}
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func queryTinEye(ctx context.Context, key, imgURL string, limit int) ([]ReverseImageMatch, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("https://api.tineye.com/rest/search/?image_url=%s&limit=%d&api_key=%s",
		url.QueryEscape(imgURL), limit, url.QueryEscape(key))
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("tineye %d", resp.StatusCode)
	}
	body := make([]byte, 0)
	buf := make([]byte, 32<<10)
	for {
		n, e := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if e != nil {
			break
		}
	}
	var parsed struct {
		Results struct {
			Matches []struct {
				BackLinks []struct {
					URL    string `json:"url"`
					Domain string `json:"backlink_domain"`
				} `json:"backlinks"`
				Width  int     `json:"width"`
				Height int     `json:"height"`
				Score  float64 `json:"score"`
			} `json:"matches"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	out := []ReverseImageMatch{}
	for _, m := range parsed.Results.Matches {
		for _, b := range m.BackLinks {
			out = append(out, ReverseImageMatch{
				Source: "tineye", URL: b.URL, Domain: b.Domain,
				Width: m.Width, Height: m.Height, Score: m.Score,
			})
		}
	}
	return out, nil
}

func queryBingVisual(ctx context.Context, key, imgURL string, limit int) ([]ReverseImageMatch, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	endpoint := "https://api.bing.microsoft.com/v7.0/images/visualsearch?imgUrl=" + url.QueryEscape(imgURL)
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Ocp-Apim-Subscription-Key", key)
	req.Header.Set("User-Agent", "osint-agent/0.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("bing %d", resp.StatusCode)
	}
	body := make([]byte, 0)
	buf := make([]byte, 32<<10)
	for {
		n, e := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if e != nil {
			break
		}
	}
	var parsed struct {
		Tags []struct {
			Actions []struct {
				ActionType string `json:"actionType"`
				Data       struct {
					Value []struct {
						HostPageURL    string `json:"hostPageUrl"`
						HostPageDomain string `json:"hostPageDomainFriendlyName"`
						Name           string `json:"name"`
						ContentURL     string `json:"contentUrl"`
						Width          int    `json:"width"`
						Height         int    `json:"height"`
						ThumbnailURL   string `json:"thumbnailUrl"`
					} `json:"value"`
				} `json:"data"`
			} `json:"actions"`
		} `json:"tags"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	out := []ReverseImageMatch{}
	count := 0
	for _, t := range parsed.Tags {
		for _, a := range t.Actions {
			if a.ActionType != "PagesIncluding" {
				continue
			}
			for _, v := range a.Data.Value {
				out = append(out, ReverseImageMatch{
					Source: "bing", URL: v.HostPageURL, Domain: v.HostPageDomain,
					Title: v.Name, Width: v.Width, Height: v.Height, Thumbnail: v.ThumbnailURL,
				})
				count++
				if count >= limit {
					return out, nil
				}
			}
		}
	}
	return out, nil
}
