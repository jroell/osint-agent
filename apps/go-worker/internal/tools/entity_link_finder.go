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

type LinkedEntity struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

type Connection struct {
	Kind       string       `json:"kind"`             // shared_employer | shared_school | shared_board | founded_same_company | a_employs_b | b_employs_a | shared_subject | shared_investor
	Bridge     LinkedEntity `json:"bridge"`           // the entity through which A and B are connected
	ARole      string       `json:"a_role,omitempty"` // A's relationship to bridge (e.g. "Software Engineer", "Founder")
	BRole      string       `json:"b_role,omitempty"`
	APeriod    string       `json:"a_period,omitempty"`
	BPeriod    string       `json:"b_period,omitempty"`
	Confidence string       `json:"confidence"` // "high" | "medium" | "low"
}

type EntityLinkOutput struct {
	EntityAQuery     string       `json:"entity_a_query"`
	EntityBQuery     string       `json:"entity_b_query"`
	EntityA          LinkedEntity `json:"entity_a,omitempty"`
	EntityB          LinkedEntity `json:"entity_b,omitempty"`
	Resolved         bool         `json:"both_resolved"`
	Connections      []Connection `json:"connections"`
	TotalConnections int          `json:"total_connections"`
	Source           string       `json:"source"`
	TookMs           int64        `json:"tookMs"`
	Note             string       `json:"note,omitempty"`
}

// EntityLinkFinder traces connection paths between two entities (people or
// organizations) via Diffbot's Knowledge Graph. Performs 1-hop common-neighbor
// analysis across:
//
//	employers (current + past)   →  did A and B work at the same company?
//	educations                   →  did A and B attend the same institution?
//	board memberships            →  do A and B share a board seat?
//	founded organizations        →  did A and B co-found anything?
//	subjectOf (for orgs)         →  do A and B share notable mentions?
//
// Plus directional checks:
//
//	A employs B (A is a founder/exec of B's employer)
//	B employs A (mirror)
//
// Returns the full set of connections with bridging entities and roles. This
// is the explicit "are X and Y connected, and how?" primitive — the marquee
// connecting-the-dots tool.
//
// REQUIRES DIFFBOT_API_KEY. Falls back to a clear error if either entity
// can't be resolved in the KG.
func EntityLinkFinder(ctx context.Context, input map[string]any) (*EntityLinkOutput, error) {
	a, _ := input["entity_a"].(string)
	b, _ := input["entity_b"].(string)
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return nil, errors.New("input.entity_a and input.entity_b both required (names of people or organizations)")
	}
	apiKey := os.Getenv("DIFFBOT_API_KEY")
	if apiKey == "" {
		return nil, errors.New("DIFFBOT_API_KEY env var required")
	}
	// Default both type hints to Person — by far the most common use case for
	// "are X and Y connected?" queries. Override via input.type_a / type_b.
	// Without this default, Diffbot's relevance ranker can return organizations
	// like "Mark Zuckerberg book club" as the top hit for the name "Mark Zuckerberg".
	typeHintA, _ := input["type_a"].(string)
	if typeHintA == "" {
		typeHintA = "Person"
	}
	typeHintB, _ := input["type_b"].(string)
	if typeHintB == "" {
		typeHintB = "Person"
	}

	start := time.Now()
	out := &EntityLinkOutput{
		EntityAQuery: a, EntityBQuery: b,
		Connections: []Connection{},
		Source:      "kg.diffbot.com (1-hop common-neighbor)",
	}

	// Resolve both entities in parallel.
	type resolved struct {
		entity LinkedEntity
		raw    map[string]interface{}
		err    error
	}
	chA := make(chan resolved, 1)
	chB := make(chan resolved, 1)
	go func() {
		ent, raw, err := diffbotResolveEntity(ctx, apiKey, a, typeHintA)
		chA <- resolved{ent, raw, err}
	}()
	go func() {
		ent, raw, err := diffbotResolveEntity(ctx, apiKey, b, typeHintB)
		chB <- resolved{ent, raw, err}
	}()
	resA := <-chA
	resB := <-chB

	if resA.err != nil {
		out.Note = fmt.Sprintf("entity_a resolution: %v", resA.err)
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	if resB.err != nil {
		out.Note = fmt.Sprintf("entity_b resolution: %v", resB.err)
		out.EntityA = resA.entity
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	out.EntityA = resA.entity
	out.EntityB = resB.entity
	out.Resolved = true

	// Extract neighbor sets from each entity.
	aN := extractNeighbors(resA.raw)
	bN := extractNeighbors(resB.raw)

	// Find shared employers — most common connection type.
	for _, ae := range aN.employers {
		for _, be := range bN.employers {
			if linkMatch(ae.id, ae.name, be.id, be.name) {
				out.Connections = append(out.Connections, Connection{
					Kind: "shared_employer", Bridge: LinkedEntity{ID: ae.id, Name: bestName(ae.name, be.name), Type: "Organization"},
					ARole: ae.role, APeriod: ae.period, BRole: be.role, BPeriod: be.period,
					Confidence: confidenceFromOverlap(ae.period, be.period),
				})
			}
		}
	}
	// Shared educations.
	for _, ae := range aN.educations {
		for _, be := range bN.educations {
			if linkMatch(ae.id, ae.name, be.id, be.name) {
				out.Connections = append(out.Connections, Connection{
					Kind: "shared_education", Bridge: LinkedEntity{ID: ae.id, Name: bestName(ae.name, be.name), Type: "Educational"},
					ARole: ae.role, APeriod: ae.period, BRole: be.role, BPeriod: be.period,
					Confidence: confidenceFromOverlap(ae.period, be.period),
				})
			}
		}
	}
	// Shared board memberships.
	for _, ae := range aN.boards {
		for _, be := range bN.boards {
			if linkMatch(ae.id, ae.name, be.id, be.name) {
				out.Connections = append(out.Connections, Connection{
					Kind: "shared_board", Bridge: LinkedEntity{ID: ae.id, Name: bestName(ae.name, be.name), Type: "Organization"},
					ARole: ae.role, BRole: be.role,
					Confidence: "high",
				})
			}
		}
	}
	// Shared founded companies (rare but high-signal).
	for _, ae := range aN.founded {
		for _, be := range bN.founded {
			if linkMatch(ae.id, ae.name, be.id, be.name) {
				out.Connections = append(out.Connections, Connection{
					Kind: "co_founders", Bridge: LinkedEntity{ID: ae.id, Name: bestName(ae.name, be.name), Type: "Organization"},
					ARole: "founder", BRole: "founder", Confidence: "high",
				})
			}
		}
	}
	// Directional: did A found a company B works at? (or vice versa)
	for _, founded := range aN.founded {
		for _, emp := range bN.employers {
			if linkMatch(founded.id, founded.name, emp.id, emp.name) {
				out.Connections = append(out.Connections, Connection{
					Kind:   "a_founded_b_employer",
					Bridge: LinkedEntity{ID: founded.id, Name: bestName(founded.name, emp.name), Type: "Organization"},
					ARole:  "founder", BRole: emp.role, BPeriod: emp.period,
					Confidence: "high",
				})
			}
		}
	}
	for _, founded := range bN.founded {
		for _, emp := range aN.employers {
			if linkMatch(founded.id, founded.name, emp.id, emp.name) {
				out.Connections = append(out.Connections, Connection{
					Kind:   "b_founded_a_employer",
					Bridge: LinkedEntity{ID: founded.id, Name: bestName(founded.name, emp.name), Type: "Organization"},
					ARole:  emp.role, APeriod: emp.period, BRole: "founder",
					Confidence: "high",
				})
			}
		}
	}

	// Direct edge: A is in B's neighbor set (or vice versa) — e.g. A is a founder of B (Org).
	if resB.entity.Type == "Organization" {
		for _, founded := range aN.founded {
			if linkMatch(founded.id, founded.name, resB.entity.ID, resB.entity.Name) {
				out.Connections = append(out.Connections, Connection{
					Kind: "a_founded_b", Bridge: resB.entity, ARole: "founder", Confidence: "high",
				})
			}
		}
		for _, emp := range aN.employers {
			if linkMatch(emp.id, emp.name, resB.entity.ID, resB.entity.Name) {
				out.Connections = append(out.Connections, Connection{
					Kind: "a_employed_at_b", Bridge: resB.entity, ARole: emp.role, APeriod: emp.period, Confidence: "high",
				})
			}
		}
	}
	if resA.entity.Type == "Organization" {
		for _, founded := range bN.founded {
			if linkMatch(founded.id, founded.name, resA.entity.ID, resA.entity.Name) {
				out.Connections = append(out.Connections, Connection{
					Kind: "b_founded_a", Bridge: resA.entity, BRole: "founder", Confidence: "high",
				})
			}
		}
		for _, emp := range bN.employers {
			if linkMatch(emp.id, emp.name, resA.entity.ID, resA.entity.Name) {
				out.Connections = append(out.Connections, Connection{
					Kind: "b_employed_at_a", Bridge: resA.entity, BRole: emp.role, BPeriod: emp.period, Confidence: "high",
				})
			}
		}
	}

	// Add the canonical Diffbot graph-neighbor pass. This covers the original
	// employer/school/board/founder checks and newer OSINT pivots such as shared
	// investors, locations, and public URIs. The dedupe pass below collapses any
	// overlap with the legacy checks above.
	out.Connections = append(out.Connections, diffbotConnectionsFromRaw(resA.raw, resB.raw)...)

	// Dedupe connections by (kind, bridge.id|bridge.name).
	out.Connections = diffbotDedupeConnections(out.Connections)
	out.TotalConnections = len(out.Connections)
	if out.TotalConnections == 0 {
		out.Note = "1-hop common-neighbor analysis found no shared employers/schools/boards/founded-companies. Multi-hop traversal not yet implemented."
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// neighborSet — the set of entities related to a given person/org.
type neighborEntry struct {
	id     string
	name   string
	role   string
	period string
}
type neighborSet struct {
	employers  []neighborEntry
	educations []neighborEntry
	boards     []neighborEntry
	founded    []neighborEntry
}

// extractNeighbors pulls the 1-hop neighborhood out of a Diffbot KG entity record.
//
// Diffbot reality (audited from live response 2026-04-29): there is no separate
// `founderOfOrganizations` or `boards` array — founder/board roles are
// classified inside `employments[]` by examining the `title.normalizedName`
// or category. So we walk employments once and bin each entry by role.
func extractNeighbors(raw map[string]interface{}) neighborSet {
	out := neighborSet{}
	employments, _ := raw["employments"].([]interface{})
	for _, e := range employments {
		em, _ := e.(map[string]interface{})
		employer, _ := em["employer"].(map[string]interface{})
		ne := neighborEntry{}
		ne.id = strFromMap(employer, "diffbotUri", "id", "targetDiffbotId")
		ne.name = strFromMap(employer, "name")
		// Title in Diffbot KG can be a string OR a {name, normalizedName, categories}
		// object. Try both shapes.
		titleObj, _ := em["title"].(map[string]interface{})
		if titleObj != nil {
			ne.role = strFromMap(titleObj, "normalizedName", "name")
		}
		if ne.role == "" {
			ne.role = strFromMap(em, "title")
		}
		ne.period = formatPeriod(em["from"], em["to"], em["isCurrent"])
		if ne.id == "" && ne.name == "" {
			continue
		}
		// Bin by role keywords. The same employer can appear in multiple bins
		// (e.g., founder + employee).
		roleLow := strings.ToLower(ne.role)
		isFounder := strings.Contains(roleLow, "founder") || strings.Contains(roleLow, "co-founder") || strings.Contains(roleLow, "cofounder")
		isBoard := strings.Contains(roleLow, "board") || strings.Contains(roleLow, "director") && strings.Contains(roleLow, "board")
		if isFounder {
			out.founded = append(out.founded, ne)
		}
		if isBoard {
			out.boards = append(out.boards, ne)
		}
		// Always add to employers — being a founder/board member also counts as employed.
		out.employers = append(out.employers, ne)
	}

	if educations, ok := raw["educations"].([]interface{}); ok {
		for _, e := range educations {
			ed, _ := e.(map[string]interface{})
			institution, _ := ed["institution"].(map[string]interface{})
			ne := neighborEntry{}
			ne.id = strFromMap(institution, "diffbotUri", "id", "targetDiffbotId")
			ne.name = strFromMap(institution, "name")
			ne.role = strFromMap(ed, "degree", "major.0.name")
			ne.period = formatPeriod(ed["from"], ed["to"], nil)
			if ne.id != "" || ne.name != "" {
				out.educations = append(out.educations, ne)
			}
		}
	}
	return out
}

// diffbotResolveEntity runs a DQL query for the given entity name + optional
// type hint, returns the top match.
func diffbotResolveEntity(ctx context.Context, apiKey, name, typeHint string) (LinkedEntity, map[string]interface{}, error) {
	q := fmt.Sprintf(`name:"%s"`, strings.ReplaceAll(name, `"`, `\"`))
	if typeHint != "" {
		q = fmt.Sprintf("type:%s %s", typeHint, q)
	}
	endpoint := fmt.Sprintf("https://kg.diffbot.com/kg/v3/dql?type=query&token=%s&query=%s&size=3&format=json",
		url.QueryEscape(apiKey), url.QueryEscape(q))
	body, err := httpGetJSON(ctx, endpoint, 20*time.Second)
	if err != nil {
		return LinkedEntity{}, nil, err
	}
	// Diffbot wraps each hit as {score, entity, entity_ctx}. Unwrap to inner entity.
	var resp struct {
		Hits int `json:"hits"`
		Data []struct {
			Score  float64                `json:"score"`
			Entity map[string]interface{} `json:"entity"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return LinkedEntity{}, nil, err
	}
	if len(resp.Data) == 0 || resp.Data[0].Entity == nil {
		return LinkedEntity{}, nil, fmt.Errorf("no Diffbot KG entity for %q", name)
	}
	r := resp.Data[0].Entity
	ent := LinkedEntity{
		ID:          strFromMap(r, "diffbotUri", "id"),
		Type:        strFromMap(r, "type"),
		Name:        strFromMap(r, "name"),
		URL:         strFromMap(r, "diffbotUri"),
		Description: truncDesc(r["description"]),
	}
	return ent, r, nil
}

// linkMatch — two entities are "the same" if their IDs match OR their names match (case-insensitive).
func linkMatch(idA, nameA, idB, nameB string) bool {
	if idA != "" && idA == idB {
		return true
	}
	if nameA == "" || nameB == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(nameA), strings.TrimSpace(nameB))
}

func bestName(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func confidenceFromOverlap(periodA, periodB string) string {
	// If both periods are present and overlap (or both isCurrent), confidence high.
	// Otherwise medium (they worked there, but possibly not at the same time).
	if (strings.Contains(periodA, "current") && strings.Contains(periodB, "current")) ||
		(periodA != "" && periodB != "" && timeRangesOverlap(periodA, periodB)) {
		return "high"
	}
	return "medium"
}

// timeRangesOverlap — extremely lenient overlap check (just looks for shared year).
func timeRangesOverlap(a, b string) bool {
	yearsA := extractYears(a)
	yearsB := extractYears(b)
	if len(yearsA) == 0 || len(yearsB) == 0 {
		return false
	}
	aStart, aEnd := yearRange(yearsA)
	bStart, bEnd := yearRange(yearsB)
	return aStart <= bEnd && bStart <= aEnd
}

func extractYears(s string) []int {
	var out []int
	for i := 0; i+4 <= len(s); i++ {
		chunk := s[i : i+4]
		if chunk[0] == '1' || chunk[0] == '2' {
			ok := true
			for _, c := range chunk {
				if c < '0' || c > '9' {
					ok = false
					break
				}
			}
			if ok {
				var y int
				if _, err := fmt.Sscanf(chunk, "%d", &y); err == nil {
					out = append(out, y)
				}
			}
		}
	}
	return out
}

func yearRange(years []int) (int, int) {
	start, end := years[0], years[0]
	for _, y := range years[1:] {
		if y < start {
			start = y
		}
		if y > end {
			end = y
		}
	}
	return start, end
}

func formatPeriod(from, to, isCurrent any) string {
	out := ""
	if v := getDateString(from); v != "" {
		out = v
	}
	out += " — "
	if v := getDateString(to); v != "" {
		out += v
	}
	if b, ok := isCurrent.(bool); ok && b {
		out += " (current)"
	}
	if strings.TrimSpace(out) == "—" {
		return ""
	}
	return strings.TrimSpace(out)
}

func getDateString(v any) string {
	if m, ok := v.(map[string]interface{}); ok {
		if s, ok := m["str"].(string); ok {
			return s
		}
		if y, ok := m["year"].(float64); ok {
			return fmt.Sprintf("%d", int(y))
		}
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// strFromMap — fetch the first non-empty string field, supporting dot-paths
// like "categories.0.name" for nested array+map traversal.
func strFromMap(m map[string]interface{}, paths ...string) string {
	if m == nil {
		return ""
	}
	for _, p := range paths {
		parts := strings.Split(p, ".")
		var cur interface{} = m
		ok := true
		for _, pp := range parts {
			switch v := cur.(type) {
			case map[string]interface{}:
				cur = v[pp]
			case []interface{}:
				idx := 0
				if _, err := fmt.Sscanf(pp, "%d", &idx); err == nil && idx < len(v) {
					cur = v[idx]
				} else {
					ok = false
				}
			default:
				ok = false
			}
			if !ok || cur == nil {
				break
			}
		}
		if ok {
			if s, ok := cur.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func truncDesc(v any) string {
	if s, ok := v.(string); ok {
		if len(s) > 240 {
			return s[:240] + "…"
		}
		return s
	}
	return ""
}
