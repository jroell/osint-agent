package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SOQuestion struct {
	ID            int    `json:"question_id"`
	Title         string `json:"title"`
	Tags          string `json:"tags"`
	Score         int    `json:"score"`
	ViewCount     int    `json:"view_count"`
	AnswerCount   int    `json:"answer_count"`
	CreationDate  string `json:"creation_date"`
	URL           string `json:"url"`
	OwnerUserID   int    `json:"owner_user_id,omitempty"`
}

type SOUser struct {
	ID           int    `json:"user_id"`
	DisplayName  string `json:"display_name"`
	Reputation   int    `json:"reputation"`
	Location     string `json:"location,omitempty"`
	AboutMe      string `json:"about_me_excerpt,omitempty"`
	WebsiteURL   string `json:"website_url,omitempty"`
	UpVotes      int    `json:"up_votes"`
	DownVotes    int    `json:"down_votes"`
	URL          string `json:"profile_url"`
}

type SOTagAggregation struct {
	Tag       string `json:"tag"`
	Questions int    `json:"questions_asked"`
}

type BQStackOverflowOutput struct {
	Mode             string              `json:"mode"`
	Query            string              `json:"query"`
	TotalReturned    int                 `json:"total_returned"`
	Questions        []SOQuestion        `json:"questions,omitempty"`
	Users            []SOUser            `json:"users,omitempty"`
	UserExpertise    []SOTagAggregation  `json:"user_expertise_tags,omitempty"`
	HighlightFindings []string           `json:"highlight_findings"`
	Source           string              `json:"source"`
	TookMs           int64               `json:"tookMs"`
	Note             string              `json:"note,omitempty"`
}

// BigQueryStackOverflow queries the Stack Overflow archive on BigQuery
// (`bigquery-public-data.stackoverflow.posts_questions/answers/users/tags`).
//
// Modes:
//   - "user_search": find users by display name (returns reputation, location, bio, votes)
//   - "user_expertise" (requires user_id): top tags this user has asked questions in
//     — strong proxy for their domain expertise
//   - "tag_top_users": top users by reputation in a specific tag (e.g. who's the
//     top expert in 'langchain'?)
//   - "keyword_search": questions matching a keyword (broader)
//
// Use cases:
//   - Tech-identity ER: given a name, find their SO presence + expertise areas
//   - Recruiting: identify domain experts in a tag
//   - Cross-reference with `github_user`/`github_emails` for full developer profile
func BigQueryStackOverflow(ctx context.Context, input map[string]any) (*BQStackOverflowOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "user_search"
	}
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required")
	}
	safeQ := strings.ReplaceAll(q, "'", "")
	safeQ = strings.ReplaceAll(safeQ, "\\", "")

	limit := 20
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	start := time.Now()
	out := &BQStackOverflowOutput{
		Mode: mode, Query: q,
		Source: "bigquery-public-data.stackoverflow",
	}

	switch mode {
	case "user_search":
		sql := fmt.Sprintf(`
SELECT id, display_name, reputation, location, about_me, up_votes, down_votes, website_url
FROM `+"`bigquery-public-data.stackoverflow.users`"+`
WHERE LOWER(display_name) LIKE '%%%s%%'
ORDER BY reputation DESC LIMIT %d`, strings.ToLower(safeQ), limit)
		rows, err := bqQuery(ctx, sql, limit)
		if err != nil {
			return nil, fmt.Errorf("user search: %w", err)
		}
		for _, r := range rows {
			u := SOUser{}
			u.ID = parseBQInt(r["id"])
			if v, ok := r["display_name"].(string); ok {
				u.DisplayName = v
			}
			u.Reputation = parseBQInt(r["reputation"])
			if v, ok := r["location"].(string); ok {
				u.Location = v
			}
			if v, ok := r["about_me"].(string); ok {
				u.AboutMe = truncate(stripTagsBody(v), 250)
			}
			if v, ok := r["website_url"].(string); ok {
				u.WebsiteURL = v
			}
			u.UpVotes = parseBQInt(r["up_votes"])
			u.DownVotes = parseBQInt(r["down_votes"])
			u.URL = fmt.Sprintf("https://stackoverflow.com/users/%d", u.ID)
			out.Users = append(out.Users, u)
		}
		out.TotalReturned = len(out.Users)

	case "user_expertise":
		userID := parseBQInt(safeQ)
		if userID == 0 {
			return nil, errors.New("user_expertise mode requires numeric user_id as query")
		}
		sql := fmt.Sprintf(`
SELECT tag, COUNT(*) AS c FROM (
  SELECT TRIM(t) AS tag
  FROM `+"`bigquery-public-data.stackoverflow.posts_questions`"+`,
  UNNEST(SPLIT(REPLACE(REPLACE(tags, '<', ''), '>', '|'), '|')) AS t
  WHERE owner_user_id = %d AND TRIM(t) != ''
)
GROUP BY tag ORDER BY c DESC LIMIT 20`, userID)
		rows, err := bqQuery(ctx, sql, 20)
		if err != nil {
			return nil, fmt.Errorf("user expertise: %w", err)
		}
		for _, r := range rows {
			t := SOTagAggregation{}
			if v, ok := r["tag"].(string); ok {
				t.Tag = v
			}
			t.Questions = parseBQInt(r["c"])
			out.UserExpertise = append(out.UserExpertise, t)
		}
		out.TotalReturned = len(out.UserExpertise)

	case "tag_top_users":
		// Top reputation users who answered in this tag
		sql := fmt.Sprintf(`
SELECT u.id, u.display_name, u.reputation, u.location,
       COUNT(DISTINCT a.id) AS answers_in_tag
FROM `+"`bigquery-public-data.stackoverflow.users`"+` u
JOIN `+"`bigquery-public-data.stackoverflow.posts_answers`"+` a ON a.owner_user_id = u.id
JOIN `+"`bigquery-public-data.stackoverflow.posts_questions`"+` q ON q.id = a.parent_id
WHERE q.tags LIKE '%%<%s>%%'
GROUP BY u.id, u.display_name, u.reputation, u.location
ORDER BY answers_in_tag DESC LIMIT %d`, strings.ToLower(safeQ), limit)
		rows, err := bqQuery(ctx, sql, limit)
		if err != nil {
			return nil, fmt.Errorf("tag top users: %w", err)
		}
		for _, r := range rows {
			u := SOUser{}
			u.ID = parseBQInt(r["id"])
			if v, ok := r["display_name"].(string); ok {
				u.DisplayName = v
			}
			u.Reputation = parseBQInt(r["reputation"])
			if v, ok := r["location"].(string); ok {
				u.Location = v
			}
			u.UpVotes = parseBQInt(r["answers_in_tag"]) // overload field
			u.URL = fmt.Sprintf("https://stackoverflow.com/users/%d", u.ID)
			out.Users = append(out.Users, u)
		}
		out.TotalReturned = len(out.Users)

	case "keyword_search":
		sql := fmt.Sprintf(`
SELECT id, title, tags, score, view_count, answer_count, creation_date, owner_user_id
FROM `+"`bigquery-public-data.stackoverflow.posts_questions`"+`
WHERE LOWER(title) LIKE '%%%s%%'
ORDER BY score DESC LIMIT %d`, strings.ToLower(safeQ), limit)
		rows, err := bqQuery(ctx, sql, limit)
		if err != nil {
			return nil, fmt.Errorf("keyword search: %w", err)
		}
		for _, r := range rows {
			q := SOQuestion{}
			q.ID = parseBQInt(r["id"])
			if v, ok := r["title"].(string); ok {
				q.Title = v
			}
			if v, ok := r["tags"].(string); ok {
				q.Tags = strings.ReplaceAll(strings.ReplaceAll(v, "<", ""), ">", " ")
			}
			q.Score = parseBQInt(r["score"])
			q.ViewCount = parseBQInt(r["view_count"])
			q.AnswerCount = parseBQInt(r["answer_count"])
			if v, ok := r["creation_date"].(string); ok {
				q.CreationDate = v
			}
			q.OwnerUserID = parseBQInt(r["owner_user_id"])
			q.URL = fmt.Sprintf("https://stackoverflow.com/questions/%d", q.ID)
			out.Questions = append(out.Questions, q)
		}
		out.TotalReturned = len(out.Questions)

	default:
		return nil, fmt.Errorf("unknown mode '%s' — use user_search, user_expertise, tag_top_users, or keyword_search", mode)
	}

	// Highlights
	highlights := []string{
		fmt.Sprintf("%d results in mode=%s", out.TotalReturned, mode),
	}
	if len(out.Users) > 0 && (mode == "user_search" || mode == "tag_top_users") {
		top := out.Users[0]
		extra := ""
		if mode == "tag_top_users" {
			extra = fmt.Sprintf(" (%d answers in tag)", top.UpVotes)
		}
		highlights = append(highlights, fmt.Sprintf("top: %s — rep=%d, loc='%s'%s",
			top.DisplayName, top.Reputation, top.Location, extra))
		if mode == "user_search" {
			highlights = append(highlights, fmt.Sprintf("→ chain: bigquery_stack_overflow mode=user_expertise query=%d for their tag focus", top.ID))
		}
	}
	if len(out.UserExpertise) > 0 {
		topT := []string{}
		for i, t := range out.UserExpertise {
			if i >= 5 {
				break
			}
			topT = append(topT, fmt.Sprintf("%s(%d)", t.Tag, t.Questions))
		}
		highlights = append(highlights, "expertise tags: "+strings.Join(topT, ", "))
	}
	if len(out.Questions) > 0 {
		topQ := out.Questions[0]
		highlights = append(highlights, fmt.Sprintf("top Q: '%s' (%d↑, %d views, %d answers)",
			truncate(topQ.Title, 80), topQ.Score, topQ.ViewCount, topQ.AnswerCount))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
