package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type SEUser struct {
	UserID       int    `json:"user_id"`
	DisplayName  string `json:"display_name"`
	Site         string `json:"site,omitempty"`
	Reputation   int    `json:"reputation"`
	CreationDate int64  `json:"creation_date"`
	LastAccess   int64  `json:"last_access_date,omitempty"`
	Location     string `json:"location,omitempty"`
	Link         string `json:"link"`
	Bio          string `json:"about_me,omitempty"`
	WebsiteURL   string `json:"website_url,omitempty"`
}

type SEUserOutput struct {
	Query   string   `json:"query"`
	Sites   []string `json:"sites_searched"`
	Users   []SEUser `json:"users"`
	Count   int      `json:"count"`
	TookMs  int64    `json:"tookMs"`
	Source  string   `json:"source"`
	Note    string   `json:"note,omitempty"`
}

// stackExchangeSites — searched in parallel.  These are the highest-traffic
// sites in the SE network; covering them catches the bulk of OSINT-relevant
// users without hitting the per-IP daily quota.
var stackExchangeSites = []string{
	"stackoverflow",  // largest
	"superuser",
	"serverfault",
	"askubuntu",
	"unix",           // unix.stackexchange.com
	"security",       // security.stackexchange.com
	"crypto",
	"reverseengineering",
	"meta",           // meta.stackexchange.com
}

// StackExchangeUser searches the Stack Exchange Network across the most
// populated sites in parallel for a username (display-name match).
// Free, 300 requests/day unauthenticated. STACKEXCHANGE_API_KEY env var
// (free) raises the quota to 10,000/day.
func StackExchangeUser(ctx context.Context, input map[string]any) (*SEUserOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (display name to search)")
	}
	limitPerSite := 5
	if v, ok := input["limit_per_site"].(float64); ok && v > 0 {
		limitPerSite = int(v)
	}
	sites := stackExchangeSites
	if v, ok := input["sites"].([]interface{}); ok && len(v) > 0 {
		sites = nil
		for _, s := range v {
			if str, ok := s.(string); ok {
				sites = append(sites, str)
			}
		}
	}
	apiKey := os.Getenv("STACKEXCHANGE_API_KEY")

	start := time.Now()
	type siteResult struct {
		site  string
		users []SEUser
		err   error
	}
	resCh := make(chan siteResult, len(sites))
	for _, site := range sites {
		go func(site string) {
			users, err := seQuerySite(ctx, site, q, limitPerSite, apiKey)
			resCh <- siteResult{site: site, users: users, err: err}
		}(site)
	}

	out := &SEUserOutput{
		Query:  q,
		Sites:  sites,
		Source: "api.stackexchange.com",
	}
	for range sites {
		r := <-resCh
		if r.err != nil {
			continue // partial-result tolerance: a single site failure shouldn't kill the whole query
		}
		out.Users = append(out.Users, r.users...)
	}
	out.Count = len(out.Users)
	if apiKey == "" {
		out.Note = "unauthenticated (300 req/day cap). Set STACKEXCHANGE_API_KEY (free) for 10,000/day."
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func seQuerySite(ctx context.Context, site, q string, limit int, apiKey string) ([]SEUser, error) {
	args := url.Values{}
	args.Set("inname", q)
	args.Set("site", site)
	args.Set("pagesize", fmt.Sprint(limit))
	args.Set("filter", "!9_bDDxJY5") // includes about_me, website_url, location, last_access_date
	if apiKey != "" {
		args.Set("key", apiKey)
	}
	endpoint := "https://api.stackexchange.com/2.3/users?" + args.Encode()
	body, err := httpGetJSON(ctx, endpoint, 10*time.Second)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []SEUser `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Items {
		resp.Items[i].Site = site
	}
	return resp.Items, nil
}
