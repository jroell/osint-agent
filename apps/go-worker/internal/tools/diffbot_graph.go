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

type DiffbotCanonicalEntity struct {
	ID         string                 `json:"id"`
	Kind       string                 `json:"kind"`
	Name       string                 `json:"name,omitempty"`
	URI        string                 `json:"uri,omitempty"`
	URL        string                 `json:"url,omitempty"`
	Source     string                 `json:"source"`
	Confidence float64                `json:"confidence,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

type DiffbotCanonicalRelationship struct {
	ID         string                 `json:"id"`
	Kind       string                 `json:"kind"`
	From       string                 `json:"from"`
	To         string                 `json:"to"`
	Role       string                 `json:"role,omitempty"`
	ValidFrom  string                 `json:"valid_from,omitempty"`
	ValidTo    string                 `json:"valid_to,omitempty"`
	Source     string                 `json:"source"`
	Confidence float64                `json:"confidence,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

type DiffbotCanonicalClaim struct {
	ID            string                 `json:"id"`
	Subject       string                 `json:"subject"`
	Predicate     string                 `json:"predicate"`
	Object        string                 `json:"object"`
	Source        string                 `json:"source"`
	Evidence      []string               `json:"evidence,omitempty"`
	Confidence    float64                `json:"confidence,omitempty"`
	RetrievedAt   string                 `json:"retrieved_at,omitempty"`
	Properties    map[string]interface{} `json:"properties,omitempty"`
	DiffbotEntity string                 `json:"diffbot_entity,omitempty"`
}

type DiffbotHardToFindLead struct {
	Kind       string   `json:"kind"`
	EntityID   string   `json:"entity_id,omitempty"`
	EntityName string   `json:"entity_name,omitempty"`
	Via        string   `json:"via,omitempty"`
	Why        string   `json:"why"`
	NextTools  []string `json:"next_tools,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
}

type DiffbotCanonicalGraph struct {
	Entities        []DiffbotCanonicalEntity       `json:"entities"`
	Relationships   []DiffbotCanonicalRelationship `json:"relationships"`
	Claims          []DiffbotCanonicalClaim        `json:"claims"`
	HardToFindLeads []DiffbotHardToFindLead        `json:"hard_to_find_leads,omitempty"`
}

type DiffbotEntityNetworkOutput struct {
	Query  string                   `json:"query"`
	Type   string                   `json:"type,omitempty"`
	Total  int                      `json:"total"`
	Hits   int                      `json:"hits"`
	Graph  DiffbotCanonicalGraph    `json:"graph"`
	Seeds  []DiffbotCanonicalEntity `json:"seeds"`
	Source string                   `json:"source"`
	TookMs int64                    `json:"tookMs"`
	Notes  []string                 `json:"notes,omitempty"`
}

type DiffbotCommonNeighborsOutput struct {
	EntityAQuery     string                `json:"entity_a_query"`
	EntityBQuery     string                `json:"entity_b_query"`
	EntityA          LinkedEntity          `json:"entity_a,omitempty"`
	EntityB          LinkedEntity          `json:"entity_b,omitempty"`
	Resolved         bool                  `json:"both_resolved"`
	Connections      []Connection          `json:"connections"`
	TotalConnections int                   `json:"total_connections"`
	Graph            DiffbotCanonicalGraph `json:"graph"`
	Source           string                `json:"source"`
	TookMs           int64                 `json:"tookMs"`
	Note             string                `json:"note,omitempty"`
}

type DiffbotArticleCoMentionOutput struct {
	EntityA  string                   `json:"entity_a"`
	EntityB  string                   `json:"entity_b"`
	Query    string                   `json:"query"`
	Total    int                      `json:"total"`
	Hits     int                      `json:"hits"`
	Articles []map[string]interface{} `json:"articles"`
	Graph    DiffbotCanonicalGraph    `json:"graph"`
	Source   string                   `json:"source"`
	TookMs   int64                    `json:"tookMs"`
}

type diffbotSearchHit struct {
	Score  float64                `json:"score"`
	Entity map[string]interface{} `json:"entity"`
}

func DiffbotEntityNetwork(ctx context.Context, input map[string]any) (*DiffbotEntityNetworkOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		name, _ := input["name"].(string)
		name = strings.TrimSpace(name)
		if name != "" {
			q = fmt.Sprintf(`name:"%s"`, strings.ReplaceAll(name, `"`, `\"`))
		}
	}
	if q == "" {
		return nil, errors.New("input.query or input.name required")
	}
	entityType, _ := input["type"].(string)
	if entityType != "" && !strings.Contains(strings.ToLower(q), "type:") {
		q = fmt.Sprintf("type:%s %s", entityType, q)
	}
	size := intFromInput(input, "size", 3, 1, 10)

	apiKey := os.Getenv("DIFFBOT_API_KEY")
	if apiKey == "" {
		return nil, errors.New("DIFFBOT_API_KEY env var required")
	}

	start := time.Now()
	total, hits, data, err := diffbotKGSearch(ctx, apiKey, q, size, 35*time.Second)
	if err != nil {
		return nil, err
	}
	raw := make([]map[string]interface{}, 0, len(data))
	for _, h := range data {
		if h.Entity == nil {
			continue
		}
		h.Entity["_diffbot_score"] = h.Score
		raw = append(raw, h.Entity)
	}
	graph := diffbotBuildCanonicalGraph(raw, "diffbot_entity_network")
	seeds := make([]DiffbotCanonicalEntity, 0, len(raw))
	for _, ent := range raw {
		seeds = append(seeds, diffbotCanonicalEntity(ent, "diffbot_entity_network"))
	}
	return &DiffbotEntityNetworkOutput{
		Query: q, Type: entityType, Total: total, Hits: hits, Graph: graph, Seeds: seeds,
		Source: "kg.diffbot.com/dql", TookMs: time.Since(start).Milliseconds(),
		Notes: []string{"Diffbot is used as an external public-web relationship oracle; persist graph output into the internal tenant graph with provenance before inference."},
	}, nil
}

func DiffbotCommonNeighbors(ctx context.Context, input map[string]any) (*DiffbotCommonNeighborsOutput, error) {
	a, _ := input["entity_a"].(string)
	b, _ := input["entity_b"].(string)
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return nil, errors.New("input.entity_a and input.entity_b required")
	}
	apiKey := os.Getenv("DIFFBOT_API_KEY")
	if apiKey == "" {
		return nil, errors.New("DIFFBOT_API_KEY env var required")
	}
	typeA, _ := input["type_a"].(string)
	if typeA == "" {
		typeA = "Person"
	}
	typeB, _ := input["type_b"].(string)
	if typeB == "" {
		typeB = "Person"
	}

	start := time.Now()
	entA, rawA, err := diffbotResolveEntity(ctx, apiKey, a, typeA)
	if err != nil {
		return &DiffbotCommonNeighborsOutput{
			EntityAQuery: a, EntityBQuery: b, Source: "kg.diffbot.com/dql",
			TookMs: time.Since(start).Milliseconds(), Note: fmt.Sprintf("entity_a resolution: %v", err),
		}, nil
	}
	entB, rawB, err := diffbotResolveEntity(ctx, apiKey, b, typeB)
	if err != nil {
		return &DiffbotCommonNeighborsOutput{
			EntityAQuery: a, EntityBQuery: b, EntityA: entA, Source: "kg.diffbot.com/dql",
			TookMs: time.Since(start).Milliseconds(), Note: fmt.Sprintf("entity_b resolution: %v", err),
		}, nil
	}
	connections := diffbotConnectionsFromRaw(rawA, rawB)
	graph := diffbotBuildCanonicalGraph([]map[string]interface{}{rawA, rawB}, "diffbot_common_neighbors")
	for _, c := range connections {
		if c.Bridge.ID == "" {
			continue
		}
		graph.HardToFindLeads = append(graph.HardToFindLeads, DiffbotHardToFindLead{
			Kind: "common_neighbor", EntityID: c.Bridge.ID, EntityName: c.Bridge.Name, Via: c.Kind,
			Why:        fmt.Sprintf("%s and %s both connect through %s", entA.Name, entB.Name, c.Bridge.Name),
			NextTools:  []string{"diffbot_entity_network", "wikidata_lookup", "opencorporates_search"},
			Confidence: diffbotConfidenceNumber(c.Confidence),
		})
	}
	return &DiffbotCommonNeighborsOutput{
		EntityAQuery: a, EntityBQuery: b, EntityA: entA, EntityB: entB, Resolved: true,
		Connections: connections, TotalConnections: len(connections), Graph: graph,
		Source: "kg.diffbot.com/dql", TookMs: time.Since(start).Milliseconds(),
	}, nil
}

func DiffbotArticleCoMentions(ctx context.Context, input map[string]any) (*DiffbotArticleCoMentionOutput, error) {
	a, _ := input["entity_a"].(string)
	b, _ := input["entity_b"].(string)
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return nil, errors.New("input.entity_a and input.entity_b required")
	}
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		q = fmt.Sprintf(`type:Article text:contains:"%s" text:contains:"%s"`, strings.ReplaceAll(a, `"`, `\"`), strings.ReplaceAll(b, `"`, `\"`))
	}
	size := intFromInput(input, "size", 10, 1, 50)
	apiKey := os.Getenv("DIFFBOT_API_KEY")
	if apiKey == "" {
		return nil, errors.New("DIFFBOT_API_KEY env var required")
	}

	start := time.Now()
	total, hits, data, err := diffbotKGSearch(ctx, apiKey, q, size, 35*time.Second)
	if err != nil {
		return nil, err
	}
	articles := make([]map[string]interface{}, 0, len(data))
	for _, h := range data {
		if h.Entity == nil {
			continue
		}
		h.Entity["_diffbot_score"] = h.Score
		articles = append(articles, h.Entity)
	}
	graph := diffbotBuildCanonicalGraph(articles, "diffbot_article_co_mentions")
	diffbotAddArticleMentionEdges(&graph, articles, a, b)
	return &DiffbotArticleCoMentionOutput{
		EntityA: a, EntityB: b, Query: q, Total: total, Hits: hits, Articles: articles, Graph: graph,
		Source: "kg.diffbot.com/dql", TookMs: time.Since(start).Milliseconds(),
	}, nil
}

func diffbotKGSearch(ctx context.Context, apiKey, query string, size int, timeout time.Duration) (int, int, []diffbotSearchHit, error) {
	endpoint := fmt.Sprintf("https://kg.diffbot.com/kg/v3/dql?type=query&token=%s&query=%s&size=%d&format=json",
		url.QueryEscape(apiKey), url.QueryEscape(query), size)
	body, err := httpGetJSON(ctx, endpoint, timeout)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("diffbot kg: %w", err)
	}
	var parsed struct {
		Hits  int                `json:"hits"`
		Total int                `json:"total"`
		Data  []diffbotSearchHit `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, 0, nil, fmt.Errorf("diffbot kg parse: %w", err)
	}
	return parsed.Total, parsed.Hits, parsed.Data, nil
}

func diffbotBuildCanonicalGraph(rawEntities []map[string]interface{}, source string) DiffbotCanonicalGraph {
	graph := DiffbotCanonicalGraph{
		Entities:      []DiffbotCanonicalEntity{},
		Relationships: []DiffbotCanonicalRelationship{},
		Claims:        []DiffbotCanonicalClaim{},
	}
	entitySeen := map[string]struct{}{}
	relSeen := map[string]struct{}{}
	now := time.Now().UTC().Format(time.RFC3339)

	addEntity := func(ent DiffbotCanonicalEntity) {
		if ent.ID == "" {
			return
		}
		if _, ok := entitySeen[ent.ID]; ok {
			return
		}
		entitySeen[ent.ID] = struct{}{}
		graph.Entities = append(graph.Entities, ent)
	}
	addRelationship := func(rel DiffbotCanonicalRelationship, seed map[string]interface{}) {
		if rel.From == "" || rel.To == "" || rel.Kind == "" {
			return
		}
		rel.ID = diffbotRelID(rel.Kind, rel.From, rel.To, rel.Role)
		if _, ok := relSeen[rel.ID]; ok {
			return
		}
		relSeen[rel.ID] = struct{}{}
		if rel.Source == "" {
			rel.Source = source
		}
		if rel.Confidence == 0 {
			rel.Confidence = 0.82
		}
		graph.Relationships = append(graph.Relationships, rel)
		graph.Claims = append(graph.Claims, DiffbotCanonicalClaim{
			ID:      diffbotRelID("CLAIM", rel.ID, source, ""),
			Subject: rel.From, Predicate: rel.Kind, Object: rel.To, Source: rel.Source,
			Evidence: diffbotEvidence(seed), Confidence: rel.Confidence, RetrievedAt: now,
			DiffbotEntity: diffbotEntityID(seed),
		})
		if diffbotHighSignalRelationship(rel.Kind) {
			graph.HardToFindLeads = append(graph.HardToFindLeads, DiffbotHardToFindLead{
				Kind: "high_signal_relationship", EntityID: rel.To, Via: rel.Kind,
				Why: diffbotLeadWhy(rel.Kind), Confidence: rel.Confidence,
				NextTools: diffbotNextToolsForRelationship(rel.Kind),
			})
		}
	}

	for _, raw := range rawEntities {
		if raw == nil {
			continue
		}
		seed := diffbotCanonicalEntity(raw, source)
		addEntity(seed)
		seedID := seed.ID
		if seedID == "" {
			continue
		}
		for _, uri := range stringSlice(raw["allUris"]) {
			addEntity(DiffbotCanonicalEntity{ID: uri, Kind: "url", URL: uri, Source: source, Confidence: 0.78})
			addRelationship(DiffbotCanonicalRelationship{Kind: "HAS_URI", From: seedID, To: uri, Source: source, Confidence: 0.78}, raw)
		}
		if homepage := strFromMap(raw, "homepageUri", "homepageURI", "homepage"); homepage != "" {
			addEntity(DiffbotCanonicalEntity{ID: homepage, Kind: "url", URL: homepage, Source: source, Confidence: 0.86})
			addRelationship(DiffbotCanonicalRelationship{Kind: "HAS_HOMEPAGE", From: seedID, To: homepage, Source: source, Confidence: 0.86}, raw)
		}
		for _, loc := range diffbotLocationMaps(raw) {
			ent := diffbotLinkedEntity(loc, "place", source)
			addEntity(ent)
			addRelationship(DiffbotCanonicalRelationship{Kind: "LOCATED_IN", From: seedID, To: ent.ID, Source: source, Confidence: 0.72}, raw)
		}
		for _, e := range asSlice(raw["employments"]) {
			em, _ := e.(map[string]interface{})
			employer, _ := em["employer"].(map[string]interface{})
			if employer == nil {
				continue
			}
			ent := diffbotLinkedEntity(employer, "organization", source)
			addEntity(ent)
			role := diffbotEmploymentRole(em)
			kind := "WORKED_AT"
			if diffbotRoleContains(role, "founder", "co-founder", "cofounder") {
				kind = "FOUNDED"
			} else if diffbotRoleContains(role, "board", "director") {
				kind = "BOARD_MEMBER_OF"
			}
			addRelationship(DiffbotCanonicalRelationship{
				Kind: kind, From: seedID, To: ent.ID, Role: role, Source: source, Confidence: 0.84,
				ValidFrom: getDateString(em["from"]), ValidTo: getDateString(em["to"]),
			}, raw)
		}
		for _, e := range asSlice(raw["educations"]) {
			ed, _ := e.(map[string]interface{})
			institution, _ := ed["institution"].(map[string]interface{})
			if institution == nil {
				continue
			}
			ent := diffbotLinkedEntity(institution, "organization", source)
			addEntity(ent)
			addRelationship(DiffbotCanonicalRelationship{
				Kind: "ATTENDED", From: seedID, To: ent.ID, Role: strFromMap(ed, "degree", "major.0.name"),
				Source: source, Confidence: 0.78, ValidFrom: getDateString(ed["from"]), ValidTo: getDateString(ed["to"]),
			}, raw)
		}
		diffbotAddLinkedRelationships(raw, source, seedID, "founders", "FOUNDED_BY", "person", addEntity, addRelationship)
		diffbotAddLinkedRelationships(raw, source, seedID, "allInvestors", "FUNDED_BY", "organization", addEntity, addRelationship)
		diffbotAddLinkedRelationships(raw, source, seedID, "investors", "FUNDED_BY", "organization", addEntity, addRelationship)
		diffbotAddLinkedRelationships(raw, source, seedID, "subsidiaries", "HAS_SUBSIDIARY", "organization", addEntity, addRelationship)
		diffbotAddLinkedRelationships(raw, source, seedID, "acquisitions", "ACQUIRED", "organization", addEntity, addRelationship)
		diffbotAddLinkedRelationships(raw, source, seedID, "customers", "HAS_CUSTOMER", "organization", addEntity, addRelationship)
		diffbotAddLinkedRelationships(raw, source, seedID, "competitors", "HAS_COMPETITOR", "organization", addEntity, addRelationship)
		for _, field := range []string{"parentCompany", "parentOrganization", "ultimateParent"} {
			if parent, ok := raw[field].(map[string]interface{}); ok {
				ent := diffbotLinkedEntity(parent, "organization", source)
				addEntity(ent)
				addRelationship(DiffbotCanonicalRelationship{Kind: "HAS_PARENT", From: seedID, To: ent.ID, Source: source, Confidence: 0.86}, raw)
			}
		}
	}
	return graph
}

func diffbotAddLinkedRelationships(
	raw map[string]interface{},
	source string,
	seedID string,
	field string,
	kind string,
	targetKind string,
	addEntity func(DiffbotCanonicalEntity),
	addRelationship func(DiffbotCanonicalRelationship, map[string]interface{}),
) {
	for _, item := range asSlice(raw[field]) {
		m, _ := item.(map[string]interface{})
		if m == nil {
			continue
		}
		ent := diffbotLinkedEntity(m, targetKind, source)
		addEntity(ent)
		addRelationship(DiffbotCanonicalRelationship{Kind: kind, From: seedID, To: ent.ID, Source: source, Confidence: 0.86}, raw)
	}
}

func diffbotCanonicalEntity(raw map[string]interface{}, source string) DiffbotCanonicalEntity {
	id := diffbotEntityID(raw)
	kind := diffbotKind(raw)
	name := strFromMap(raw, "name", "title", "caption")
	props := map[string]interface{}{}
	for _, key := range []string{"description", "summary", "homepageUri", "nbEmployees", "nbEmployeesMin", "nbEmployeesMax", "wikipediaUri", "_diffbot_score"} {
		if v, ok := raw[key]; ok {
			props[key] = v
		}
	}
	return DiffbotCanonicalEntity{
		ID: id, Kind: kind, Name: name, URI: strFromMap(raw, "diffbotUri", "uri"),
		URL: strFromMap(raw, "url", "homepageUri"), Source: source,
		Confidence: diffbotScore(raw, 0.8), Properties: props,
	}
}

func diffbotLinkedEntity(raw map[string]interface{}, fallbackKind string, source string) DiffbotCanonicalEntity {
	ent := diffbotCanonicalEntity(raw, source)
	if ent.Kind == "entity" || ent.Kind == "" {
		ent.Kind = fallbackKind
	}
	return ent
}

func diffbotEntityID(raw map[string]interface{}) string {
	id := strFromMap(raw, "diffbotUri", "id", "uri", "url", "homepageUri")
	if id != "" {
		return id
	}
	name := strFromMap(raw, "name", "title", "caption")
	if name == "" {
		return ""
	}
	return "diffbot:synthetic:" + strings.ToLower(strings.Join(strings.Fields(name), "-"))
}

func diffbotKind(raw map[string]interface{}) string {
	t := strings.ToLower(strFromMap(raw, "type", "types.0"))
	switch {
	case strings.Contains(t, "person"):
		return "person"
	case strings.Contains(t, "organization") || strings.Contains(t, "company") || strings.Contains(t, "educational"):
		return "organization"
	case strings.Contains(t, "article"):
		return "article"
	case strings.Contains(t, "place") || strings.Contains(t, "location"):
		return "place"
	case strings.Contains(t, "image"):
		return "image"
	case strings.Contains(t, "product"):
		return "product"
	default:
		return "entity"
	}
}

func diffbotLocationMaps(raw map[string]interface{}) []map[string]interface{} {
	out := []map[string]interface{}{}
	if m, ok := raw["location"].(map[string]interface{}); ok {
		out = append(out, m)
	}
	for _, v := range asSlice(raw["locations"]) {
		if m, ok := v.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func diffbotEmploymentRole(em map[string]interface{}) string {
	if title, ok := em["title"].(map[string]interface{}); ok {
		if s := strFromMap(title, "normalizedName", "name"); s != "" {
			return s
		}
	}
	return strFromMap(em, "title")
}

type diffbotNeighbor struct {
	id       string
	name     string
	typ      string
	relation string
	role     string
	period   string
}

func diffbotConnectionsFromRaw(a, b map[string]interface{}) []Connection {
	aN := diffbotGraphNeighbors(a)
	bN := diffbotGraphNeighbors(b)
	out := []Connection{}
	for _, an := range aN {
		for _, bn := range bN {
			if an.relation != bn.relation {
				continue
			}
			if !linkMatch(an.id, an.name, bn.id, bn.name) {
				continue
			}
			out = append(out, Connection{
				Kind: diffbotSharedConnectionKind(an.relation), Bridge: LinkedEntity{ID: an.id, Name: bestName(an.name, bn.name), Type: an.typ},
				ARole: an.role, BRole: bn.role, APeriod: an.period, BPeriod: bn.period,
				Confidence: confidenceFromOverlap(an.period, bn.period),
			})
		}
	}
	return diffbotDedupeConnections(out)
}

func diffbotGraphNeighbors(raw map[string]interface{}) []diffbotNeighbor {
	out := []diffbotNeighbor{}
	for _, e := range asSlice(raw["employments"]) {
		em, _ := e.(map[string]interface{})
		employer, _ := em["employer"].(map[string]interface{})
		if employer == nil {
			continue
		}
		role := diffbotEmploymentRole(em)
		out = append(out, diffbotNeighbor{
			id: strFromMap(employer, "diffbotUri", "id", "targetDiffbotId"), name: strFromMap(employer, "name"),
			typ: "Organization", relation: "employer", role: role, period: formatPeriod(em["from"], em["to"], em["isCurrent"]),
		})
		if diffbotRoleContains(role, "founder", "co-founder", "cofounder") {
			out = append(out, diffbotNeighbor{id: strFromMap(employer, "diffbotUri", "id", "targetDiffbotId"), name: strFromMap(employer, "name"), typ: "Organization", relation: "founded", role: "founder"})
		}
		if diffbotRoleContains(role, "board", "director") {
			out = append(out, diffbotNeighbor{id: strFromMap(employer, "diffbotUri", "id", "targetDiffbotId"), name: strFromMap(employer, "name"), typ: "Organization", relation: "board", role: role})
		}
	}
	for _, e := range asSlice(raw["educations"]) {
		ed, _ := e.(map[string]interface{})
		institution, _ := ed["institution"].(map[string]interface{})
		if institution == nil {
			continue
		}
		out = append(out, diffbotNeighbor{
			id: strFromMap(institution, "diffbotUri", "id", "targetDiffbotId"), name: strFromMap(institution, "name"),
			typ: "Educational", relation: "education", role: strFromMap(ed, "degree", "major.0.name"), period: formatPeriod(ed["from"], ed["to"], nil),
		})
	}
	for _, field := range []string{"allInvestors", "investors"} {
		for _, v := range asSlice(raw[field]) {
			if m, ok := v.(map[string]interface{}); ok {
				out = append(out, diffbotNeighbor{id: diffbotEntityID(m), name: strFromMap(m, "name"), typ: "Organization", relation: "investor"})
			}
		}
	}
	for _, loc := range diffbotLocationMaps(raw) {
		out = append(out, diffbotNeighbor{id: diffbotEntityID(loc), name: strFromMap(loc, "name", "city.name"), typ: "Place", relation: "location"})
	}
	for _, uri := range stringSlice(raw["allUris"]) {
		out = append(out, diffbotNeighbor{id: uri, name: uri, typ: "URL", relation: "uri"})
	}
	return out
}

func diffbotSharedConnectionKind(relation string) string {
	switch relation {
	case "employer":
		return "shared_employer"
	case "education":
		return "shared_education"
	case "board":
		return "shared_board"
	case "founded":
		return "co_founders"
	case "investor":
		return "shared_investor"
	case "location":
		return "shared_location"
	case "uri":
		return "shared_uri"
	default:
		return "shared_" + relation
	}
}

func diffbotAddArticleMentionEdges(graph *DiffbotCanonicalGraph, articles []map[string]interface{}, a, b string) {
	seen := map[string]struct{}{}
	addMention := func(name string) DiffbotCanonicalEntity {
		id := "mention:" + strings.ToLower(strings.Join(strings.Fields(name), "-"))
		ent := DiffbotCanonicalEntity{ID: id, Kind: "mention", Name: name, Source: "diffbot_article_co_mentions", Confidence: 0.65}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			graph.Entities = append(graph.Entities, ent)
		}
		return ent
	}
	ma := addMention(a)
	mb := addMention(b)
	for _, article := range articles {
		articleID := diffbotEntityID(article)
		if articleID == "" {
			continue
		}
		for _, mention := range []DiffbotCanonicalEntity{ma, mb} {
			rel := DiffbotCanonicalRelationship{
				ID: diffbotRelID("MENTIONS", articleID, mention.ID, ""), Kind: "MENTIONS", From: articleID, To: mention.ID,
				Source: "diffbot_article_co_mentions", Confidence: 0.66,
			}
			graph.Relationships = append(graph.Relationships, rel)
			graph.Claims = append(graph.Claims, DiffbotCanonicalClaim{
				ID: diffbotRelID("CLAIM", rel.ID, rel.Source, ""), Subject: rel.From, Predicate: rel.Kind, Object: rel.To,
				Source: rel.Source, Evidence: diffbotEvidence(article), Confidence: rel.Confidence, RetrievedAt: time.Now().UTC().Format(time.RFC3339),
			})
		}
		graph.HardToFindLeads = append(graph.HardToFindLeads, DiffbotHardToFindLead{
			Kind: "article_co_mention", EntityID: articleID, EntityName: strFromMap(article, "title", "name"),
			Why:        "Public-web article mentions both entities; use as corroborating evidence, not a standalone relationship.",
			NextTools:  []string{"diffbot_extract", "google_news_recent", "bigquery_gdelt"},
			Confidence: 0.58,
		})
	}
}

func diffbotDedupeConnections(conns []Connection) []Connection {
	seen := map[string]struct{}{}
	out := []Connection{}
	for _, c := range conns {
		key := c.Kind + "::" + c.Bridge.ID + "::" + strings.ToLower(c.Bridge.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}

func diffbotEvidence(raw map[string]interface{}) []string {
	out := []string{}
	for _, key := range []string{"diffbotUri", "url", "pageUrl", "resolvedPageUrl", "homepageUri", "wikipediaUri"} {
		if s := strFromMap(raw, key); s != "" {
			out = append(out, s)
		}
	}
	for _, uri := range stringSlice(raw["allUris"]) {
		if len(out) >= 8 {
			break
		}
		out = append(out, uri)
	}
	return out
}

func diffbotRoleContains(role string, needles ...string) bool {
	role = strings.ToLower(role)
	for _, needle := range needles {
		if strings.Contains(role, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func diffbotHighSignalRelationship(kind string) bool {
	switch kind {
	case "FOUNDED", "BOARD_MEMBER_OF", "FOUNDED_BY", "FUNDED_BY", "HAS_SUBSIDIARY", "HAS_PARENT", "ACQUIRED":
		return true
	default:
		return false
	}
}

func diffbotLeadWhy(kind string) string {
	switch kind {
	case "FOUNDED", "FOUNDED_BY":
		return "Founder edges often reveal prior companies, shells, advisors, and hidden operating networks."
	case "BOARD_MEMBER_OF":
		return "Board roles bridge otherwise separate organizations and can reveal influence networks."
	case "FUNDED_BY":
		return "Investor overlap is a strong non-obvious corporate-network pivot."
	case "HAS_SUBSIDIARY", "HAS_PARENT", "ACQUIRED":
		return "Corporate hierarchy edges reveal related assets that may not share domains or branding."
	default:
		return "High-signal Diffbot relationship worth corroborating with authoritative sources."
	}
}

func diffbotNextToolsForRelationship(kind string) []string {
	switch kind {
	case "FUNDED_BY", "HAS_SUBSIDIARY", "HAS_PARENT", "ACQUIRED", "FOUNDED_BY":
		return []string{"opencorporates_search", "gleif_lei_lookup", "sec_edgar_search", "wikidata_lookup"}
	case "FOUNDED", "BOARD_MEMBER_OF":
		return []string{"diffbot_common_neighbors", "wikidata_lookup", "linkedin_proxycurl"}
	default:
		return []string{"diffbot_entity_network", "wikidata_lookup"}
	}
}

func diffbotScore(raw map[string]interface{}, fallback float64) float64 {
	if v, ok := raw["_diffbot_score"].(float64); ok && v > 0 {
		if v > 1 {
			return 0.99
		}
		return v
	}
	return fallback
}

func diffbotConfidenceNumber(label string) float64 {
	switch label {
	case "high":
		return 0.88
	case "medium":
		return 0.68
	default:
		return 0.45
	}
}

func diffbotRelID(kind, from, to, role string) string {
	parts := []string{kind, from, to, role}
	return strings.Join(parts, "::")
}

func asSlice(v interface{}) []interface{} {
	if v == nil {
		return nil
	}
	if s, ok := v.([]interface{}); ok {
		return s
	}
	return nil
}

func stringSlice(v interface{}) []string {
	out := []string{}
	switch vv := v.(type) {
	case []string:
		return vv
	case []interface{}:
		for _, item := range vv {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case string:
		if vv != "" {
			out = append(out, vv)
		}
	}
	return out
}

func intFromInput(input map[string]any, key string, fallback, minValue, maxValue int) int {
	out := fallback
	switch v := input[key].(type) {
	case float64:
		out = int(v)
	case int:
		out = v
	}
	if out < minValue {
		out = minValue
	}
	if out > maxValue {
		out = maxValue
	}
	return out
}
