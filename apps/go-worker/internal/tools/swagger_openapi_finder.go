package tools

import (
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

type OpenAPIOperation struct {
	Method      string   `json:"method"`        // GET / POST / PUT / DELETE / PATCH
	Path        string   `json:"path"`          // /api/v1/users/{id}
	OperationID string   `json:"operation_id,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Parameters  []string `json:"parameters,omitempty"`   // [name in_query/in_path/in_body required type]
	Responses   []string `json:"responses,omitempty"`    // [200, 201, 400, ...]
	AuthRequired bool    `json:"auth_required"`         // any security requirement attached
	AuthSchemes []string `json:"auth_schemes,omitempty"` // bearer, oauth2, apiKey, etc.
	Deprecated  bool     `json:"deprecated,omitempty"`
}

type SwaggerSpecHit struct {
	URL          string             `json:"url"`
	Status       int                `json:"status"`
	SpecVersion  string             `json:"spec_version,omitempty"` // openapi 3.x or swagger 2.x
	Title        string             `json:"title,omitempty"`
	APIVersion   string             `json:"api_version,omitempty"`
	Description  string             `json:"description,omitempty"`
	Servers      []string           `json:"servers,omitempty"`
	OpsCount     int                `json:"operations_count"`
	Operations   []OpenAPIOperation `json:"operations"`
	GlobalAuth   []string           `json:"global_auth_schemes,omitempty"`
	Bytes        int                `json:"bytes"`
}

type SwaggerOpenAPIOutput struct {
	Target          string             `json:"target"`
	PathsProbed     int                `json:"paths_probed"`
	SpecsFound      int                `json:"specs_found"`
	Specs           []SwaggerSpecHit   `json:"specs"`
	UniqueOps       int                `json:"unique_operations"`
	UniquePaths     int                `json:"unique_paths"`
	HighRiskOps     []OpenAPIOperation `json:"high_risk_unauthed_ops,omitempty"` // no auth_required + suspicious paths
	ProbedURLs      []EndpointProbe    `json:"probed_urls,omitempty"`
	Source          string             `json:"source"`
	TookMs          int64              `json:"tookMs"`
	Note            string             `json:"note"`
}

// commonSwaggerPaths — well-known locations checked by every modern bug-bounty
// recon workflow. Sorted by approximate hit-rate based on Autoswagger / public
// scan datasets.
var commonSwaggerPaths = []string{
	// JSON specs
	"/swagger.json", "/openapi.json", "/openapi/v3", "/v3/api-docs",
	"/v2/api-docs", "/api-docs", "/api/swagger.json", "/api/openapi.json",
	"/api/v1/swagger.json", "/api/v2/swagger.json", "/api/v3/swagger.json",
	"/api/v1/openapi.json", "/api/v2/openapi.json", "/api/v3/openapi.json",
	"/swagger/v1/swagger.json", "/swagger/v2/swagger.json", // .NET pattern
	"/swagger/docs/v1", "/swagger/docs/v2",
	"/api/docs/swagger.json", "/api/docs/openapi.json",
	"/.well-known/openapi.json", "/.well-known/swagger.json",
	"/api/spec", "/api/spec.json", "/spec/openapi.json",
	"/openapi", "/swagger-resources", "/swagger-config.json",
	// YAML specs
	"/openapi.yaml", "/openapi.yml", "/swagger.yaml", "/swagger.yml",
	"/api/openapi.yaml", "/api/swagger.yaml",
	// Swagger UI / ReDoc / RapiDoc landing pages — useful for HTML detection
	"/swagger-ui.html", "/swagger-ui/", "/swagger/index.html",
	"/docs", "/api/docs", "/redoc", "/rapidoc",
}

// SwaggerOpenAPIFinder probes a target for exposed OpenAPI/Swagger spec
// documents. Hits any of ~35 well-known paths in parallel; for each successful
// hit, parses the spec and extracts every operation (method+path+params+auth).
// Returns deduped operations across all found specs plus a `high_risk_unauthed_ops`
// list flagging operations that have NO auth requirement attached and look like
// they handle sensitive resources (users/admin/internal/private/etc).
//
// This is the canonical "exposed-API-spec" recon primitive — when found, gives
// the agent the COMPLETE machine-readable API surface (path + method + params +
// auth + response codes) in one tool call, dramatically faster than fuzzing
// or JS extraction.
func SwaggerOpenAPIFinder(ctx context.Context, input map[string]any) (*SwaggerOpenAPIOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, errors.New("input.target required (URL or bare domain)")
	}
	// Normalize: accept "vurvey.app", "https://vurvey.app", "https://api.vurvey.app/", etc.
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	base, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid url: %v", err)
	}
	base.Path = ""
	base.RawQuery = ""
	concurrency := 10
	if v, ok := input["concurrency"].(float64); ok && v > 0 {
		concurrency = int(v)
	}

	start := time.Now()
	out := &SwaggerOpenAPIOutput{
		Target:      base.String(),
		PathsProbed: len(commonSwaggerPaths),
		Specs:       []SwaggerSpecHit{},
		Source:      "swagger_openapi_finder (probes ~35 well-known paths)",
	}

	// Probe all paths in parallel (bounded concurrency).
	type probeR struct {
		url    string
		status int
		body   []byte
		ctype  string
	}
	results := make([]probeR, 0, len(commonSwaggerPaths))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for _, p := range commonSwaggerPaths {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			full := base.String() + p
			body, status, ctype := swaggerProbe(ctx, full, 8*time.Second)
			mu.Lock()
			results = append(results, probeR{url: full, status: status, body: body, ctype: ctype})
			out.ProbedURLs = append(out.ProbedURLs, EndpointProbe{URL: full, Status: status, OK: status == 200 && len(body) > 50})
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	// Parse each successful response.
	uniqOps := map[string]struct{}{}
	uniqPaths := map[string]struct{}{}
	for _, r := range results {
		if r.status != 200 || len(r.body) < 30 {
			continue
		}
		hit, ok := parseSwaggerOrOpenAPI(r.body, r.ctype, r.url)
		if !ok {
			continue
		}
		hit.Status = r.status
		hit.URL = r.url
		hit.Bytes = len(r.body)
		out.Specs = append(out.Specs, hit)
		for _, op := range hit.Operations {
			uniqOps[op.Method+" "+op.Path] = struct{}{}
			uniqPaths[op.Path] = struct{}{}
			if !op.AuthRequired && isSuspiciousPath(op.Path) {
				out.HighRiskOps = append(out.HighRiskOps, op)
			}
		}
	}
	out.SpecsFound = len(out.Specs)
	out.UniqueOps = len(uniqOps)
	out.UniquePaths = len(uniqPaths)
	sort.Slice(out.ProbedURLs, func(i, j int) bool { return out.ProbedURLs[i].URL < out.ProbedURLs[j].URL })

	if out.SpecsFound == 0 {
		out.Note = "no OpenAPI/Swagger spec found at any well-known path. Some apps deploy them at non-standard URLs — try discovering JS-referenced spec URLs via js_endpoint_extract first."
	} else {
		out.Note = fmt.Sprintf("found %d spec(s) across %d unique paths / %d unique operations. high_risk_unauthed_ops list flags operations with no security requirement on suspicious paths (users/admin/internal/etc).", out.SpecsFound, out.UniquePaths, out.UniqueOps)
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func swaggerProbe(ctx context.Context, target string, timeout time.Duration) ([]byte, int, string) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, ""
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	req.Header.Set("Accept", "application/json,application/yaml,text/yaml,text/html,*/*")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return body, resp.StatusCode, resp.Header.Get("Content-Type")
}

// parseSwaggerOrOpenAPI handles both OpenAPI 3.x (`openapi:` field) and
// Swagger 2.x (`swagger:` field) JSON specs. YAML support deferred — most
// modern apps ship JSON.
func parseSwaggerOrOpenAPI(body []byte, ctype, url string) (SwaggerSpecHit, bool) {
	hit := SwaggerSpecHit{Operations: []OpenAPIOperation{}}
	trimmed := strings.TrimSpace(string(body))

	// YAML detection — top-level "openapi:", "swagger:", or "info:" keys.
	if strings.HasPrefix(trimmed, "openapi:") || strings.HasPrefix(trimmed, "swagger:") ||
		strings.HasPrefix(trimmed, "info:") || strings.HasPrefix(trimmed, "# OpenAPI") {
		hit.SpecVersion = "yaml-detected"
		hit.Title = "OpenAPI/Swagger YAML spec found (parser handles JSON only — but YAML is here, ready for downstream conversion)"
		// Best-effort metadata extraction via simple line patterns.
		if m := yamlGrepFirst(trimmed, "title:"); m != "" {
			hit.Title = m
		}
		if m := yamlGrepFirst(trimmed, "version:"); m != "" {
			hit.APIVersion = m
		}
		hit.OpsCount = countYAMLPaths(trimmed)
		return hit, true
	}

	if !strings.HasPrefix(trimmed, "{") {
		if strings.Contains(trimmed, "swagger-ui") || strings.Contains(trimmed, "redoc") || strings.Contains(trimmed, "rapidoc") {
			hit.SpecVersion = "html_swagger_ui"
			hit.Title = "Swagger UI / ReDoc landing page (spec URL likely embedded — check js_endpoint_extract)"
			return hit, true
		}
		return hit, false
	}

	var spec map[string]interface{}
	if err := json.Unmarshal(body, &spec); err != nil {
		return hit, false
	}

	// OpenAPI 3.x?
	if v, ok := spec["openapi"].(string); ok {
		hit.SpecVersion = "openapi-" + v
	} else if v, ok := spec["swagger"].(string); ok {
		hit.SpecVersion = "swagger-" + v
	} else {
		return hit, false
	}

	// info block
	if info, ok := spec["info"].(map[string]interface{}); ok {
		hit.Title = strFromAny(info["title"])
		hit.APIVersion = strFromAny(info["version"])
		hit.Description = truncate(strFromAny(info["description"]), 240)
	}
	// servers (OpenAPI 3) or host/basePath (Swagger 2)
	if servers, ok := spec["servers"].([]interface{}); ok {
		for _, s := range servers {
			sm, _ := s.(map[string]interface{})
			if u := strFromAny(sm["url"]); u != "" {
				hit.Servers = append(hit.Servers, u)
			}
		}
	} else if host, ok := spec["host"].(string); ok {
		schemes, _ := spec["schemes"].([]interface{})
		basePath, _ := spec["basePath"].(string)
		scheme := "https"
		if len(schemes) > 0 {
			scheme = strFromAny(schemes[0])
		}
		hit.Servers = []string{scheme + "://" + host + basePath}
	}

	// global security requirements
	if sec, ok := spec["security"].([]interface{}); ok && len(sec) > 0 {
		for _, s := range sec {
			sm, _ := s.(map[string]interface{})
			for k := range sm {
				hit.GlobalAuth = append(hit.GlobalAuth, k)
			}
		}
	}

	// paths
	paths, _ := spec["paths"].(map[string]interface{})
	for path, methods := range paths {
		mm, _ := methods.(map[string]interface{})
		for method, op := range mm {
			methodU := strings.ToUpper(method)
			if !isHTTPMethod(methodU) {
				continue
			}
			om, _ := op.(map[string]interface{})
			if om == nil {
				continue
			}
			oo := OpenAPIOperation{Method: methodU, Path: path}
			oo.OperationID = strFromAny(om["operationId"])
			oo.Summary = strFromAny(om["summary"])
			oo.Description = truncate(strFromAny(om["description"]), 200)
			if dep, _ := om["deprecated"].(bool); dep {
				oo.Deprecated = true
			}
			// tags
			if tags, ok := om["tags"].([]interface{}); ok {
				for _, t := range tags {
					if s := strFromAny(t); s != "" {
						oo.Tags = append(oo.Tags, s)
					}
				}
			}
			// parameters
			if params, ok := om["parameters"].([]interface{}); ok {
				for _, p := range params {
					pm, _ := p.(map[string]interface{})
					name := strFromAny(pm["name"])
					in := strFromAny(pm["in"])
					required, _ := pm["required"].(bool)
					reqStr := ""
					if required {
						reqStr = "*"
					}
					oo.Parameters = append(oo.Parameters, fmt.Sprintf("%s%s in %s", reqStr, name, in))
				}
			}
			// responses
			if resps, ok := om["responses"].(map[string]interface{}); ok {
				for code := range resps {
					oo.Responses = append(oo.Responses, code)
				}
				sort.Strings(oo.Responses)
			}
			// per-operation security overrides global
			if sec, ok := om["security"].([]interface{}); ok && len(sec) > 0 {
				oo.AuthRequired = true
				for _, s := range sec {
					sm, _ := s.(map[string]interface{})
					for k := range sm {
						oo.AuthSchemes = append(oo.AuthSchemes, k)
					}
				}
			} else if len(hit.GlobalAuth) > 0 {
				oo.AuthRequired = true
				oo.AuthSchemes = hit.GlobalAuth
			}
			hit.Operations = append(hit.Operations, oo)
		}
	}
	hit.OpsCount = len(hit.Operations)
	return hit, true
}

func isHTTPMethod(s string) bool {
	switch s {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD", "TRACE":
		return true
	}
	return false
}

// isSuspiciousPath heuristic for high_risk_unauthed_ops list.
func isSuspiciousPath(path string) bool {
	low := strings.ToLower(path)
	for _, p := range []string{
		"/admin", "/internal", "/private", "/users", "/user/", "/account", "/me/",
		"/auth", "/token", "/login", "/register", "/password",
		"/payment", "/billing", "/invoice", "/customer", "/order",
		"/upload", "/download", "/export", "/import",
		"/secret", "/key", "/credential", "/config", "/settings",
		"/debug", "/test", "/staging", "/dev",
	} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func strFromAny(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// yamlGrepFirst — best-effort extract of `key: value` line from YAML.
func yamlGrepFirst(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, key) {
			val := strings.TrimSpace(strings.TrimPrefix(t, key))
			val = strings.Trim(val, `"' `)
			if val != "" {
				return val
			}
		}
	}
	return ""
}

// countYAMLPaths — count entries in a YAML `paths:` block by scanning indented `/path:` keys.
func countYAMLPaths(text string) int {
	lines := strings.Split(text, "\n")
	inPaths := false
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "paths:") {
			inPaths = true
			continue
		}
		if inPaths {
			// path keys are indented 2 spaces and start with "/"
			if strings.HasPrefix(line, "  /") && strings.Contains(line, ":") && !strings.HasPrefix(line, "    ") {
				count++
			} else if !strings.HasPrefix(line, " ") && trimmed != "" {
				// hit a top-level key after paths: → done counting
				break
			}
		}
	}
	return count
}
