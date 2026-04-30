package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func hfTopN(counts map[string]int, n int) []HFTagAggregate {
	res := make([]HFTagAggregate, 0, len(counts))
	for v, c := range counts {
		res = append(res, HFTagAggregate{Tag: v, Count: c})
	}
	sort.SliceStable(res, func(i, j int) bool { return res[i].Count > res[j].Count })
	if len(res) > n {
		res = res[:n]
	}
	return res
}

// HFModel is a slim model representation.
type HFModel struct {
	ModelID      string   `json:"model_id"`
	Author       string   `json:"author,omitempty"`
	Downloads    int      `json:"downloads,omitempty"`
	Likes        int      `json:"likes,omitempty"`
	LibraryName  string   `json:"library,omitempty"`
	PipelineTag  string   `json:"pipeline_tag,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	License      string   `json:"license,omitempty"`
	BaseModel    any      `json:"base_model,omitempty"` // can be string or array
	TrainingData []string `json:"training_datasets,omitempty"`
	CreatedAt    string   `json:"created_at,omitempty"`
	LastModified string   `json:"last_modified,omitempty"`
	Languages    []string `json:"languages,omitempty"`
	Private      bool     `json:"private,omitempty"`
	Gated        any      `json:"gated,omitempty"`
}

// HFDataset is a slim dataset representation.
type HFDataset struct {
	DatasetID    string   `json:"dataset_id"`
	Author       string   `json:"author,omitempty"`
	Downloads    int      `json:"downloads,omitempty"`
	Likes        int      `json:"likes,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	License      string   `json:"license,omitempty"`
	CreatedAt    string   `json:"created_at,omitempty"`
	LastModified string   `json:"last_modified,omitempty"`
	Private      bool     `json:"private,omitempty"`
}

// HFUserOverview is a user/org profile.
type HFUserOverview struct {
	ID            string `json:"id,omitempty"`
	Type          string `json:"type,omitempty"` // "user" or "org"
	Username      string `json:"username,omitempty"`
	Fullname      string `json:"fullname,omitempty"`
	AvatarURL     string `json:"avatar_url,omitempty"`
	IsPro         bool   `json:"is_pro,omitempty"`
	NumModels     int    `json:"num_models,omitempty"`
	NumDatasets   int    `json:"num_datasets,omitempty"`
	NumSpaces     int    `json:"num_spaces,omitempty"`
	NumDiscussions int   `json:"num_discussions,omitempty"`
	NumPapers     int    `json:"num_papers,omitempty"`
	NumUpvotes    int    `json:"num_upvotes,omitempty"`
	NumLikes      int    `json:"num_likes,omitempty"`
	NumFollowers  int    `json:"num_followers,omitempty"`
	NumFollowing  int    `json:"num_following,omitempty"`
	ProfileURL    string `json:"profile_url,omitempty"`
}

// HFPaper is a paper detail.
type HFPaper struct {
	ArxivID      string   `json:"arxiv_id"`
	Title        string   `json:"title,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	PublishedDate string  `json:"published_date,omitempty"`
	Upvotes      int      `json:"upvotes,omitempty"`
	NumComments  int      `json:"num_comments,omitempty"`
	AuthorNames  []string `json:"author_names,omitempty"`
	HFAuthors    []string `json:"hf_user_authors,omitempty"`
	URL          string   `json:"url,omitempty"`
}

// HFTagAggregate counts top tag values across results.
type HFTagAggregate struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// HFOutput is the response.
type HFOutput struct {
	Mode             string             `json:"mode"`
	Query            string             `json:"query"`
	TotalReturned    int                `json:"total_returned"`
	Models           []HFModel          `json:"models,omitempty"`
	Datasets         []HFDataset        `json:"datasets,omitempty"`
	User             *HFUserOverview    `json:"user,omitempty"`
	Paper            *HFPaper           `json:"paper,omitempty"`
	TopTags          []HFTagAggregate   `json:"top_tags,omitempty"`
	TopAuthors       []HFTagAggregate   `json:"top_authors,omitempty"`
	TopLanguages     []HFTagAggregate   `json:"top_languages,omitempty"`
	TotalDownloads   int64              `json:"total_downloads,omitempty"`
	TotalLikes       int64              `json:"total_likes,omitempty"`
	HighlightFindings []string          `json:"highlight_findings"`
	Source           string             `json:"source"`
	TookMs           int64              `json:"tookMs"`
	Note             string             `json:"note,omitempty"`
}

type hfRawModel struct {
	ID            string   `json:"id"`
	ModelID       string   `json:"modelId"`
	Author        string   `json:"author"`
	Downloads     int      `json:"downloads"`
	Likes         int      `json:"likes"`
	LibraryName   string   `json:"library_name"`
	PipelineTag   string   `json:"pipeline_tag"`
	Tags          []string `json:"tags"`
	CreatedAt     string   `json:"createdAt"`
	LastModified  string   `json:"lastModified"`
	Private       bool     `json:"private"`
	Gated         any      `json:"gated"`
	CardData      map[string]any `json:"cardData"`
}

type hfRawDataset struct {
	ID            string   `json:"id"`
	Author        string   `json:"author"`
	Downloads     int      `json:"downloads"`
	Likes         int      `json:"likes"`
	Tags          []string `json:"tags"`
	CreatedAt     string   `json:"createdAt"`
	LastModified  string   `json:"lastModified"`
	Private       bool     `json:"private"`
	CardData      map[string]any `json:"cardData"`
}

type hfRawUser struct {
	ID            string `json:"_id"`
	User          string `json:"user"`
	Type          string `json:"type"`
	Fullname      string `json:"fullname"`
	AvatarURL     string `json:"avatarUrl"`
	IsPro         bool   `json:"isPro"`
	NumModels     int    `json:"numModels"`
	NumDatasets   int    `json:"numDatasets"`
	NumSpaces     int    `json:"numSpaces"`
	NumDiscussions int   `json:"numDiscussions"`
	NumPapers     int    `json:"numPapers"`
	NumUpvotes    int    `json:"numUpvotes"`
	NumLikes      int    `json:"numLikes"`
	NumFollowers  int    `json:"numFollowers"`
	NumFollowing  int    `json:"numFollowing"`
}

type hfRawPaper struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Authors  []struct {
		Name string `json:"name"`
		User struct {
			User string `json:"user"`
		} `json:"user"`
	} `json:"authors"`
	PublishedAt string `json:"publishedAt"`
	Upvotes     int    `json:"upvotes"`
	NumComments int    `json:"numComments"`
}

// HuggingFaceHubSearch queries the HuggingFace Hub public API for AI/ML
// community ER. Free, no auth required. Use cases:
//
//   - Resolve an AI researcher's HF identity → models published, papers,
//     datasets contributed (often the same handle as their GitHub).
//   - Track model lineage (zephyr-7b → fine-tuned from mistralai/Mistral-7B
//     → which uses tokenizer X). Lineage = research-collaboration graph.
//   - Author-network ER: every model under "meta-llama" is published by
//     Meta's AI org; co-fine-tunes between authors reveal collaborations.
//   - Paper attribution: who *registered* an arXiv paper on HF (often
//     more revealing than arXiv author list because HF requires identity).
//
// Modes:
//   - "models_by_author"   : all models by an author/org
//   - "models_search"      : full-text model search
//   - "model_detail"       : full model card (lineage, license, datasets)
//   - "datasets_by_author" : all datasets by an author/org
//   - "datasets_search"    : full-text dataset search
//   - "user_overview"      : user/org profile (numModels/Datasets/Papers/Followers)
//   - "paper_detail"       : paper by arXiv ID
func HuggingFaceHubSearch(ctx context.Context, input map[string]any) (*HFOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "models_by_author"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required (author/username/model_id/dataset_id/arxiv_id depending on mode)")
	}

	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	out := &HFOutput{Mode: mode, Query: query, Source: "huggingface.co/api"}
	start := time.Now()
	client := &http.Client{Timeout: 25 * time.Second}

	switch mode {
	case "models_by_author":
		params := url.Values{}
		params.Set("author", query)
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("sort", "downloads")
		params.Set("direction", "-1")
		raw, err := hfFetchModels(ctx, client, "https://huggingface.co/api/models?"+params.Encode())
		if err != nil {
			return nil, err
		}
		hfMaterializeModels(out, raw)
	case "models_search":
		params := url.Values{}
		params.Set("search", query)
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("sort", "downloads")
		params.Set("direction", "-1")
		raw, err := hfFetchModels(ctx, client, "https://huggingface.co/api/models?"+params.Encode())
		if err != nil {
			return nil, err
		}
		hfMaterializeModels(out, raw)
	case "model_detail":
		endpoint := "https://huggingface.co/api/models/" + query
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Header.Set("User-Agent", "osint-agent/0.1")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("hf model detail: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 || resp.StatusCode == 401 {
			// HF returns 401 for nonexistent or private models; treat as "not found / inaccessible"
			out.Note = fmt.Sprintf("model '%s' not found on HuggingFace Hub (or is private/gated)", query)
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("hf %d: %s", resp.StatusCode, string(body))
		}
		var raw hfRawModel
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return nil, err
		}
		hfMaterializeModels(out, []hfRawModel{raw})
	case "datasets_by_author":
		params := url.Values{}
		params.Set("author", query)
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("sort", "downloads")
		params.Set("direction", "-1")
		raw, err := hfFetchDatasets(ctx, client, "https://huggingface.co/api/datasets?"+params.Encode())
		if err != nil {
			return nil, err
		}
		hfMaterializeDatasets(out, raw)
	case "datasets_search":
		params := url.Values{}
		params.Set("search", query)
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("sort", "downloads")
		params.Set("direction", "-1")
		raw, err := hfFetchDatasets(ctx, client, "https://huggingface.co/api/datasets?"+params.Encode())
		if err != nil {
			return nil, err
		}
		hfMaterializeDatasets(out, raw)
	case "user_overview":
		// Try users endpoint first; if 404, try organizations endpoint
		endpoint := "https://huggingface.co/api/users/" + query + "/overview"
		userRaw, ok := hfFetchUser(ctx, client, endpoint)
		if !ok {
			endpoint = "https://huggingface.co/api/organizations/" + query + "/overview"
			userRaw, ok = hfFetchUser(ctx, client, endpoint)
		}
		if !ok {
			out.Note = fmt.Sprintf("user/org '%s' not found", query)
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		out.User = &HFUserOverview{
			ID:             userRaw.ID,
			Username:       query,
			Type:           userRaw.Type,
			Fullname:       userRaw.Fullname,
			AvatarURL:      userRaw.AvatarURL,
			IsPro:          userRaw.IsPro,
			NumModels:      userRaw.NumModels,
			NumDatasets:    userRaw.NumDatasets,
			NumSpaces:      userRaw.NumSpaces,
			NumDiscussions: userRaw.NumDiscussions,
			NumPapers:      userRaw.NumPapers,
			NumUpvotes:     userRaw.NumUpvotes,
			NumLikes:       userRaw.NumLikes,
			NumFollowers:   userRaw.NumFollowers,
			NumFollowing:   userRaw.NumFollowing,
			ProfileURL:     "https://huggingface.co/" + query,
		}
	case "paper_detail":
		endpoint := "https://huggingface.co/api/papers/" + query
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		req.Header.Set("User-Agent", "osint-agent/0.1")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("hf paper: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 {
			out.Note = fmt.Sprintf("arXiv ID '%s' not indexed on HuggingFace Papers", query)
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		var p hfRawPaper
		if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
			return nil, err
		}
		paper := &HFPaper{
			ArxivID:       p.ID,
			Title:         p.Title,
			Summary:       hfTruncate(p.Summary, 800),
			PublishedDate: p.PublishedAt,
			Upvotes:       p.Upvotes,
			NumComments:   p.NumComments,
			URL:           "https://huggingface.co/papers/" + p.ID,
		}
		for _, a := range p.Authors {
			if a.Name != "" {
				paper.AuthorNames = append(paper.AuthorNames, a.Name)
			}
			if a.User.User != "" {
				paper.HFAuthors = append(paper.HFAuthors, a.User.User)
			}
		}
		out.Paper = paper
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: models_by_author, models_search, model_detail, datasets_by_author, datasets_search, user_overview, paper_detail", mode)
	}

	out.HighlightFindings = hfBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func hfFetchModels(ctx context.Context, client *http.Client, endpoint string) ([]hfRawModel, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hf models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("hf %d: %s", resp.StatusCode, string(body))
	}
	var raw []hfRawModel
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func hfFetchDatasets(ctx context.Context, client *http.Client, endpoint string) ([]hfRawDataset, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hf datasets: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("hf %d: %s", resp.StatusCode, string(body))
	}
	var raw []hfRawDataset
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func hfFetchUser(ctx context.Context, client *http.Client, endpoint string) (*hfRawUser, bool) {
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, false
	}
	var raw hfRawUser
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, false
	}
	if raw.ID == "" && raw.Fullname == "" && raw.NumModels == 0 && raw.NumDatasets == 0 {
		return nil, false
	}
	return &raw, true
}

func hfMaterializeModels(out *HFOutput, raw []hfRawModel) {
	tagAgg := map[string]int{}
	authorAgg := map[string]int{}
	langAgg := map[string]int{}
	for _, r := range raw {
		id := r.ID
		if id == "" {
			id = r.ModelID
		}
		m := HFModel{
			ModelID:      id,
			Author:       r.Author,
			Downloads:    r.Downloads,
			Likes:        r.Likes,
			LibraryName:  r.LibraryName,
			PipelineTag:  r.PipelineTag,
			Tags:         r.Tags,
			CreatedAt:    r.CreatedAt,
			LastModified: r.LastModified,
			Private:      r.Private,
			Gated:        r.Gated,
		}
		// derive author from ID if missing
		if m.Author == "" && strings.Contains(id, "/") {
			m.Author = strings.SplitN(id, "/", 2)[0]
		}
		// extract from cardData
		if cd := r.CardData; cd != nil {
			if v, ok := cd["license"].(string); ok {
				m.License = v
			}
			if v, ok := cd["base_model"]; ok {
				m.BaseModel = v
			}
			// datasets used in training
			if v, ok := cd["datasets"]; ok {
				if arr, ok := v.([]any); ok {
					for _, x := range arr {
						if s, ok := x.(string); ok {
							m.TrainingData = append(m.TrainingData, s)
						}
					}
				} else if s, ok := v.(string); ok {
					m.TrainingData = append(m.TrainingData, s)
				}
			}
			if v, ok := cd["language"]; ok {
				if arr, ok := v.([]any); ok {
					for _, x := range arr {
						if s, ok := x.(string); ok {
							m.Languages = append(m.Languages, s)
							langAgg[s]++
						}
					}
				} else if s, ok := v.(string); ok {
					m.Languages = append(m.Languages, s)
					langAgg[s]++
				}
			}
		}
		out.Models = append(out.Models, m)
		out.TotalDownloads += int64(r.Downloads)
		out.TotalLikes += int64(r.Likes)
		if m.Author != "" {
			authorAgg[m.Author]++
		}
		for _, t := range r.Tags {
			tagAgg[t]++
		}
	}
	out.TotalReturned = len(out.Models)
	out.TopTags = hfTopN(tagAgg, 12)
	out.TopAuthors = hfTopN(authorAgg, 10)
	out.TopLanguages = hfTopN(langAgg, 10)
}

func hfMaterializeDatasets(out *HFOutput, raw []hfRawDataset) {
	tagAgg := map[string]int{}
	authorAgg := map[string]int{}
	for _, r := range raw {
		d := HFDataset{
			DatasetID:    r.ID,
			Author:       r.Author,
			Downloads:    r.Downloads,
			Likes:        r.Likes,
			Tags:         r.Tags,
			CreatedAt:    r.CreatedAt,
			LastModified: r.LastModified,
			Private:      r.Private,
		}
		if d.Author == "" && strings.Contains(r.ID, "/") {
			d.Author = strings.SplitN(r.ID, "/", 2)[0]
		}
		if cd := r.CardData; cd != nil {
			if v, ok := cd["license"].(string); ok {
				d.License = v
			}
		}
		out.Datasets = append(out.Datasets, d)
		out.TotalDownloads += int64(r.Downloads)
		out.TotalLikes += int64(r.Likes)
		if d.Author != "" {
			authorAgg[d.Author]++
		}
		for _, t := range r.Tags {
			tagAgg[t]++
		}
	}
	out.TotalReturned = len(out.Datasets)
	out.TopTags = hfTopN(tagAgg, 12)
	out.TopAuthors = hfTopN(authorAgg, 10)
}

func hfBuildHighlights(out *HFOutput) []string {
	hi := []string{}
	switch out.Mode {
	case "models_by_author", "models_search", "model_detail":
		hi = append(hi, fmt.Sprintf("%d models returned", out.TotalReturned))
		if out.TotalDownloads > 0 {
			hi = append(hi, fmt.Sprintf("total downloads across results: %s", fmtUSD(out.TotalDownloads)))
		}
		if out.TotalLikes > 0 {
			hi = append(hi, fmt.Sprintf("total likes: %d", out.TotalLikes))
		}
		if len(out.TopAuthors) > 0 {
			top := []string{}
			for _, a := range out.TopAuthors[:min2(5, len(out.TopAuthors))] {
				top = append(top, fmt.Sprintf("%s (%d)", a.Tag, a.Count))
			}
			hi = append(hi, "top authors: "+strings.Join(top, ", "))
		}
		if out.Mode == "model_detail" && len(out.Models) > 0 {
			m := out.Models[0]
			if m.BaseModel != nil {
				hi = append(hi, fmt.Sprintf("⛓️  fine-tuned from base_model: %v — lineage signal", m.BaseModel))
			}
			if len(m.TrainingData) > 0 {
				hi = append(hi, fmt.Sprintf("training datasets: %s", strings.Join(m.TrainingData, ", ")))
			}
			if m.License != "" {
				hi = append(hi, "license: "+m.License)
			}
		}
	case "datasets_by_author", "datasets_search":
		hi = append(hi, fmt.Sprintf("%d datasets returned", out.TotalReturned))
		if out.TotalDownloads > 0 {
			hi = append(hi, fmt.Sprintf("total downloads: %s", fmtUSD(out.TotalDownloads)))
		}
	case "user_overview":
		if out.User != nil {
			u := out.User
			hi = append(hi, fmt.Sprintf("u/%s — %s%s", u.Username,
				ifEmpty(u.Fullname, "(no fullname)"),
				map[bool]string{true: " [pro]", false: ""}[u.IsPro]))
			hi = append(hi, fmt.Sprintf("ml inventory: %d models, %d datasets, %d spaces, %d papers — %d followers, %d following",
				u.NumModels, u.NumDatasets, u.NumSpaces, u.NumPapers, u.NumFollowers, u.NumFollowing))
			if u.NumModels >= 10 || u.NumPapers >= 5 {
				hi = append(hi, "✓ active AI/ML practitioner — strong AI-community ER signal")
			} else if u.NumModels == 0 && u.NumDatasets == 0 && u.NumPapers == 0 {
				hi = append(hi, "⚠️  empty profile — possible squat or new account")
			}
			hi = append(hi, "profile: "+u.ProfileURL)
		}
	case "paper_detail":
		if out.Paper != nil {
			p := out.Paper
			hi = append(hi, fmt.Sprintf("📄 %s (arXiv:%s)", p.Title, p.ArxivID))
			hi = append(hi, fmt.Sprintf("%d upvotes, %d comments on HF Papers", p.Upvotes, p.NumComments))
			if len(p.HFAuthors) > 0 {
				hi = append(hi, fmt.Sprintf("✓ %d HF user authors (registered identities): %s", len(p.HFAuthors), strings.Join(p.HFAuthors, ", ")))
			}
			if len(p.AuthorNames) > 0 {
				hi = append(hi, fmt.Sprintf("paper author list (n=%d): %s", len(p.AuthorNames), strings.Join(p.AuthorNames, ", ")))
			}
		}
	}
	return hi
}

func hfTruncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func ifEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
