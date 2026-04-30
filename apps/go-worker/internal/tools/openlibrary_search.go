package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenLibrarySearch wraps OpenLibrary's free no-auth public API for
// book/author ER. ~30M books, 5M+ authors. Operated by Internet Archive.
//
// Three modes:
//
//   - "search_books"   : title/author/subject query → matching books with
//                         ISBNs + publishers + subject taxonomy + first
//                         publish year + languages
//   - "search_authors" : fuzzy author name → author with bio + birth/death
//                         dates + work count + alternate names + top
//                         subjects + top work
//   - "isbn_lookup"    : ISBN → book metadata (title, authors, publish
//                         date, page count, language, subjects, library
//                         classifications)

type OLBook struct {
	Title             string   `json:"title"`
	Authors           []string `json:"authors,omitempty"`
	AuthorKeys        []string `json:"author_keys,omitempty"`
	FirstPublishYear  int      `json:"first_publish_year,omitempty"`
	Publishers        []string `json:"publishers,omitempty"`
	ISBNs             []string `json:"isbns,omitempty"`
	Subjects          []string `json:"subjects,omitempty"`
	Languages         []string `json:"languages,omitempty"`
	Key               string   `json:"key,omitempty"` // OpenLibrary work key
	NumberOfPages     int      `json:"number_of_pages,omitempty"`
	LCC               []string `json:"lc_classifications,omitempty"`
	Dewey             []string `json:"dewey_decimal_class,omitempty"`
	URL               string   `json:"url,omitempty"`
}

type OLAuthor struct {
	Key             string   `json:"key"`
	Name            string   `json:"name"`
	BirthDate       string   `json:"birth_date,omitempty"`
	DeathDate       string   `json:"death_date,omitempty"`
	WorkCount       int      `json:"work_count,omitempty"`
	TopWork         string   `json:"top_work,omitempty"`
	TopSubjects     []string `json:"top_subjects,omitempty"`
	AlternateNames  []string `json:"alternate_names,omitempty"`
	URL             string   `json:"url,omitempty"`
}

type OpenLibrarySearchOutput struct {
	Mode              string     `json:"mode"`
	Query             string     `json:"query,omitempty"`
	TotalCount        int        `json:"total_count,omitempty"`
	Returned          int        `json:"returned"`
	Books             []OLBook   `json:"books,omitempty"`
	Book              *OLBook    `json:"book,omitempty"`
	Authors           []OLAuthor `json:"authors,omitempty"`

	HighlightFindings []string   `json:"highlight_findings"`
	Source            string     `json:"source"`
	TookMs            int64      `json:"tookMs"`
	Note              string     `json:"note,omitempty"`
}

func OpenLibrarySearch(ctx context.Context, input map[string]any) (*OpenLibrarySearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		// Auto-detect
		if _, ok := input["isbn"]; ok {
			mode = "isbn_lookup"
		} else if _, ok := input["author"]; ok {
			mode = "search_authors"
		} else {
			mode = "search_books"
		}
	}

	out := &OpenLibrarySearchOutput{
		Mode:   mode,
		Source: "openlibrary.org",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	switch mode {
	case "search_books":
		title, _ := input["title"].(string)
		author, _ := input["author"].(string)
		subject, _ := input["subject"].(string)
		query, _ := input["query"].(string)
		title = strings.TrimSpace(title)
		author = strings.TrimSpace(author)
		subject = strings.TrimSpace(subject)
		query = strings.TrimSpace(query)
		if title == "" && author == "" && subject == "" && query == "" {
			return nil, fmt.Errorf("at least one of title, author, subject, or query required")
		}
		params := url.Values{}
		queryParts := []string{}
		if title != "" {
			params.Set("title", title)
			queryParts = append(queryParts, "title="+title)
		}
		if author != "" {
			params.Set("author", author)
			queryParts = append(queryParts, "author="+author)
		}
		if subject != "" {
			params.Set("subject", subject)
			queryParts = append(queryParts, "subject="+subject)
		}
		if query != "" {
			params.Set("q", query)
			queryParts = append(queryParts, "q="+query)
		}
		out.Query = strings.Join(queryParts, " · ")
		limit := 10
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 50 {
			limit = int(l)
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("fields", "title,author_name,author_key,first_publish_year,publisher,isbn,key,subject,language,number_of_pages_median,lcc,ddc")
		body, err := olGet(ctx, cli, "https://openlibrary.org/search.json?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			NumFound int                `json:"numFound"`
			Docs     []map[string]any   `json:"docs"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("openlibrary decode: %w", err)
		}
		out.TotalCount = raw.NumFound
		for _, d := range raw.Docs {
			b := OLBook{
				Title:            gtString(d, "title"),
				Authors:          olStringSlice(d, "author_name"),
				AuthorKeys:       olStringSlice(d, "author_key"),
				FirstPublishYear: gtInt(d, "first_publish_year"),
				Publishers:       olStringSlice(d, "publisher"),
				ISBNs:            olStringSlice(d, "isbn"),
				Subjects:         olStringSlice(d, "subject"),
				Languages:        olStringSlice(d, "language"),
				Key:              gtString(d, "key"),
				LCC:              olStringSlice(d, "lcc"),
				Dewey:            olStringSlice(d, "ddc"),
				NumberOfPages:    gtInt(d, "number_of_pages_median"),
			}
			if b.Key != "" {
				b.URL = "https://openlibrary.org" + b.Key
			}
			// Cap each big slice at first 5 to keep response manageable
			if len(b.Subjects) > 8 {
				b.Subjects = b.Subjects[:8]
			}
			if len(b.Publishers) > 4 {
				b.Publishers = b.Publishers[:4]
			}
			if len(b.ISBNs) > 4 {
				b.ISBNs = b.ISBNs[:4]
			}
			out.Books = append(out.Books, b)
		}
		out.Returned = len(out.Books)

	case "search_authors":
		q, _ := input["author"].(string)
		if q == "" {
			q, _ = input["query"].(string)
		}
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, fmt.Errorf("input.author or input.query required for search_authors mode")
		}
		out.Query = q
		params := url.Values{}
		params.Set("q", q)
		limit := 5
		if l, ok := input["limit"].(float64); ok && l > 0 && l <= 25 {
			limit = int(l)
		}
		params.Set("limit", fmt.Sprintf("%d", limit))
		body, err := olGet(ctx, cli, "https://openlibrary.org/search/authors.json?"+params.Encode())
		if err != nil {
			return nil, err
		}
		var raw struct {
			NumFound int                `json:"numFound"`
			Docs     []map[string]any   `json:"docs"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("authors decode: %w", err)
		}
		out.TotalCount = raw.NumFound
		for _, d := range raw.Docs {
			a := OLAuthor{
				Key:            gtString(d, "key"),
				Name:           gtString(d, "name"),
				BirthDate:      gtString(d, "birth_date"),
				DeathDate:      gtString(d, "death_date"),
				WorkCount:      gtInt(d, "work_count"),
				TopWork:        gtString(d, "top_work"),
				TopSubjects:    olStringSlice(d, "top_subjects"),
				AlternateNames: olStringSlice(d, "alternate_names"),
			}
			if a.Key != "" {
				a.URL = "https://openlibrary.org/authors/" + a.Key
			}
			if len(a.TopSubjects) > 8 {
				a.TopSubjects = a.TopSubjects[:8]
			}
			if len(a.AlternateNames) > 6 {
				a.AlternateNames = a.AlternateNames[:6]
			}
			out.Authors = append(out.Authors, a)
		}
		out.Returned = len(out.Authors)

	case "isbn_lookup":
		isbn, _ := input["isbn"].(string)
		isbn = strings.ReplaceAll(strings.TrimSpace(isbn), "-", "")
		if isbn == "" {
			return nil, fmt.Errorf("input.isbn required for isbn_lookup mode")
		}
		out.Query = isbn
		body, err := olGet(ctx, cli, "https://openlibrary.org/isbn/"+isbn+".json")
		if err != nil {
			return nil, err
		}
		// Empty body = no record
		if len(body) == 0 || strings.TrimSpace(string(body)) == "" {
			out.Note = fmt.Sprintf("ISBN %s not found in OpenLibrary", isbn)
			break
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, fmt.Errorf("isbn decode: %w", err)
		}
		// Authors are referenced as [{key:"/authors/OL..."}] — need to resolve
		var authorNames []string
		var authorKeys []string
		if authors, ok := raw["authors"].([]any); ok {
			for _, a := range authors {
				if am, ok := a.(map[string]any); ok {
					if k := gtString(am, "key"); k != "" {
						authorKeys = append(authorKeys, k)
					}
				}
			}
		}
		// Fetch each author's name (one extra round-trip per author; usually 1-2)
		for _, k := range authorKeys {
			authorBody, err := olGet(ctx, cli, "https://openlibrary.org"+k+".json")
			if err != nil {
				continue
			}
			var ar map[string]any
			if err := json.Unmarshal(authorBody, &ar); err == nil {
				if name := gtString(ar, "name"); name != "" {
					authorNames = append(authorNames, name)
				}
			}
		}
		b := &OLBook{
			Title:         gtString(raw, "title"),
			Authors:       authorNames,
			AuthorKeys:    authorKeys,
			Publishers:    olStringSlice(raw, "publishers"),
			ISBNs:         append(olStringSlice(raw, "isbn_13"), olStringSlice(raw, "isbn_10")...),
			Subjects:      olStringSlice(raw, "subjects"),
			Languages:     olLangSlice(raw, "languages"),
			Key:           gtString(raw, "key"),
			NumberOfPages: gtInt(raw, "number_of_pages"),
			LCC:           olStringSlice(raw, "lc_classifications"),
			Dewey:         olStringSlice(raw, "dewey_decimal_class"),
		}
		if b.Key != "" {
			b.URL = "https://openlibrary.org" + b.Key
		}
		// Try to extract publish_date (single string field for editions)
		if pd, ok := raw["publish_date"].(string); ok && pd != "" {
			// Surface in subjects for now (no dedicated field)
			b.Subjects = append([]string{"published: " + pd}, b.Subjects...)
		}
		if len(b.Subjects) > 8 {
			b.Subjects = b.Subjects[:8]
		}
		out.Book = b
		out.Returned = 1

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: search_books, search_authors, isbn_lookup", mode)
	}

	out.HighlightFindings = buildOLHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func olStringSlice(m map[string]any, key string) []string {
	out := []string{}
	if v, ok := m[key].([]any); ok {
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func olLangSlice(m map[string]any, key string) []string {
	out := []string{}
	if v, ok := m[key].([]any); ok {
		for _, x := range v {
			if obj, ok := x.(map[string]any); ok {
				if k := gtString(obj, "key"); k != "" {
					// Format: /languages/eng → "eng"
					parts := strings.Split(k, "/")
					if len(parts) > 0 {
						out = append(out, parts[len(parts)-1])
					}
				}
			} else if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func olGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openlibrary: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == 404 {
		// Empty body for not-found; let caller decide handling
		return []byte{}, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openlibrary HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildOLHighlights(o *OpenLibrarySearchOutput) []string {
	hi := []string{}
	switch o.Mode {
	case "search_books":
		hi = append(hi, fmt.Sprintf("✓ %d books match %s (returning %d)", o.TotalCount, o.Query, o.Returned))
		for i, b := range o.Books {
			if i >= 5 {
				break
			}
			authors := strings.Join(b.Authors, ", ")
			yr := ""
			if b.FirstPublishYear > 0 {
				yr = fmt.Sprintf(" (%d)", b.FirstPublishYear)
			}
			isbn := ""
			if len(b.ISBNs) > 0 {
				isbn = " · ISBN " + b.ISBNs[0]
			}
			hi = append(hi, fmt.Sprintf("  • %s%s by %s%s", b.Title, yr, authors, isbn))
			if len(b.Subjects) > 0 {
				topSubjects := b.Subjects
				if len(topSubjects) > 4 {
					topSubjects = topSubjects[:4]
				}
				hi = append(hi, "    subjects: "+strings.Join(topSubjects, ", "))
			}
		}

	case "search_authors":
		hi = append(hi, fmt.Sprintf("✓ %d authors match '%s'", o.TotalCount, o.Query))
		for i, a := range o.Authors {
			if i >= 4 {
				break
			}
			lifespan := ""
			if a.BirthDate != "" || a.DeathDate != "" {
				lifespan = fmt.Sprintf(" (%s – %s)", a.BirthDate, a.DeathDate)
			}
			hi = append(hi, fmt.Sprintf("  • %s%s [%s]", a.Name, lifespan, a.Key))
			if a.WorkCount > 0 {
				topW := ""
				if a.TopWork != "" {
					topW = " · top work: " + a.TopWork
				}
				hi = append(hi, fmt.Sprintf("    %d works%s", a.WorkCount, topW))
			}
			if len(a.AlternateNames) > 0 {
				alts := a.AlternateNames
				if len(alts) > 4 {
					alts = alts[:4]
				}
				hi = append(hi, "    alt names: "+strings.Join(alts, "; "))
			}
			if len(a.TopSubjects) > 0 {
				subs := a.TopSubjects
				if len(subs) > 5 {
					subs = subs[:5]
				}
				hi = append(hi, "    top subjects: "+strings.Join(subs, ", "))
			}
		}

	case "isbn_lookup":
		if o.Book == nil {
			hi = append(hi, fmt.Sprintf("✗ ISBN %s not in OpenLibrary", o.Query))
			break
		}
		b := o.Book
		hi = append(hi, fmt.Sprintf("✓ ISBN %s → %s", o.Query, b.Title))
		if len(b.Authors) > 0 {
			hi = append(hi, "  authors: "+strings.Join(b.Authors, ", "))
		}
		if len(b.Publishers) > 0 {
			hi = append(hi, "  publisher(s): "+strings.Join(b.Publishers, ", "))
		}
		details := []string{}
		if b.NumberOfPages > 0 {
			details = append(details, fmt.Sprintf("%d pages", b.NumberOfPages))
		}
		if len(b.Languages) > 0 {
			details = append(details, "language: "+strings.Join(b.Languages, "/"))
		}
		if len(details) > 0 {
			hi = append(hi, "  "+strings.Join(details, " · "))
		}
		if len(b.Subjects) > 0 {
			subs := b.Subjects
			if len(subs) > 6 {
				subs = subs[:6]
			}
			hi = append(hi, "  subjects: "+strings.Join(subs, ", "))
		}
		if b.URL != "" {
			hi = append(hi, "  url: "+b.URL)
		}
	}
	return hi
}
