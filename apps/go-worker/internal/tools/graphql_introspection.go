package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type GraphQLArg struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type GraphQLOperation struct {
	Name       string       `json:"name"`
	Args       []GraphQLArg `json:"args,omitempty"`
	ReturnType string       `json:"return_type,omitempty"`
	Deprecated bool         `json:"deprecated,omitempty"`
}

type GraphQLIntrospectionOutput struct {
	TargetURL            string             `json:"target_url"`
	IntrospectionEnabled bool               `json:"introspection_enabled"`
	Endpoint             string             `json:"endpoint_used"`
	HTTPStatus           int                `json:"http_status"`
	QueryTypeName        string             `json:"query_type_name,omitempty"`
	MutationTypeName     string             `json:"mutation_type_name,omitempty"`
	SubscriptionTypeName string             `json:"subscription_type_name,omitempty"`
	TypeCount            int                `json:"type_count"`
	QueryCount           int                `json:"query_count"`
	MutationCount        int                `json:"mutation_count"`
	SubscriptionCount    int                `json:"subscription_count"`
	Types                []string           `json:"types,omitempty"`
	Queries              []GraphQLOperation `json:"queries,omitempty"`
	Mutations            []GraphQLOperation `json:"mutations,omitempty"`
	Subscriptions        []GraphQLOperation `json:"subscriptions,omitempty"`
	ProbedEndpoints      []EndpointProbe    `json:"probed_endpoints,omitempty"`
	Source               string             `json:"source"`
	TookMs               int64              `json:"tookMs"`
	Note                 string             `json:"note,omitempty"`
}

type EndpointProbe struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	OK     bool   `json:"ok"`
}

// commonGraphQLPaths — paths we probe when only a domain is supplied.
var commonGraphQLPaths = []string{
	"/graphql", "/api/graphql", "/v1/graphql", "/v2/graphql",
	"/api/v1/graphql", "/api/v2/graphql",
	"/graphql/system", // Directus
	"/gql", "/query",
	"/.well-known/graphql",
	"/api/gql",
}

// introspectionQuery is the canonical IntrospectionQuery — same one Apollo
// Studio + GraphiQL + every GraphQL client uses. Recovers full type system,
// queries, mutations, subscriptions, directives.
const introspectionQuery = `query IntrospectionQuery {
  __schema {
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types { ...FullType }
    directives { name description args { ...InputValue } }
  }
}
fragment FullType on __Type {
  kind name description
  fields(includeDeprecated: true) {
    name description
    args { ...InputValue }
    type { ...TypeRef }
    isDeprecated deprecationReason
  }
  inputFields { ...InputValue }
  interfaces { ...TypeRef }
  enumValues(includeDeprecated: true) { name description isDeprecated deprecationReason }
  possibleTypes { ...TypeRef }
}
fragment InputValue on __InputValue {
  name description type { ...TypeRef } defaultValue
}
fragment TypeRef on __Type {
  kind name
  ofType { kind name ofType { kind name ofType { kind name ofType { kind name ofType { kind name } } } } }
}`

// GraphQLIntrospection runs the canonical IntrospectionQuery against a target
// GraphQL endpoint. If introspection is enabled (the default in many frameworks
// and frequently left on in dev/staging environments), recovers the FULL
// schema: query/mutation/subscription operations with arguments + return types,
// plus the complete type system. This is the bug-bounty / OSINT primitive that
// turns a "we found a /graphql endpoint" finding into "here's the entire API
// surface and exploitation surface in one call".
//
// Two modes:
//   - url:    explicit GraphQL endpoint URL (POST query directly there)
//   - target: bare domain → probes 11 common GraphQL paths in parallel,
//             uses the first one that responds with a valid GraphQL schema.
func GraphQLIntrospection(ctx context.Context, input map[string]any) (*GraphQLIntrospectionOutput, error) {
	rawURL, _ := input["url"].(string)
	target, _ := input["target"].(string)
	rawURL = strings.TrimSpace(rawURL)
	target = strings.TrimSpace(target)
	if rawURL == "" && target == "" {
		return nil, errors.New("input.url (explicit GraphQL endpoint) or input.target (bare domain to probe common paths) required")
	}

	start := time.Now()
	out := &GraphQLIntrospectionOutput{
		Source: "graphql introspection (canonical IntrospectionQuery)",
	}

	// Build candidate endpoint list.
	var endpoints []string
	if rawURL != "" {
		endpoints = []string{rawURL}
		out.TargetURL = rawURL
	} else {
		u, err := url.Parse("https://" + target)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("input.target invalid: %v", err)
		}
		out.TargetURL = "https://" + target
		for _, p := range commonGraphQLPaths {
			endpoints = append(endpoints, "https://"+target+p)
		}
	}

	// Probe each endpoint in parallel — first one that returns a valid schema wins.
	type probeResult struct {
		url      string
		status   int
		schema   map[string]interface{}
		err      error
	}
	resCh := make(chan probeResult, len(endpoints))
	var wg sync.WaitGroup
	for _, ep := range endpoints {
		wg.Add(1)
		go func(ep string) {
			defer wg.Done()
			schema, status, err := graphqlIntrospect(ctx, ep)
			resCh <- probeResult{url: ep, status: status, schema: schema, err: err}
		}(ep)
	}
	wg.Wait()
	close(resCh)

	// Collect probes; pick the first one with valid schema data.
	var winner *probeResult
	for r := range resCh {
		probe := r
		out.ProbedEndpoints = append(out.ProbedEndpoints, EndpointProbe{
			URL: r.url, Status: r.status, OK: r.err == nil && r.schema != nil,
		})
		if r.err == nil && r.schema != nil && winner == nil {
			winner = &probe
		}
	}
	sort.Slice(out.ProbedEndpoints, func(i, j int) bool {
		return out.ProbedEndpoints[i].URL < out.ProbedEndpoints[j].URL
	})

	if winner == nil {
		out.Note = "no endpoint returned a valid GraphQL schema. Either GraphQL isn't deployed at any common path, or introspection is disabled (try clairvoyance-style field-suggestion attack — coming in next iteration)."
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	out.Endpoint = winner.url
	out.HTTPStatus = winner.status
	out.IntrospectionEnabled = true
	parseSchema(winner.schema, out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// graphqlIntrospect POSTs the canonical IntrospectionQuery and returns the
// raw __schema data on success. Returns (nil, status, err) when the endpoint
// either doesn't speak GraphQL or has introspection disabled.
func graphqlIntrospect(ctx context.Context, endpoint string) (map[string]interface{}, int, error) {
	body, _ := json.Marshal(map[string]string{"query": introspectionQuery})
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, resp.StatusCode, fmt.Errorf("status %d", resp.StatusCode)
	}
	var parsed struct {
		Data   map[string]interface{} `json:"data"`
		Errors []map[string]interface{} `json:"errors"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("not JSON: %w", err)
	}
	if parsed.Data == nil {
		// Some sites return `errors:[{...}]` indicating introspection blocked.
		return nil, resp.StatusCode, fmt.Errorf("no data field in response")
	}
	schema, ok := parsed.Data["__schema"].(map[string]interface{})
	if !ok {
		return nil, resp.StatusCode, fmt.Errorf("response had no __schema field")
	}
	return schema, resp.StatusCode, nil
}

// parseSchema extracts query/mutation/subscription operations + type list from
// the introspection response.
func parseSchema(schema map[string]interface{}, out *GraphQLIntrospectionOutput) {
	getName := func(typeRef map[string]interface{}) string {
		if typeRef == nil {
			return ""
		}
		if n, ok := typeRef["name"].(string); ok && n != "" {
			return n
		}
		return ""
	}
	flattenType := func(t map[string]interface{}) string {
		if t == nil {
			return ""
		}
		// Walk wrappers (NON_NULL / LIST) to the named base type.
		var parts []string
		curr := t
		for curr != nil {
			kind, _ := curr["kind"].(string)
			name, _ := curr["name"].(string)
			if name != "" {
				parts = append(parts, name)
				return strings.Join(parts, "")
			}
			if kind == "NON_NULL" {
				parts = append(parts, "!")
			} else if kind == "LIST" {
				parts = append(parts, "[]")
			}
			of, _ := curr["ofType"].(map[string]interface{})
			curr = of
		}
		return ""
	}

	if qt, ok := schema["queryType"].(map[string]interface{}); ok {
		out.QueryTypeName = getName(qt)
	}
	if mt, ok := schema["mutationType"].(map[string]interface{}); ok {
		out.MutationTypeName = getName(mt)
	}
	if st, ok := schema["subscriptionType"].(map[string]interface{}); ok {
		out.SubscriptionTypeName = getName(st)
	}

	types, _ := schema["types"].([]interface{})
	out.TypeCount = len(types)

	extractOps := func(typeName string) []GraphQLOperation {
		var ops []GraphQLOperation
		for _, t := range types {
			tm, _ := t.(map[string]interface{})
			if tm == nil || tm["name"] != typeName {
				continue
			}
			fields, _ := tm["fields"].([]interface{})
			for _, f := range fields {
				fm, _ := f.(map[string]interface{})
				if fm == nil {
					continue
				}
				op := GraphQLOperation{
					Name:       getStr(fm, "name"),
					ReturnType: flattenType(asMap(fm["type"])),
				}
				if dep, _ := fm["isDeprecated"].(bool); dep {
					op.Deprecated = true
				}
				args, _ := fm["args"].([]interface{})
				for _, a := range args {
					am, _ := a.(map[string]interface{})
					if am == nil {
						continue
					}
					op.Args = append(op.Args, GraphQLArg{
						Name:        getStr(am, "name"),
						Type:        flattenType(asMap(am["type"])),
						Description: truncate(getStr(am, "description"), 120),
					})
				}
				ops = append(ops, op)
			}
		}
		return ops
	}

	if out.QueryTypeName != "" {
		out.Queries = extractOps(out.QueryTypeName)
		out.QueryCount = len(out.Queries)
	}
	if out.MutationTypeName != "" {
		out.Mutations = extractOps(out.MutationTypeName)
		out.MutationCount = len(out.Mutations)
	}
	if out.SubscriptionTypeName != "" {
		out.Subscriptions = extractOps(out.SubscriptionTypeName)
		out.SubscriptionCount = len(out.Subscriptions)
	}

	// Top-level type list (skip introspection-internal __* and scalars).
	for _, t := range types {
		tm, _ := t.(map[string]interface{})
		name := getStr(tm, "name")
		if name == "" || strings.HasPrefix(name, "__") {
			continue
		}
		out.Types = append(out.Types, name)
	}
	sort.Strings(out.Types)
}

func getStr(m map[string]interface{}, k string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[k].(string); ok {
		return s
	}
	return ""
}
func asMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}
