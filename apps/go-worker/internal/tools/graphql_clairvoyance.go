package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type ClairvoyanceField struct {
	Name        string   `json:"name"`
	DiscoveredFrom string `json:"discovered_from"`     // the bait field that elicited this suggestion
	OnType      string   `json:"on_type,omitempty"`    // type the field belongs to (e.g. "Query")
}

type GraphQLClairvoyanceOutput struct {
	TargetURL          string              `json:"target_url"`
	Endpoint           string              `json:"endpoint_used"`
	HTTPStatus         int                 `json:"http_status"`
	IntrospectionFirst bool                `json:"tried_introspection_first"`
	IntrospectionOpen  bool                `json:"introspection_was_open"`
	BaitsTotal         int                 `json:"baits_attempted"`
	HitsTotal          int                 `json:"suggestion_hits"`
	QueryFields        []ClairvoyanceField `json:"query_fields"`
	MutationFields     []ClairvoyanceField `json:"mutation_fields"`
	UniqueTypes        []string            `json:"unique_types_observed"`
	SampleErrors       []string            `json:"sample_errors,omitempty"`
	Source             string              `json:"source"`
	TookMs             int64               `json:"tookMs"`
	Note               string              `json:"note,omitempty"`
}

// Curated wordlist — common API field names that flush out the most schema
// fragments per probe. Skewed toward identity/auth/admin paths since those
// are the highest-value ER signals.
var clairvoyanceBaitFields = []string{
	// Identity & users
	"user", "users", "userById", "currentUser", "me", "viewer", "account", "accounts",
	"profile", "profiles", "member", "members", "person", "people", "team", "teams",
	"organization", "organizations", "tenant", "tenants", "workspace", "workspaces",
	// Auth
	"login", "logout", "register", "signup", "signin", "auth", "session", "token",
	"refreshToken", "apiKey", "apiKeys", "secret", "credential", "credentials",
	// Admin / internal
	"admin", "adminUser", "internal", "system", "config", "settings", "feature",
	"featureFlag", "audit", "log", "logs", "event", "events", "metric", "metrics",
	// Data
	"post", "posts", "comment", "comments", "message", "messages", "thread", "threads",
	"channel", "channels", "notification", "notifications", "search", "query",
	"document", "documents", "file", "files", "asset", "assets", "media",
	// Commerce
	"product", "products", "order", "orders", "cart", "checkout", "payment", "payments",
	"invoice", "invoices", "subscription", "subscriptions", "plan", "plans",
	"transaction", "transactions", "refund", "discount", "coupon",
	// Content / CMS
	"page", "pages", "article", "articles", "post", "category", "categories", "tag", "tags",
	// Survey/research (vurvey-relevant)
	"survey", "surveys", "response", "responses", "question", "questions", "answer", "answers",
	"insight", "insights", "report", "reports", "analytics", "dashboard", "study", "studies",
	"interview", "interviews", "video", "videos", "transcript", "transcripts",
	// Misc useful
	"node", "nodes", "edges", "list", "create", "update", "delete", "remove",
	"export", "import", "backup", "restore", "clone", "duplicate",
	"webhook", "webhooks", "integration", "integrations", "connector", "connectors",
}

// Apollo: `Cannot query field "foo" on type "Query". Did you mean "users", "user", or "viewer"?`
// Hot Chocolate: `The field 'foo' does not exist on the type 'Query'. Did you mean 'users'?`
// Some servers: `Did you mean "X"?`
var didYouMeanRE = regexp.MustCompile(`(?i)did you mean\s+([^?]+?)\?`)
var fieldNameRE = regexp.MustCompile(`["'\x60]([A-Za-z_][A-Za-z0-9_]*)["'\x60]`)

// Apollo & friends: `Cannot query field "foo" on type "Query"`
var onTypeRE = regexp.MustCompile(`(?i)(?:on type|on the type)\s+["'\x60]?([A-Za-z_][A-Za-z0-9_]*)["'\x60]?`)

// GraphQLClairvoyance attempts to recover GraphQL schema fragments when
// introspection has been disabled, by abusing the "Did you mean X?" field-
// suggestion error messages most servers leak by default.
//
// Algorithm:
//  1. (Optional) Try real introspection first — if open, recommend that tool.
//  2. For each bait field in the wordlist, send `query { <bait> }` and
//     `mutation { <bait> }`.
//  3. Parse error response for "Did you mean X, Y, or Z?" hints.
//  4. Aggregate discovered field names + the types they belong to.
//
// Reference: github.com/nikitastupin/clairvoyance — canonical bug-bounty
// SOTA when introspection is locked but error suggestions are not.
//
// Cost: O(N) HTTP requests where N = wordlist size. We probe in parallel
// with concurrency 8 to stay polite.
func GraphQLClairvoyance(ctx context.Context, input map[string]any) (*GraphQLClairvoyanceOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("input.target required (URL or domain)")
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	endpoint := target
	if !strings.Contains(strings.TrimPrefix(strings.TrimPrefix(target, "https://"), "http://"), "/") {
		// Bare host — assume /graphql.
		endpoint = strings.TrimRight(target, "/") + "/graphql"
	}

	// Custom wordlist?
	wordlist := clairvoyanceBaitFields
	if v, ok := input["wordlist"].([]any); ok && len(v) > 0 {
		custom := make([]string, 0, len(v))
		for _, w := range v {
			if s, ok := w.(string); ok && s != "" {
				custom = append(custom, s)
			}
		}
		if len(custom) > 0 {
			wordlist = custom
		}
	}
	maxBaits := 200
	if v, ok := input["max_baits"].(float64); ok && int(v) > 0 {
		maxBaits = int(v)
	}
	if len(wordlist) > maxBaits {
		wordlist = wordlist[:maxBaits]
	}
	probeMutations := true
	if v, ok := input["probe_mutations"].(bool); ok {
		probeMutations = v
	}

	start := time.Now()

	// 1. Try introspection first (informational).
	tryIntrospect := true
	if v, ok := input["skip_introspection_check"].(bool); ok && v {
		tryIntrospect = false
	}
	out := &GraphQLClairvoyanceOutput{
		TargetURL: target, Endpoint: endpoint,
		IntrospectionFirst: tryIntrospect,
		Source: "graphql_clairvoyance",
	}
	if tryIntrospect {
		_, _, err := graphqlIntrospect(ctx, endpoint)
		if err == nil {
			out.IntrospectionOpen = true
			out.Note = "Introspection is OPEN — use graphql_introspection for full schema, this tool isn't needed"
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
	}

	// 2. Concurrent bait probing.
	type result struct {
		bait     string
		opType   string // "query" | "mutation"
		body     []byte
		status   int
		err      error
	}
	jobs := make(chan struct {
		bait, opType string
	})
	results := make(chan result)
	var wg sync.WaitGroup
	const concurrency = 8
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				body, status, err := clairvoyancePost(ctx, endpoint, job.opType, job.bait)
				results <- result{bait: job.bait, opType: job.opType, body: body, status: status, err: err}
			}
		}()
	}
	go func() {
		for _, b := range wordlist {
			jobs <- struct{ bait, opType string }{b, "query"}
			if probeMutations {
				jobs <- struct{ bait, opType string }{b, "mutation"}
			}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	// 3. Aggregate suggestions.
	queryFields := map[string]ClairvoyanceField{}
	mutationFields := map[string]ClairvoyanceField{}
	typesSeen := map[string]bool{}
	var sampleErrors []string
	baitCount := 0
	httpStatus := 0
	for r := range results {
		baitCount++
		if r.err != nil || r.status == 0 {
			continue
		}
		if httpStatus == 0 {
			httpStatus = r.status
		}
		errText, onType := extractErrorText(r.body)
		if errText == "" {
			continue
		}
		if onType != "" {
			typesSeen[onType] = true
		}
		// Find "Did you mean ...?" suggestions
		hints := didYouMeanRE.FindAllStringSubmatch(errText, -1)
		for _, h := range hints {
			if len(h) < 2 {
				continue
			}
			for _, m := range fieldNameRE.FindAllStringSubmatch(h[1], -1) {
				name := m[1]
				if name == r.bait || name == "" {
					continue
				}
				field := ClairvoyanceField{
					Name: name, DiscoveredFrom: r.bait, OnType: onType,
				}
				if r.opType == "query" {
					if _, exists := queryFields[name]; !exists {
						queryFields[name] = field
					}
				} else {
					if _, exists := mutationFields[name]; !exists {
						mutationFields[name] = field
					}
				}
			}
		}
		// Capture a few sample errors for debugging
		if len(sampleErrors) < 3 {
			sampleErrors = append(sampleErrors, truncate(errText, 200))
		}
	}

	out.BaitsTotal = baitCount
	out.HTTPStatus = httpStatus
	out.QueryFields = sortedFields(queryFields)
	out.MutationFields = sortedFields(mutationFields)
	out.HitsTotal = len(out.QueryFields) + len(out.MutationFields)
	for t := range typesSeen {
		out.UniqueTypes = append(out.UniqueTypes, t)
	}
	sort.Strings(out.UniqueTypes)
	out.SampleErrors = sampleErrors
	out.TookMs = time.Since(start).Milliseconds()

	if out.HitsTotal == 0 {
		out.Note = "No field suggestions extracted — server may have suggestions disabled, or endpoint isn't GraphQL. Sample errors recorded for diagnosis."
	}
	return out, nil
}

func clairvoyancePost(ctx context.Context, endpoint, opType, fieldName string) ([]byte, int, error) {
	q := fmt.Sprintf("%s { %s }", opType, fieldName)
	body, _ := json.Marshal(map[string]string{"query": q})
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "osint-agent/clairvoyance")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return rb, resp.StatusCode, nil
}

// extractErrorText flattens all GraphQL `errors[].message` strings.
// Also returns the first "on type X" hint for type-attribution.
func extractErrorText(body []byte) (string, string) {
	var parsed struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", ""
	}
	var sb strings.Builder
	onType := ""
	for _, e := range parsed.Errors {
		sb.WriteString(e.Message)
		sb.WriteString(" | ")
		if onType == "" {
			if m := onTypeRE.FindStringSubmatch(e.Message); len(m) >= 2 {
				onType = m[1]
			}
		}
	}
	return sb.String(), onType
}

func sortedFields(m map[string]ClairvoyanceField) []ClairvoyanceField {
	out := make([]ClairvoyanceField, 0, len(m))
	for _, f := range m {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
