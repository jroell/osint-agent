package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type GHArchiveEvent struct {
	Type      string `json:"type"`
	Actor     string `json:"actor"`
	Repo      string `json:"repo"`
	Org       string `json:"org,omitempty"`
	CreatedAt string `json:"created_at"`
}

type GHArchiveAggCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type BQGHArchiveOutput struct {
	Mode               string              `json:"mode"`
	Target             string              `json:"target"`
	StartDate          string              `json:"start_date"`
	EndDate            string              `json:"end_date"`
	TotalEvents        int                 `json:"total_events"`
	EventsByType       []GHArchiveAggCount `json:"events_by_type,omitempty"`
	TopActors          []GHArchiveAggCount `json:"top_actors,omitempty"`
	TopRepos           []GHArchiveAggCount `json:"top_repos,omitempty"`
	ShadowContributors []string            `json:"top_pushers_for_shadow_check,omitempty"`
	RecentEvents       []GHArchiveEvent    `json:"recent_events,omitempty"`
	HighlightFindings  []string            `json:"highlight_findings"`
	Source             string              `json:"source"`
	TookMs             int64               `json:"tookMs"`
	Note               string              `json:"note,omitempty"`
}

// monthsCovering returns YYYYMM strings for every month between start and end (inclusive).
func monthsCovering(start, end time.Time) []string {
	out := []string{}
	y, m, _ := start.Date()
	cur := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	endMonth := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !cur.After(endMonth) {
		out = append(out, cur.Format("200601"))
		cur = cur.AddDate(0, 1, 0)
	}
	return out
}

func tableUnion(months []string) string {
	if len(months) == 1 {
		return fmt.Sprintf("`githubarchive.month.%s`", months[0])
	}
	parts := make([]string, len(months))
	for i, m := range months {
		parts[i] = fmt.Sprintf("SELECT * FROM `githubarchive.month.%s`", m)
	}
	return "(" + strings.Join(parts, " UNION ALL ") + ")"
}

// BigQueryGitHubArchive queries the GH Archive on BigQuery — every public
// GitHub event since 2011. Uses month-partitioned tables (githubarchive.month.YYYYMM)
// to avoid view-conflict issues with day.* wildcard expansion.
//
// Modes:
//   - "org_activity": all events for an org over a date range
//   - "user_activity": all events by a specific user
//   - "repo_history": all events on a specific repo (target='owner/repo')
//   - "shadow_contributors": top pushers/PR-authors for cross-ref vs github_org_intel.public_members
func BigQueryGitHubArchive(ctx context.Context, input map[string]any) (*BQGHArchiveOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "org_activity"
	}
	target, _ := input["target"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("input.target required")
	}
	safeTarget := strings.ReplaceAll(target, "'", "")
	safeTarget = strings.ReplaceAll(safeTarget, "\\", "")

	daysBack := 7
	if v, ok := input["days_back"].(float64); ok && int(v) > 0 && int(v) <= 90 {
		daysBack = int(v)
	}
	limit := 50
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 500 {
		limit = int(v)
	}

	now := time.Now().UTC()
	endDate := now.AddDate(0, 0, -1)
	startDate := endDate.AddDate(0, 0, -(daysBack - 1))
	tableExpr := tableUnion(monthsCovering(startDate, endDate))
	timeFilter := fmt.Sprintf("created_at >= TIMESTAMP('%s 00:00:00') AND created_at <= TIMESTAMP('%s 23:59:59')",
		startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))

	start := time.Now()
	out := &BQGHArchiveOutput{
		Mode: mode, Target: target,
		StartDate: startDate.Format("2006-01-02"),
		EndDate:   endDate.Format("2006-01-02"),
		Source:    "githubarchive on BigQuery",
	}

	switch mode {
	case "org_activity":
		aggSQL := fmt.Sprintf(`
SELECT type, COUNT(*) AS c
FROM %s
WHERE %s
  AND (org.login = '%s' OR repo.name LIKE '%s/%%')
GROUP BY type ORDER BY c DESC LIMIT 20`, tableExpr, timeFilter, safeTarget, safeTarget)
		rows, err := bqQuery(ctx, aggSQL, 20)
		if err != nil {
			return nil, fmt.Errorf("agg query: %w", err)
		}
		for _, r := range rows {
			a := GHArchiveAggCount{}
			if v, ok := r["type"].(string); ok {
				a.Key = v
			}
			a.Count = parseBQInt(r["c"])
			out.EventsByType = append(out.EventsByType, a)
			out.TotalEvents += a.Count
		}
		actorsSQL := fmt.Sprintf(`
SELECT actor.login AS actor, COUNT(*) AS c
FROM %s
WHERE %s
  AND (org.login = '%s' OR repo.name LIKE '%s/%%')
  AND actor.login NOT LIKE '%%[bot]'
GROUP BY actor ORDER BY c DESC LIMIT %d`, tableExpr, timeFilter, safeTarget, safeTarget, limit)
		arows, err := bqQuery(ctx, actorsSQL, limit)
		if err == nil {
			for _, r := range arows {
				a := GHArchiveAggCount{}
				if v, ok := r["actor"].(string); ok {
					a.Key = v
				}
				a.Count = parseBQInt(r["c"])
				out.TopActors = append(out.TopActors, a)
			}
		}
		reposSQL := fmt.Sprintf(`
SELECT repo.name AS repo, COUNT(*) AS c
FROM %s
WHERE %s
  AND (org.login = '%s' OR repo.name LIKE '%s/%%')
GROUP BY repo ORDER BY c DESC LIMIT 15`, tableExpr, timeFilter, safeTarget, safeTarget)
		rrows, err := bqQuery(ctx, reposSQL, 15)
		if err == nil {
			for _, r := range rrows {
				a := GHArchiveAggCount{}
				if v, ok := r["repo"].(string); ok {
					a.Key = v
				}
				a.Count = parseBQInt(r["c"])
				out.TopRepos = append(out.TopRepos, a)
			}
		}

	case "user_activity":
		actSQL := fmt.Sprintf(`
SELECT type, COUNT(*) AS c
FROM %s
WHERE %s AND actor.login = '%s'
GROUP BY type ORDER BY c DESC LIMIT 20`, tableExpr, timeFilter, safeTarget)
		rows, err := bqQuery(ctx, actSQL, 20)
		if err != nil {
			return nil, fmt.Errorf("user activity: %w", err)
		}
		for _, r := range rows {
			a := GHArchiveAggCount{}
			if v, ok := r["type"].(string); ok {
				a.Key = v
			}
			a.Count = parseBQInt(r["c"])
			out.EventsByType = append(out.EventsByType, a)
			out.TotalEvents += a.Count
		}
		urSQL := fmt.Sprintf(`
SELECT repo.name AS repo, COUNT(*) AS c
FROM %s
WHERE %s AND actor.login = '%s'
GROUP BY repo ORDER BY c DESC LIMIT 20`, tableExpr, timeFilter, safeTarget)
		urrows, _ := bqQuery(ctx, urSQL, 20)
		for _, r := range urrows {
			a := GHArchiveAggCount{}
			if v, ok := r["repo"].(string); ok {
				a.Key = v
			}
			a.Count = parseBQInt(r["c"])
			out.TopRepos = append(out.TopRepos, a)
		}

	case "repo_history":
		if !strings.Contains(target, "/") {
			return nil, errors.New("for mode='repo_history', target must be 'owner/repo'")
		}
		rhSQL := fmt.Sprintf(`
SELECT type, COUNT(*) AS c
FROM %s
WHERE %s AND repo.name = '%s'
GROUP BY type ORDER BY c DESC LIMIT 20`, tableExpr, timeFilter, safeTarget)
		rows, err := bqQuery(ctx, rhSQL, 20)
		if err != nil {
			return nil, fmt.Errorf("repo history: %w", err)
		}
		for _, r := range rows {
			a := GHArchiveAggCount{}
			if v, ok := r["type"].(string); ok {
				a.Key = v
			}
			a.Count = parseBQInt(r["c"])
			out.EventsByType = append(out.EventsByType, a)
			out.TotalEvents += a.Count
		}
		conSQL := fmt.Sprintf(`
SELECT actor.login AS actor, COUNT(*) AS c
FROM %s
WHERE %s AND repo.name = '%s' AND actor.login NOT LIKE '%%[bot]'
GROUP BY actor ORDER BY c DESC LIMIT %d`, tableExpr, timeFilter, safeTarget, limit)
		cnrows, _ := bqQuery(ctx, conSQL, limit)
		for _, r := range cnrows {
			a := GHArchiveAggCount{}
			if v, ok := r["actor"].(string); ok {
				a.Key = v
			}
			a.Count = parseBQInt(r["c"])
			out.TopActors = append(out.TopActors, a)
		}

	case "shadow_contributors":
		shSQL := fmt.Sprintf(`
SELECT actor.login AS actor, COUNT(*) AS c
FROM %s
WHERE %s
  AND (org.login = '%s' OR repo.name LIKE '%s/%%')
  AND type IN ('PushEvent','PullRequestEvent')
  AND actor.login NOT LIKE '%%[bot]'
GROUP BY actor ORDER BY c DESC LIMIT %d`, tableExpr, timeFilter, safeTarget, safeTarget, limit)
		rows, err := bqQuery(ctx, shSQL, limit)
		if err != nil {
			return nil, fmt.Errorf("shadow contributors: %w", err)
		}
		for _, r := range rows {
			a := GHArchiveAggCount{}
			if v, ok := r["actor"].(string); ok {
				a.Key = v
				out.ShadowContributors = append(out.ShadowContributors, v)
			}
			a.Count = parseBQInt(r["c"])
			out.TopActors = append(out.TopActors, a)
			out.TotalEvents += a.Count
		}
		out.Note = "Top pushers/PR-authors. Cross-reference with github_org_intel's public_members list — anyone here NOT in that list is a 'shadow contributor' (employee/contractor/external who isn't publicly attributed)."

	default:
		return nil, fmt.Errorf("unknown mode '%s' — try org_activity, user_activity, repo_history, or shadow_contributors", mode)
	}

	highlights := []string{}
	highlights = append(highlights, fmt.Sprintf("%d events for '%s' across %s → %s", out.TotalEvents, target, out.StartDate, out.EndDate))
	if len(out.EventsByType) > 0 {
		ts := []string{}
		for i, e := range out.EventsByType {
			if i >= 4 {
				break
			}
			ts = append(ts, fmt.Sprintf("%s=%d", e.Key, e.Count))
		}
		highlights = append(highlights, "events by type: "+strings.Join(ts, ", "))
	}
	if len(out.TopActors) > 0 {
		as := []string{}
		for i, a := range out.TopActors {
			if i >= 5 {
				break
			}
			as = append(as, fmt.Sprintf("%s(%d)", a.Key, a.Count))
		}
		highlights = append(highlights, "top contributors: "+strings.Join(as, ", "))
	}
	if len(out.TopRepos) > 0 {
		rs := []string{}
		for i, r := range out.TopRepos {
			if i >= 5 {
				break
			}
			rs = append(rs, fmt.Sprintf("%s(%d)", r.Key, r.Count))
		}
		highlights = append(highlights, "top repos: "+strings.Join(rs, ", "))
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func parseBQInt(v any) int {
	if s, ok := v.(string); ok {
		var n int
		fmt.Sscanf(s, "%d", &n)
		return n
	}
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}
