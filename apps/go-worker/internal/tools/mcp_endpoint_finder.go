package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type MCPProbe struct {
	Path           string `json:"path"`
	URL            string `json:"url"`
	HTTPStatus     int    `json:"http_status"`
	ContentType    string `json:"content_type,omitempty"`
	Found          bool   `json:"found"`
	Reason         string `json:"reason,omitempty"`
}

type MCPServerInfo struct {
	Name        string `json:"name,omitempty"`
	Version     string `json:"version,omitempty"`
	Title       string `json:"title,omitempty"`
}

type MCPToolEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	WriteRisk   bool   `json:"write_risk"` // does the tool name suggest mutation?
}

type MCPPromptEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type MCPEndpointFinderOutput struct {
	Target               string            `json:"target"`
	Probes               []MCPProbe        `json:"probes"`
	ServerFound          bool              `json:"server_found"`
	ServerURL            string            `json:"server_url,omitempty"`
	AuthRequired         bool              `json:"auth_required"`
	AuthMethod           string            `json:"auth_method_hint,omitempty"`
	ServerInfo           *MCPServerInfo    `json:"server_info,omitempty"`
	Tools                []MCPToolEntry    `json:"tools,omitempty"`
	ToolCount            int               `json:"tool_count"`
	WriteRiskTools       []string          `json:"write_risk_tools,omitempty"`
	Prompts              []MCPPromptEntry  `json:"prompts,omitempty"`
	PromptCount          int               `json:"prompt_count"`
	ResourceCount        int               `json:"resource_count"`
	SecuritySeverity     string            `json:"security_severity"` // critical | high | medium | low | none
	SecurityRationale    string            `json:"security_rationale"`
	Source               string            `json:"source"`
	TookMs               int64             `json:"tookMs"`
	Note                 string            `json:"note,omitempty"`
}

// commonMCPPaths to probe.
var commonMCPPaths = []string{
	"/mcp",
	"/sse",
	"/api/mcp",
	"/api/v1/mcp",
	"/v1/mcp",
	"/.well-known/mcp",
	"/mcp/sse",
	"/mcp/v1",
	"/server/mcp",
	"/agent/mcp",
}

// writeRiskTokens — tool name patterns that suggest mutation/destructive ops.
var writeRiskTokens = []string{
	"create", "delete", "remove", "update", "modify", "send", "execute",
	"run", "deploy", "publish", "upload", "write", "set", "post", "put",
	"patch", "push", "commit", "merge", "kill", "stop", "restart", "destroy",
	"drop", "truncate", "alter", "grant", "revoke", "invite",
}

func toolNameHasWriteRisk(name string) bool {
	low := strings.ToLower(name)
	for _, t := range writeRiskTokens {
		if strings.HasPrefix(low, t+"_") || strings.HasSuffix(low, "_"+t) ||
			strings.Contains(low, "_"+t+"_") || low == t {
			return true
		}
	}
	return false
}

// MCPEndpointFinder probes a target host for exposed Model Context Protocol
// servers, then interrogates any found server to enumerate its capabilities.
//
// Strategy:
//  1. Probe ~10 common MCP paths in parallel via JSON-RPC 'initialize' call
//  2. For each successful probe, call 'tools/list', 'prompts/list',
//     'resources/list' to enumerate capabilities
//  3. Auto-classify security severity:
//     - CRITICAL: no auth + destructive tools exposed (delete_*, send_*, execute_*)
//     - HIGH: no auth + many tools exposed (>20)
//     - MEDIUM: no auth + read-only tools
//     - LOW: auth required (likely intentional public endpoint)
//
// Use cases:
//   - Recon: enumerate an org's agent extension surface
//   - Bug bounty: find misconfigured public MCP servers
//   - Validation: scan our own MCP server to verify catalog
func MCPEndpointFinder(ctx context.Context, input map[string]any) (*MCPEndpointFinderOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(strings.ToLower(target))

	// Allow caller to specify a single full URL to interrogate (skip path probing).
	directURL, _ := input["direct_url"].(string)
	directURL = strings.TrimSpace(directURL)

	if target == "" && directURL == "" {
		return nil, errors.New("input.target or input.direct_url required")
	}
	if target != "" {
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			target = "https://" + target
		}
		target = strings.TrimRight(target, "/")
	}

	start := time.Now()
	out := &MCPEndpointFinderOutput{Target: target, Source: "mcp_endpoint_finder"}

	paths := commonMCPPaths
	if directURL != "" {
		paths = []string{} // skip default probing
	}

	// Phase 1: Probe paths in parallel
	probes := make([]MCPProbe, len(paths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, path string) {
			defer wg.Done()
			defer func() { <-sem }()
			probes[idx] = mcpProbe(ctx, target+path)
		}(i, p)
	}
	wg.Wait()
	out.Probes = probes

	// Find first successful probe
	var serverURL string
	if directURL != "" {
		serverURL = directURL
	} else {
		for _, p := range probes {
			if p.Found {
				serverURL = p.URL
				break
			}
		}
	}

	if serverURL == "" {
		out.SecuritySeverity = "none"
		out.SecurityRationale = "No MCP server endpoint discovered at any common path."
		out.TookMs = time.Since(start).Milliseconds()
		out.Note = "No MCP server found. Try direct_url=<full URL> if the path is non-standard."
		return out, nil
	}

	out.ServerFound = true
	out.ServerURL = serverURL

	// Phase 2: Initialize + interrogate
	initResp, authStatus := mcpJSONRPC(ctx, serverURL, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "osint-agent-mcp-finder",
			"version": "1.0.0",
		},
	})
	if authStatus == 401 || authStatus == 403 {
		out.AuthRequired = true
		out.AuthMethod = "HTTP " + fmt.Sprintf("%d", authStatus) + " on initialize"
		out.SecuritySeverity = "low"
		out.SecurityRationale = "MCP server present but requires authentication — likely intentional public endpoint."
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// Parse initialize response for serverInfo
	if initResp != nil {
		if result, ok := initResp["result"].(map[string]any); ok {
			if serverInfo, ok := result["serverInfo"].(map[string]any); ok {
				out.ServerInfo = &MCPServerInfo{}
				if v, ok := serverInfo["name"].(string); ok {
					out.ServerInfo.Name = v
				}
				if v, ok := serverInfo["version"].(string); ok {
					out.ServerInfo.Version = v
				}
				if v, ok := serverInfo["title"].(string); ok {
					out.ServerInfo.Title = v
				}
			}
		}
	}

	// tools/list
	toolsResp, _ := mcpJSONRPC(ctx, serverURL, "tools/list", nil)
	if toolsResp != nil {
		if result, ok := toolsResp["result"].(map[string]any); ok {
			if toolsList, ok := result["tools"].([]any); ok {
				for _, t := range toolsList {
					if tm, ok := t.(map[string]any); ok {
						name, _ := tm["name"].(string)
						desc, _ := tm["description"].(string)
						if name == "" {
							continue
						}
						entry := MCPToolEntry{
							Name:        name,
							Description: truncate(desc, 200),
							WriteRisk:   toolNameHasWriteRisk(name),
						}
						out.Tools = append(out.Tools, entry)
						if entry.WriteRisk {
							out.WriteRiskTools = append(out.WriteRiskTools, name)
						}
					}
				}
			}
		}
	}
	out.ToolCount = len(out.Tools)
	sort.SliceStable(out.Tools, func(i, j int) bool {
		// Write-risk tools first
		if out.Tools[i].WriteRisk != out.Tools[j].WriteRisk {
			return out.Tools[i].WriteRisk
		}
		return out.Tools[i].Name < out.Tools[j].Name
	})

	// prompts/list
	promptsResp, _ := mcpJSONRPC(ctx, serverURL, "prompts/list", nil)
	if promptsResp != nil {
		if result, ok := promptsResp["result"].(map[string]any); ok {
			if promptsList, ok := result["prompts"].([]any); ok {
				for _, p := range promptsList {
					if pm, ok := p.(map[string]any); ok {
						name, _ := pm["name"].(string)
						desc, _ := pm["description"].(string)
						if name == "" {
							continue
						}
						out.Prompts = append(out.Prompts, MCPPromptEntry{
							Name: name, Description: truncate(desc, 200),
						})
					}
				}
			}
		}
	}
	out.PromptCount = len(out.Prompts)

	// resources/list (some servers don't support — that's fine)
	resourcesResp, _ := mcpJSONRPC(ctx, serverURL, "resources/list", nil)
	if resourcesResp != nil {
		if result, ok := resourcesResp["result"].(map[string]any); ok {
			if resList, ok := result["resources"].([]any); ok {
				out.ResourceCount = len(resList)
			}
		}
	}

	// Severity classification
	switch {
	case len(out.WriteRiskTools) > 0:
		out.SecuritySeverity = "critical"
		out.SecurityRationale = fmt.Sprintf("Public MCP server with NO authentication exposes %d destructive/write tools (%s) — agent compromise = data destruction or impersonation",
			len(out.WriteRiskTools), strings.Join(out.WriteRiskTools[:minInt(len(out.WriteRiskTools), 5)], ", "))
	case out.ToolCount > 20:
		out.SecuritySeverity = "high"
		out.SecurityRationale = fmt.Sprintf("Public MCP server with NO authentication exposes %d tools — large attack surface even if read-only", out.ToolCount)
	case out.ToolCount > 0:
		out.SecuritySeverity = "medium"
		out.SecurityRationale = fmt.Sprintf("Public MCP server with NO authentication exposes %d read-only tools — informational disclosure risk", out.ToolCount)
	default:
		out.SecuritySeverity = "low"
		out.SecurityRationale = "MCP server present but reports no tools (may require capability negotiation we didn't perform)"
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// mcpProbe sends a JSON-RPC 'initialize' request to detect MCP server presence.
func mcpProbe(ctx context.Context, url string) MCPProbe {
	rec := MCPProbe{Path: extractPath(url), URL: url}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "osint-agent-mcp-finder",
				"version": "1.0.0",
			},
		},
	})
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "osint-agent/mcp-finder")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		rec.Reason = err.Error()
		return rec
	}
	defer resp.Body.Close()
	rec.HTTPStatus = resp.StatusCode
	rec.ContentType = resp.Header.Get("Content-Type")
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	bodyStr := string(respBody)

	// Auth challenge = MCP-likely but requires auth
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		rec.Found = true
		rec.Reason = fmt.Sprintf("auth required (%d)", resp.StatusCode)
		return rec
	}

	if resp.StatusCode != 200 {
		rec.Reason = fmt.Sprintf("status %d", resp.StatusCode)
		return rec
	}

	// Look for MCP-shaped response
	if strings.Contains(bodyStr, `"jsonrpc"`) && (strings.Contains(bodyStr, `"result"`) || strings.Contains(bodyStr, `"error"`)) {
		rec.Found = true
		rec.Reason = "MCP JSON-RPC response shape detected"
	}
	// SSE stream signals MCP (event-stream)
	if strings.Contains(rec.ContentType, "text/event-stream") {
		rec.Found = true
		rec.Reason = "SSE stream content-type"
	}
	return rec
}

// mcpJSONRPC sends an arbitrary JSON-RPC request to an MCP server URL and
// returns the parsed response (best-effort) and HTTP status.
func mcpJSONRPC(ctx context.Context, url, method string, params map[string]any) (map[string]any, int) {
	if params == nil {
		params = map[string]any{} // MCP SDKs reject "params": null
	}
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "osint-agent/mcp-finder")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, resp.StatusCode
	}
	var parsed map[string]any
	// Try direct JSON first
	if err := json.Unmarshal(respBody, &parsed); err == nil {
		return parsed, resp.StatusCode
	}
	// Try SSE-style (data: ...\n\n) parse — pull first data: line
	for _, line := range strings.Split(string(respBody), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if err := json.Unmarshal([]byte(data), &parsed); err == nil {
				return parsed, resp.StatusCode
			}
		}
	}
	return nil, resp.StatusCode
}

func extractPath(fullURL string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(fullURL, prefix) {
			rest := fullURL[len(prefix):]
			if i := strings.Index(rest, "/"); i >= 0 {
				return rest[i:]
			}
			return "/"
		}
	}
	return fullURL
}
