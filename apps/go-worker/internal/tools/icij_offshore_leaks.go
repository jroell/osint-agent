package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ICIJOffshoreLeaks wraps the public ICIJ Offshore Leaks Database
// (offshoreleaks.icij.org). Free, no key. Indexes the Pandora,
// Paradise, Panama, Bahamas, Offshore, and Swissleaks investigations:
// ~810k+ entities (officers, intermediaries, addresses) and the
// connections between them.
//
// This is one of the highest-value OSINT data sources for beneficial
// ownership and shell-company unmasking.
//
// Modes:
//   - "search"        : full-text search across all entity types
//   - "entity"        : fetch one node by id (officer/entity/intermediary)
//   - "node_relationships" : list 1-hop relationships of a node
//
// Knowledge-graph: emits typed entities (kind: "person" |
// "organization" | "intermediary" | "address") with stable ICIJ node
// IDs and edge attributes. Pairs with `opencorporates` and
// `opensanctions` for cross-reference of beneficial ownership.

type ICIJNode struct {
	ID             string `json:"icij_id"`
	Type           string `json:"node_type"` // Officer | Entity | Intermediary | Address
	Name           string `json:"name"`
	Country        string `json:"country,omitempty"`
	Jurisdiction   string `json:"jurisdiction,omitempty"`
	Address        string `json:"address,omitempty"`
	IncorporatedAt string `json:"incorporation_date,omitempty"`
	Status         string `json:"status,omitempty"`
	Source         string `json:"source_dataset,omitempty"` // pandora_papers, panama_papers, etc.
	URL            string `json:"icij_url"`
}

type ICIJRelationship struct {
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
	Role   string `json:"role"`
}

type ICIJEntity struct {
	Kind        string         `json:"kind"`
	ICIJID      string         `json:"icij_id"`
	Name        string         `json:"name"`
	URL         string         `json:"url"`
	Description string         `json:"description,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type ICIJOffshoreLeaksOutput struct {
	Mode              string             `json:"mode"`
	Query             string             `json:"query,omitempty"`
	Returned          int                `json:"returned"`
	Total             int                `json:"total,omitempty"`
	Nodes             []ICIJNode         `json:"nodes,omitempty"`
	Relationships     []ICIJRelationship `json:"relationships,omitempty"`
	Entities          []ICIJEntity       `json:"entities"`
	HighlightFindings []string           `json:"highlight_findings"`
	Source            string             `json:"source"`
	TookMs            int64              `json:"tookMs"`
}

func ICIJOffshoreLeaks(ctx context.Context, input map[string]any) (*ICIJOffshoreLeaksOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		switch {
		case input["node_id"] != nil:
			mode = "entity"
		case input["relationships_for"] != nil:
			mode = "node_relationships"
		default:
			mode = "search"
		}
	}
	out := &ICIJOffshoreLeaksOutput{Mode: mode, Source: "offshoreleaks.icij.org"}
	start := time.Now()
	cli := &http.Client{Timeout: 45 * time.Second}

	get := func(u string) (string, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
		req.Header.Set("Accept", "text/html, application/json")
		req.Header.Set("User-Agent", "osint-agent/1.0 (research)")
		resp, err := cli.Do(req)
		if err != nil {
			return "", fmt.Errorf("icij: %w", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("icij HTTP %d", resp.StatusCode)
		}
		return string(body), nil
	}

	switch mode {
	case "search":
		q, _ := input["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("input.query required")
		}
		out.Query = q
		typeFilter := "" // 1=entity 2=officer 3=intermediary 4=address
		if t, ok := input["entity_type"].(string); ok {
			switch strings.ToLower(t) {
			case "entity", "company":
				typeFilter = "1"
			case "officer", "person":
				typeFilter = "2"
			case "intermediary":
				typeFilter = "3"
			case "address":
				typeFilter = "4"
			}
		}
		params := url.Values{}
		params.Set("q", q)
		if typeFilter != "" {
			params.Set("type", typeFilter)
		}
		params.Set("e", "1")
		html, err := get("https://offshoreleaks.icij.org/search?" + params.Encode())
		if err != nil {
			return nil, err
		}
		out.Nodes, out.Total = parseICIJSearchResults(html)

	case "entity":
		id, _ := input["node_id"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.node_id required")
		}
		out.Query = id
		ntype, _ := input["node_type"].(string)
		ntype = strings.ToLower(ntype)
		if ntype == "" {
			ntype = "entity"
		}
		// ICIJ exposes /nodes/<id> with JSON
		html, err := get(fmt.Sprintf("https://offshoreleaks.icij.org/nodes/%s", url.PathEscape(id)))
		if err != nil {
			return nil, err
		}
		node := parseICIJDetailHTML(html, id)
		if node != nil {
			out.Nodes = []ICIJNode{*node}
		}

	case "node_relationships":
		id, _ := input["relationships_for"].(string)
		if id == "" {
			return nil, fmt.Errorf("input.relationships_for required")
		}
		out.Query = id
		// /nodes/<id>/relationships endpoint returns the same HTML page;
		// we parse out relationship blocks using a regex on the visible text.
		html, err := get(fmt.Sprintf("https://offshoreleaks.icij.org/nodes/%s", url.PathEscape(id)))
		if err != nil {
			return nil, err
		}
		out.Relationships = parseICIJRelationships(html, id)
		// Also include the seed node
		seed := parseICIJDetailHTML(html, id)
		if seed != nil {
			out.Nodes = []ICIJNode{*seed}
		}

	default:
		return nil, fmt.Errorf("unknown mode '%s'", mode)
	}

	out.Returned = len(out.Nodes) + len(out.Relationships)
	out.Entities = icijBuildEntities(out)
	out.HighlightFindings = icijBuildHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

var (
	icijRowRe       = regexp.MustCompile(`(?is)<tr[^>]*>\s*<td[^>]*><a[^>]+href="(/nodes/(\d+))"[^>]*>([^<]+)</a>.*?</tr>`)
	icijDetailRowRe = regexp.MustCompile(`(?is)<dt[^>]*>([^<]+)</dt>\s*<dd[^>]*>([^<]+)</dd>`)
	icijRelRowRe    = regexp.MustCompile(`(?is)<tr[^>]*>\s*<td[^>]*>([^<]+)</td>\s*<td[^>]*><a[^>]+href="/nodes/(\d+)"[^>]*>([^<]+)</a>`)
	icijTotalRe     = regexp.MustCompile(`(?is)Showing\s+\d+\s*-\s*\d+\s+of\s+([0-9,]+)\s+results`)
)

func parseICIJSearchResults(html string) ([]ICIJNode, int) {
	out := []ICIJNode{}
	seen := map[string]bool{}
	for _, m := range icijRowRe.FindAllStringSubmatch(html, -1) {
		if len(m) >= 4 {
			id := m[2]
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, ICIJNode{
				ID:   id,
				Name: strings.TrimSpace(stripHTMLBare(m[3])),
				URL:  "https://offshoreleaks.icij.org" + m[1],
			})
		}
		if len(out) >= 30 {
			break
		}
	}
	total := 0
	if t := icijTotalRe.FindStringSubmatch(html); len(t) >= 2 {
		fmt.Sscanf(strings.ReplaceAll(t[1], ",", ""), "%d", &total)
	}
	return out, total
}

func parseICIJDetailHTML(html, id string) *ICIJNode {
	n := &ICIJNode{
		ID:  id,
		URL: "https://offshoreleaks.icij.org/nodes/" + id,
	}
	// Extract h1 title
	if m := regexp.MustCompile(`(?is)<h1[^>]*>\s*([^<]+)</h1>`).FindStringSubmatch(html); len(m) >= 2 {
		n.Name = strings.TrimSpace(stripHTMLBare(m[1]))
	}
	// Extract dt/dd pairs (Address, Jurisdiction, Source, etc.)
	for _, m := range icijDetailRowRe.FindAllStringSubmatch(html, -1) {
		if len(m) >= 3 {
			label := strings.ToLower(strings.TrimSpace(stripHTMLBare(m[1])))
			value := strings.TrimSpace(stripHTMLBare(m[2]))
			switch {
			case strings.Contains(label, "country"):
				n.Country = value
			case strings.Contains(label, "jurisdiction"):
				n.Jurisdiction = value
			case strings.Contains(label, "address"):
				n.Address = value
			case strings.Contains(label, "incorporation"):
				n.IncorporatedAt = value
			case strings.Contains(label, "status"):
				n.Status = value
			case strings.Contains(label, "data from") || strings.Contains(label, "source"):
				n.Source = value
			case strings.Contains(label, "type"):
				n.Type = value
			}
		}
	}
	if n.Name == "" {
		return nil
	}
	return n
}

func parseICIJRelationships(html, fromID string) []ICIJRelationship {
	out := []ICIJRelationship{}
	for _, m := range icijRelRowRe.FindAllStringSubmatch(html, -1) {
		if len(m) >= 4 {
			role := strings.TrimSpace(stripHTMLBare(m[1]))
			toID := m[2]
			out = append(out, ICIJRelationship{
				FromID: fromID,
				ToID:   toID,
				Role:   role,
			})
		}
		if len(out) >= 50 {
			break
		}
	}
	return out
}

func icijBuildEntities(o *ICIJOffshoreLeaksOutput) []ICIJEntity {
	ents := []ICIJEntity{}
	for _, n := range o.Nodes {
		kind := strings.ToLower(n.Type)
		if kind == "" {
			kind = "organization" // default to org since most search hits are entities
		}
		switch kind {
		case "officer":
			kind = "person"
		case "entity", "company":
			kind = "organization"
		case "intermediary":
			kind = "intermediary"
		case "address":
			kind = "address"
		}
		desc := ""
		if n.Jurisdiction != "" {
			desc = "Jurisdiction: " + n.Jurisdiction
		}
		if n.Source != "" {
			if desc != "" {
				desc += " · "
			}
			desc += "Source: " + n.Source
		}
		ents = append(ents, ICIJEntity{
			Kind: kind, ICIJID: n.ID, Name: n.Name, URL: n.URL,
			Description: desc,
			Attributes: map[string]any{
				"node_type":      n.Type,
				"country":        n.Country,
				"jurisdiction":   n.Jurisdiction,
				"address":        n.Address,
				"incorporation":  n.IncorporatedAt,
				"status":         n.Status,
				"source_dataset": n.Source,
			},
		})
	}
	for _, r := range o.Relationships {
		// edge entity: kind=relationship for graph ingestion
		ents = append(ents, ICIJEntity{
			Kind:   "relationship",
			ICIJID: r.FromID + "->" + r.ToID,
			Name:   r.Role,
			URL:    "https://offshoreleaks.icij.org/nodes/" + r.ToID,
			Attributes: map[string]any{
				"from_id": r.FromID,
				"to_id":   r.ToID,
				"role":    r.Role,
			},
		})
	}
	return ents
}

func icijBuildHighlights(o *ICIJOffshoreLeaksOutput) []string {
	hi := []string{fmt.Sprintf("✓ icij offshore leaks %s: %d records (total %d)", o.Mode, o.Returned, o.Total)}
	for i, n := range o.Nodes {
		if i >= 8 {
			break
		}
		jur := ""
		if n.Jurisdiction != "" {
			jur = " [" + n.Jurisdiction + "]"
		}
		hi = append(hi, fmt.Sprintf("  • %s [%s]%s — %s", n.Name, n.ID, jur, n.URL))
	}
	for i, r := range o.Relationships {
		if i >= 6 {
			break
		}
		hi = append(hi, fmt.Sprintf("    rel: %s → %s (%s)", r.FromID, r.ToID, r.Role))
	}
	return hi
}
