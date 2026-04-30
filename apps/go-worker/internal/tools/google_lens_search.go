package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// LensVisualMatch is one similar-image hit.
type LensVisualMatch struct {
	Title     string `json:"title,omitempty"`
	Source    string `json:"source,omitempty"`
	Link      string `json:"link,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Position  int    `json:"position,omitempty"`
	Price     string `json:"price,omitempty"`
}

// LensRelatedSearch is one suggested related search.
type LensRelatedSearch struct {
	Query string `json:"query"`
	Link  string `json:"link,omitempty"`
}

// LensProductMatch is product-recognition data when applicable.
type LensProductMatch struct {
	Title    string `json:"title,omitempty"`
	Brand    string `json:"brand,omitempty"`
	Source   string `json:"source,omitempty"`
	Price    string `json:"price,omitempty"`
	Link     string `json:"link,omitempty"`
}

// GoogleLensOutput is the response.
type GoogleLensOutput struct {
	ImageURL          string             `json:"image_url"`
	Backend           string             `json:"backend"` // serpapi | serper
	VisualMatches     []LensVisualMatch  `json:"visual_matches,omitempty"`
	ProductMatches    []LensProductMatch `json:"product_matches,omitempty"`
	RelatedSearches   []LensRelatedSearch `json:"related_searches,omitempty"`
	UniqueDomains     []string           `json:"unique_source_domains,omitempty"`
	HighlightFindings []string           `json:"highlight_findings"`
	Source            string             `json:"source"`
	TookMs            int64              `json:"tookMs"`
	Note              string             `json:"note,omitempty"`
}

// GoogleLensSearch performs a Google Lens visual-similarity search via either
// SerpAPI (primary) or Serper.dev (fallback). Both wrap Google Lens's
// underlying API. Returns visual matches (similar images on the web with
// titles + source + link), product matches (when Lens identifies an item),
// and related searches.
//
// Why this matters for ER:
//   - Distinct from gemini_image_analyze (content reasoning) and
//     reverse_image (TinEye/Bing similarity): Google Lens often
//     identifies SPECIFIC PRODUCTS and SPECIFIC LOCATIONS that Gemini
//     misses because it's the world's largest image-to-web index.
//   - For OSINT: find every page where a leaked screenshot/photo
//     appears on the web, identify the original source of an image,
//     find the specific product/painting/landmark with shopping links.
//   - Particularly strong for: branded products, art reproductions,
//     book covers, currency, signage, license plates.
//
// REQUIRES one of: SERPAPI_KEY (preferred — has google_lens engine),
// SERPER_API_KEY (alternative — POST /lens endpoint).
func GoogleLensSearch(ctx context.Context, input map[string]any) (*GoogleLensOutput, error) {
	imageURL, _ := input["image_url"].(string)
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return nil, fmt.Errorf("input.image_url required")
	}

	out := &GoogleLensOutput{
		ImageURL: imageURL,
		Source:   "Google Lens (via SerpAPI or Serper.dev wrapper)",
	}
	start := time.Now()

	// Try SerpAPI first (canonical google_lens engine)
	if serpKey := os.Getenv("SERPAPI_KEY"); serpKey != "" {
		if err := lensQuerySerpAPI(ctx, serpKey, imageURL, out); err == nil && len(out.VisualMatches) > 0 {
			out.Backend = "serpapi"
			finalizeLensOutput(out)
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
	}

	// Fallback: Serper.dev /lens
	if serperKey := os.Getenv("SERPER_API_KEY"); serperKey != "" {
		if err := lensQuerySerper(ctx, serperKey, imageURL, out); err == nil {
			out.Backend = "serper"
			finalizeLensOutput(out)
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
	}

	out.Note = "no SERPAPI_KEY or SERPER_API_KEY configured. Set one in env to use Google Lens. Falls back: this tool returns nothing today, but gemini_image_analyze can substitute for content reasoning."
	out.HighlightFindings = []string{out.Note}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// SerpAPI google_lens engine
type serpapiLensResp struct {
	VisualMatches []struct {
		Position  int    `json:"position"`
		Title     string `json:"title"`
		Source    string `json:"source"`
		Link      string `json:"link"`
		Thumbnail string `json:"thumbnail"`
		Price     struct {
			Value         string `json:"value"`
			ExtractedValue float64 `json:"extracted_value"`
			Currency      string `json:"currency"`
		} `json:"price"`
	} `json:"visual_matches"`
	RelatedSearches []struct {
		Query string `json:"query"`
		Link  string `json:"link"`
	} `json:"related_searches"`
	KnowledgeGraph map[string]any `json:"knowledge_graph"`
	Error          string         `json:"error"`
}

func lensQuerySerpAPI(ctx context.Context, apiKey, imageURL string, out *GoogleLensOutput) error {
	params := url.Values{}
	params.Set("engine", "google_lens")
	params.Set("url", imageURL)
	params.Set("api_key", apiKey)
	endpoint := "https://serpapi.com/search.json?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("serpapi %d: %s", resp.StatusCode, string(body))
	}
	var raw serpapiLensResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return err
	}
	if raw.Error != "" {
		return fmt.Errorf("serpapi: %s", raw.Error)
	}
	for _, m := range raw.VisualMatches {
		out.VisualMatches = append(out.VisualMatches, LensVisualMatch{
			Position:  m.Position,
			Title:     m.Title,
			Source:    m.Source,
			Link:      m.Link,
			Thumbnail: m.Thumbnail,
			Price:     m.Price.Value,
		})
	}
	for _, r := range raw.RelatedSearches {
		out.RelatedSearches = append(out.RelatedSearches, LensRelatedSearch{Query: r.Query, Link: r.Link})
	}
	return nil
}

// Serper.dev /lens endpoint
type serperLensResp struct {
	VisualMatches []struct {
		Title  string `json:"title"`
		Source string `json:"source"`
		Link   string `json:"link"`
		Image  string `json:"image"`
	} `json:"visual_matches"`
	RelatedSearches []struct {
		Query string `json:"query"`
	} `json:"related_searches"`
	Message string `json:"message"`
}

func lensQuerySerper(ctx context.Context, apiKey, imageURL string, out *GoogleLensOutput) error {
	body, _ := json.Marshal(map[string]any{"url": imageURL})
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://google.serper.dev/lens", strings.NewReader(string(body)))
	req.Header.Set("X-API-KEY", apiKey)
	req.Header.Set("Content-Type", "application/json")
	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("serper %d: %s", resp.StatusCode, hfTruncate(string(rawBody), 200))
	}
	var raw serperLensResp
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		return err
	}
	if raw.Message != "" {
		return fmt.Errorf("serper: %s", raw.Message)
	}
	for i, m := range raw.VisualMatches {
		out.VisualMatches = append(out.VisualMatches, LensVisualMatch{
			Position:  i + 1,
			Title:     m.Title,
			Source:    m.Source,
			Link:      m.Link,
			Thumbnail: m.Image,
		})
	}
	for _, r := range raw.RelatedSearches {
		out.RelatedSearches = append(out.RelatedSearches, LensRelatedSearch{Query: r.Query})
	}
	return nil
}

func finalizeLensOutput(out *GoogleLensOutput) {
	domSet := map[string]struct{}{}
	for _, m := range out.VisualMatches {
		if m.Source != "" {
			domSet[m.Source] = struct{}{}
		}
		if m.Link != "" {
			if u, err := url.Parse(m.Link); err == nil && u.Host != "" {
				host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
				domSet[host] = struct{}{}
			}
		}
	}
	for d := range domSet {
		out.UniqueDomains = append(out.UniqueDomains, d)
	}
	sort.Strings(out.UniqueDomains)

	hi := []string{
		fmt.Sprintf("✓ %d visual matches via %s for image %s", len(out.VisualMatches), out.Backend, out.ImageURL),
	}
	if len(out.UniqueDomains) > 0 {
		hi = append(hi, fmt.Sprintf("🌐 %d unique source domains", len(out.UniqueDomains)))
	}
	for i, m := range out.VisualMatches {
		if i >= 5 {
			break
		}
		hi = append(hi, fmt.Sprintf("  %d. [%s] %s — %s", m.Position, m.Source, hfTruncate(m.Title, 60), m.Link))
	}
	if len(out.RelatedSearches) > 0 {
		queries := []string{}
		for _, r := range out.RelatedSearches[:min2(5, len(out.RelatedSearches))] {
			queries = append(queries, r.Query)
		}
		hi = append(hi, "🔎 related searches Lens suggests: "+strings.Join(queries, " | "))
	}
	out.HighlightFindings = hi
}
