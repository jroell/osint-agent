package tools

import "testing"

func TestDiffbotBuildGraphPersonEdgesAndClaims(t *testing.T) {
	raw := map[string]interface{}{
		"diffbotUri": "diffbot://person/jane-doe",
		"type":       "Person",
		"name":       "Jane Doe",
		"allUris": []interface{}{
			"https://www.linkedin.com/in/janedoe",
			"https://janedoe.example",
		},
		"location": map[string]interface{}{
			"name": "Cincinnati, Ohio",
			"city": map[string]interface{}{"name": "Cincinnati"},
		},
		"employments": []interface{}{
			map[string]interface{}{
				"employer":  map[string]interface{}{"diffbotUri": "diffbot://org/acme", "name": "Acme"},
				"title":     map[string]interface{}{"normalizedName": "Founder"},
				"from":      map[string]interface{}{"str": "2020"},
				"isCurrent": true,
			},
			map[string]interface{}{
				"employer": map[string]interface{}{"diffbotUri": "diffbot://org/boardco", "name": "BoardCo"},
				"title":    map[string]interface{}{"normalizedName": "Board Director"},
			},
		},
		"educations": []interface{}{
			map[string]interface{}{
				"institution": map[string]interface{}{"diffbotUri": "diffbot://org/uc", "name": "University of Cincinnati"},
				"degree":      "MBA",
				"from":        map[string]interface{}{"str": "2016"},
				"to":          map[string]interface{}{"str": "2018"},
			},
		},
	}

	graph := diffbotBuildCanonicalGraph([]map[string]interface{}{raw}, "unit-test")

	assertGraphHasEntity(t, graph, "diffbot://person/jane-doe", "person")
	assertGraphHasEntity(t, graph, "diffbot://org/acme", "organization")
	assertGraphHasEntity(t, graph, "https://www.linkedin.com/in/janedoe", "url")
	assertGraphHasRelationship(t, graph, "FOUNDED", "diffbot://person/jane-doe", "diffbot://org/acme")
	assertGraphHasRelationship(t, graph, "BOARD_MEMBER_OF", "diffbot://person/jane-doe", "diffbot://org/boardco")
	assertGraphHasRelationship(t, graph, "ATTENDED", "diffbot://person/jane-doe", "diffbot://org/uc")
	assertGraphHasRelationship(t, graph, "HAS_URI", "diffbot://person/jane-doe", "https://www.linkedin.com/in/janedoe")

	if len(graph.Claims) < 4 {
		t.Fatalf("expected relationship claims, got %d", len(graph.Claims))
	}
	if len(graph.HardToFindLeads) == 0 {
		t.Fatalf("expected high-signal leads for founder/board edges")
	}
}

func TestDiffbotBuildGraphOrganizationEdges(t *testing.T) {
	raw := map[string]interface{}{
		"diffbotUri":  "diffbot://org/acme",
		"type":        "Organization",
		"name":        "Acme",
		"homepageUri": "https://acme.example",
		"founders": []interface{}{
			map[string]interface{}{"diffbotUri": "diffbot://person/jane-doe", "name": "Jane Doe"},
		},
		"allInvestors": []interface{}{
			map[string]interface{}{"diffbotUri": "diffbot://org/venture", "name": "Venture Fund"},
		},
		"subsidiaries": []interface{}{
			map[string]interface{}{"diffbotUri": "diffbot://org/acme-labs", "name": "Acme Labs"},
		},
		"parentCompany": map[string]interface{}{"diffbotUri": "diffbot://org/parent", "name": "ParentCo"},
		"acquisitions": []interface{}{
			map[string]interface{}{"diffbotUri": "diffbot://org/oldco", "name": "OldCo"},
		},
	}

	graph := diffbotBuildCanonicalGraph([]map[string]interface{}{raw}, "unit-test")

	assertGraphHasRelationship(t, graph, "FOUNDED_BY", "diffbot://org/acme", "diffbot://person/jane-doe")
	assertGraphHasRelationship(t, graph, "FUNDED_BY", "diffbot://org/acme", "diffbot://org/venture")
	assertGraphHasRelationship(t, graph, "HAS_SUBSIDIARY", "diffbot://org/acme", "diffbot://org/acme-labs")
	assertGraphHasRelationship(t, graph, "HAS_PARENT", "diffbot://org/acme", "diffbot://org/parent")
	assertGraphHasRelationship(t, graph, "ACQUIRED", "diffbot://org/acme", "diffbot://org/oldco")
}

func TestDiffbotConnectionGraphFindsHardToFindCommonNeighbors(t *testing.T) {
	a := map[string]interface{}{
		"diffbotUri": "diffbot://person/a",
		"type":       "Person",
		"name":       "Person A",
		"employments": []interface{}{
			map[string]interface{}{
				"employer": map[string]interface{}{"diffbotUri": "diffbot://org/shared", "name": "SharedCo"},
				"title":    map[string]interface{}{"normalizedName": "Founder"},
				"from":     map[string]interface{}{"str": "2019"},
				"to":       map[string]interface{}{"str": "2022"},
			},
		},
	}
	b := map[string]interface{}{
		"diffbotUri": "diffbot://person/b",
		"type":       "Person",
		"name":       "Person B",
		"employments": []interface{}{
			map[string]interface{}{
				"employer": map[string]interface{}{"diffbotUri": "diffbot://org/shared", "name": "SharedCo"},
				"title":    map[string]interface{}{"normalizedName": "Advisor"},
				"from":     map[string]interface{}{"str": "2021"},
				"to":       map[string]interface{}{"str": "2024"},
			},
		},
	}

	connections := diffbotConnectionsFromRaw(a, b)
	if len(connections) != 1 {
		t.Fatalf("expected one shared connection, got %d: %#v", len(connections), connections)
	}
	if connections[0].Kind != "shared_employer" {
		t.Fatalf("expected shared_employer, got %q", connections[0].Kind)
	}
	if connections[0].Confidence != "high" {
		t.Fatalf("expected high confidence from overlapping years, got %q", connections[0].Confidence)
	}
	if connections[0].Bridge.ID != "diffbot://org/shared" {
		t.Fatalf("expected shared bridge, got %#v", connections[0].Bridge)
	}
}

func assertGraphHasEntity(t *testing.T, graph DiffbotCanonicalGraph, id string, kind string) {
	t.Helper()
	for _, ent := range graph.Entities {
		if ent.ID == id && ent.Kind == kind {
			return
		}
	}
	t.Fatalf("missing entity id=%s kind=%s in %#v", id, kind, graph.Entities)
}

func assertGraphHasRelationship(t *testing.T, graph DiffbotCanonicalGraph, kind string, from string, to string) {
	t.Helper()
	for _, rel := range graph.Relationships {
		if rel.Kind == kind && rel.From == from && rel.To == to {
			return
		}
	}
	t.Fatalf("missing relationship kind=%s from=%s to=%s in %#v", kind, from, to, graph.Relationships)
}
