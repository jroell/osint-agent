package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type MastodonAccount struct {
	Instance       string `json:"instance"`
	ID             string `json:"id"`
	Username       string `json:"username"`
	Acct           string `json:"acct"`
	DisplayName    string `json:"display_name,omitempty"`
	Note           string `json:"note,omitempty"`
	URL            string `json:"url,omitempty"`
	AvatarURL      string `json:"avatar_url,omitempty"`
	FollowersCount int    `json:"followers_count,omitempty"`
	FollowingCount int    `json:"following_count,omitempty"`
	StatusesCount  int    `json:"statuses_count,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	Bot            bool   `json:"bot,omitempty"`
}

type MastodonOutput struct {
	Query             string            `json:"query"`
	InstancesQueried  []string          `json:"instances_queried"`
	InstancesResponded []string         `json:"instances_responded"`
	Matches           []MastodonAccount `json:"matches"`
	UniqueAccounts    int               `json:"unique_accounts"`
	TookMs            int64             `json:"tookMs"`
	Source            string            `json:"source"`
	Note              string            `json:"note,omitempty"`
}

// defaultMastodonInstances — the 5 highest-traffic instances, intentionally
// covering different communities (general, tech, infosec) so a single
// instance being slow/blocked still leaves useful coverage. Caller can
// override with the `instances` param.
var defaultMastodonInstances = []string{
	"mastodon.social",   // largest general-purpose
	"hachyderm.io",      // tech community
	"infosec.exchange",  // security professionals
	"fosstodon.org",     // OSS / linux
	"mas.to",            // general, large
}

// MastodonUserLookup queries multiple Mastodon instances in parallel for a
// username. Instances are independent — if mastodon.social is rate-limiting,
// the other 4 still respond. Returns the aggregated, deduplicated set of
// accounts found anywhere on the federated network.
//
// This is the textbook example of "fallback for flaky services": federation
// MEANS no single point of failure if you actually use multiple sources.
func MastodonUserLookup(ctx context.Context, input map[string]any) (*MastodonOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(strings.TrimPrefix(q, "@"))
	if q == "" {
		return nil, errors.New("input.query required (username, with or without @, e.g. \"alice\" or \"alice@example.org\")")
	}
	instances := defaultMastodonInstances
	if v, ok := input["instances"].([]interface{}); ok && len(v) > 0 {
		instances = nil
		for _, s := range v {
			if str, ok := s.(string); ok {
				instances = append(instances, str)
			}
		}
	}
	perInstanceTimeoutS := 6
	if v, ok := input["per_instance_timeout_s"].(float64); ok && v > 0 {
		perInstanceTimeoutS = int(v)
	}

	start := time.Now()
	out := &MastodonOutput{
		Query:            q,
		InstancesQueried: instances,
		Source:           "federated mastodon (multi-instance)",
	}

	type result struct {
		instance string
		accounts []MastodonAccount
		err      error
	}
	resCh := make(chan result, len(instances))
	var wg sync.WaitGroup
	for _, instance := range instances {
		wg.Add(1)
		go func(instance string) {
			defer wg.Done()
			accs, err := mastodonSearchInstance(ctx, instance, q, perInstanceTimeoutS)
			resCh <- result{instance, accs, err}
		}(instance)
	}
	wg.Wait()
	close(resCh)

	seen := map[string]struct{}{}
	for r := range resCh {
		if r.err != nil || len(r.accounts) == 0 {
			continue
		}
		out.InstancesResponded = append(out.InstancesResponded, r.instance)
		for _, a := range r.accounts {
			// Dedupe by canonical acct (user@host).
			key := a.URL
			if key == "" {
				key = fmt.Sprintf("%s@%s", a.Username, r.instance)
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			a.Instance = r.instance
			out.Matches = append(out.Matches, a)
		}
	}
	sort.Slice(out.Matches, func(i, j int) bool {
		return out.Matches[i].FollowersCount > out.Matches[j].FollowersCount
	})
	out.UniqueAccounts = len(out.Matches)
	if len(out.InstancesResponded) < len(instances) {
		out.Note = fmt.Sprintf("%d/%d instances responded (the rest timed out or rate-limited — federation makes this graceful)", len(out.InstancesResponded), len(instances))
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func mastodonSearchInstance(ctx context.Context, instance, q string, timeoutS int) ([]MastodonAccount, error) {
	endpoint := fmt.Sprintf("https://%s/api/v2/search?q=%s&type=accounts&limit=10",
		instance, url.QueryEscape(q))
	body, err := httpGetJSON(ctx, endpoint, time.Duration(timeoutS)*time.Second)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Accounts []struct {
			ID             string `json:"id"`
			Username       string `json:"username"`
			Acct           string `json:"acct"`
			DisplayName    string `json:"display_name"`
			Note           string `json:"note"`
			URL            string `json:"url"`
			Avatar         string `json:"avatar"`
			FollowersCount int    `json:"followers_count"`
			FollowingCount int    `json:"following_count"`
			StatusesCount  int    `json:"statuses_count"`
			CreatedAt      string `json:"created_at"`
			Bot            bool   `json:"bot"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out := make([]MastodonAccount, 0, len(resp.Accounts))
	for _, a := range resp.Accounts {
		out = append(out, MastodonAccount{
			ID: a.ID, Username: a.Username, Acct: a.Acct, DisplayName: a.DisplayName,
			Note: stripHTML(a.Note, 280), URL: a.URL, AvatarURL: a.Avatar,
			FollowersCount: a.FollowersCount, FollowingCount: a.FollowingCount,
			StatusesCount: a.StatusesCount, CreatedAt: a.CreatedAt, Bot: a.Bot,
		})
	}
	return out, nil
}
