package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// CTOfficial is one principal investigator / overall official.
type CTOfficial struct {
	Name        string `json:"name"`
	Affiliation string `json:"affiliation,omitempty"`
	Role        string `json:"role,omitempty"`
}

// CTLocation is one trial site.
type CTLocation struct {
	Facility string `json:"facility,omitempty"`
	City     string `json:"city,omitempty"`
	State    string `json:"state,omitempty"`
	Country  string `json:"country,omitempty"`
}

// CTStudy is one trial.
type CTStudy struct {
	NCTID         string       `json:"nct_id"`
	BriefTitle    string       `json:"brief_title"`
	OfficialTitle string       `json:"official_title,omitempty"`
	Status        string       `json:"status,omitempty"`
	StartDate     string       `json:"start_date,omitempty"`
	CompletionDate string      `json:"completion_date,omitempty"`
	Sponsor       string       `json:"lead_sponsor,omitempty"`
	Collaborators []string     `json:"collaborators,omitempty"`
	StudyType     string       `json:"study_type,omitempty"`
	Phases        []string     `json:"phases,omitempty"`
	Enrollment    int          `json:"enrollment,omitempty"`
	Conditions    []string     `json:"conditions,omitempty"`
	Interventions []string     `json:"interventions,omitempty"`
	OverallOfficials []CTOfficial `json:"overall_officials,omitempty"`
	Locations     []CTLocation `json:"locations,omitempty"`
	BriefSummary  string       `json:"brief_summary,omitempty"`
	URL           string       `json:"clinicaltrials_url"`
}

// CTSponsorAggregate counts trials per sponsor.
type CTSponsorAggregate struct {
	Sponsor    string `json:"sponsor"`
	TrialCount int    `json:"trial_count"`
}

// CTPIAggregate counts trials per PI.
type CTPIAggregate struct {
	Name        string `json:"name"`
	Affiliation string `json:"affiliation,omitempty"`
	TrialCount  int    `json:"trial_count"`
}

// CTConditionAggregate counts trials per condition.
type CTConditionAggregate struct {
	Condition  string `json:"condition"`
	TrialCount int    `json:"trial_count"`
}

// CTStatusAggregate counts trials per status.
type CTStatusAggregate struct {
	Status     string `json:"status"`
	TrialCount int    `json:"trial_count"`
}

// CTSearchOutput is the response.
type CTSearchOutput struct {
	Mode             string                 `json:"mode"`
	Query            string                 `json:"query"`
	Returned         int                    `json:"returned"`
	Studies          []CTStudy              `json:"studies"`
	TopSponsors      []CTSponsorAggregate   `json:"top_sponsors,omitempty"`
	TopPIs           []CTPIAggregate        `json:"top_principal_investigators,omitempty"`
	TopConditions    []CTConditionAggregate `json:"top_conditions,omitempty"`
	StatusBreakdown  []CTStatusAggregate    `json:"status_breakdown,omitempty"`
	UniqueSponsorCountries []string         `json:"unique_country_locations,omitempty"`
	TotalEnrollment  int                    `json:"total_enrollment,omitempty"`
	YearRange        string                 `json:"year_range,omitempty"`
	HighlightFindings []string              `json:"highlight_findings"`
	Source           string                 `json:"source"`
	TookMs           int64                  `json:"tookMs"`
	Note             string                 `json:"note,omitempty"`
}

// raw v2 API response
type ctRawResp struct {
	Studies []ctRawStudy `json:"studies"`
}

type ctRawStudy struct {
	ProtocolSection struct {
		IdentificationModule struct {
			NCTID         string `json:"nctId"`
			BriefTitle    string `json:"briefTitle"`
			OfficialTitle string `json:"officialTitle"`
		} `json:"identificationModule"`
		StatusModule struct {
			OverallStatus    string `json:"overallStatus"`
			StartDateStruct struct {
				Date string `json:"date"`
			} `json:"startDateStruct"`
			CompletionDateStruct struct {
				Date string `json:"date"`
			} `json:"completionDateStruct"`
		} `json:"statusModule"`
		SponsorCollaboratorsModule struct {
			LeadSponsor struct {
				Name string `json:"name"`
			} `json:"leadSponsor"`
			Collaborators []struct {
				Name string `json:"name"`
			} `json:"collaborators"`
		} `json:"sponsorCollaboratorsModule"`
		DescriptionModule struct {
			BriefSummary string `json:"briefSummary"`
		} `json:"descriptionModule"`
		ConditionsModule struct {
			Conditions []string `json:"conditions"`
		} `json:"conditionsModule"`
		ArmsInterventionsModule struct {
			Interventions []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"interventions"`
		} `json:"armsInterventionsModule"`
		DesignModule struct {
			StudyType string   `json:"studyType"`
			Phases    []string `json:"phases"`
			EnrollmentInfo struct {
				Count int `json:"count"`
			} `json:"enrollmentInfo"`
		} `json:"designModule"`
		ContactsLocationsModule struct {
			OverallOfficials []struct {
				Name        string `json:"name"`
				Affiliation string `json:"affiliation"`
				Role        string `json:"role"`
			} `json:"overallOfficials"`
			Locations []struct {
				Facility string `json:"facility"`
				City     string `json:"city"`
				State    string `json:"state"`
				Country  string `json:"country"`
			} `json:"locations"`
		} `json:"contactsLocationsModule"`
	} `json:"protocolSection"`
}

// ClinicalTrialsSearch queries the public ClinicalTrials.gov v2 API.
// Free, no auth. ~480K registered trials since 2000.
//
// 5 modes:
//   - "condition"   : search by disease/condition (uses query.cond)
//   - "sponsor"     : search by sponsor name (drug company / institution)
//   - "pi_name"     : search by PI name (uses AREA[OverallOfficialName])
//   - "text"        : general full-text query
//   - "nct_lookup"  : direct lookup by NCT ID (e.g. "NCT05668741")
//
// Why this matters for ER:
//   - Distinct surface from NIH RePORTER (grants) and PubMed (publications):
//     ClinicalTrials.gov tracks every registered clinical study with PI +
//     sponsor + multi-site location data + enrollment counts.
//   - PI affiliation is stamped per-trial (same pattern as IETF/PubMed
//     temporal employer trail).
//   - Sponsor field reveals pharma-funded vs academic vs government trials.
//   - Multi-site locations reveal collaborator networks across institutions.
//   - Use cases: trace a researcher's clinical trial portfolio, find all
//     trials at a hospital, find all sponsors of a specific drug class.
func ClinicalTrialsSearch(ctx context.Context, input map[string]any) (*CTSearchOutput, error) {
	mode, _ := input["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "condition"
	}
	query, _ := input["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("input.query required")
	}
	limit := 25
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}
	statusFilter, _ := input["status"].(string)
	statusFilter = strings.ToUpper(strings.TrimSpace(statusFilter))

	out := &CTSearchOutput{
		Mode:   mode,
		Query:  query,
		Source: "clinicaltrials.gov v2 API",
	}
	start := time.Now()
	client := &http.Client{Timeout: 45 * time.Second}

	if mode == "nct_lookup" {
		// Single-trial detail
		s, err := ctFetchByNCT(ctx, client, query)
		if err != nil {
			return nil, err
		}
		if s == nil {
			out.Note = fmt.Sprintf("no trial with NCT ID '%s'", query)
			out.HighlightFindings = []string{out.Note}
			out.TookMs = time.Since(start).Milliseconds()
			return out, nil
		}
		out.Studies = []CTStudy{*s}
		out.Returned = 1
	} else {
		studies, err := ctSearch(ctx, client, mode, query, limit, statusFilter)
		if err != nil {
			return nil, err
		}
		out.Studies = studies
		out.Returned = len(studies)
	}

	if out.Returned == 0 {
		out.Note = fmt.Sprintf("no clinical trials returned for mode=%s query='%s'", mode, query)
		out.HighlightFindings = []string{out.Note}
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	// Aggregations
	sponsorMap := map[string]int{}
	piMap := map[string]*CTPIAggregate{}
	condMap := map[string]int{}
	statusMap := map[string]int{}
	countrySet := map[string]struct{}{}
	totalEnrollment := 0
	minYear, maxYear := "", ""

	for _, s := range out.Studies {
		if s.Sponsor != "" {
			sponsorMap[s.Sponsor]++
		}
		for _, c := range s.Conditions {
			if c != "" {
				condMap[c]++
			}
		}
		if s.Status != "" {
			statusMap[s.Status]++
		}
		for _, o := range s.OverallOfficials {
			key := o.Name
			ag, ok := piMap[key]
			if !ok {
				ag = &CTPIAggregate{Name: o.Name, Affiliation: o.Affiliation}
				piMap[key] = ag
			}
			ag.TrialCount++
		}
		for _, l := range s.Locations {
			if l.Country != "" {
				countrySet[l.Country] = struct{}{}
			}
		}
		totalEnrollment += s.Enrollment
		// Year tracking from start date
		if len(s.StartDate) >= 4 {
			yr := s.StartDate[:4]
			if minYear == "" || yr < minYear {
				minYear = yr
			}
			if yr > maxYear {
				maxYear = yr
			}
		}
	}

	for sp, c := range sponsorMap {
		out.TopSponsors = append(out.TopSponsors, CTSponsorAggregate{Sponsor: sp, TrialCount: c})
	}
	sort.SliceStable(out.TopSponsors, func(i, j int) bool { return out.TopSponsors[i].TrialCount > out.TopSponsors[j].TrialCount })
	if len(out.TopSponsors) > 10 {
		out.TopSponsors = out.TopSponsors[:10]
	}

	for _, ag := range piMap {
		out.TopPIs = append(out.TopPIs, *ag)
	}
	sort.SliceStable(out.TopPIs, func(i, j int) bool { return out.TopPIs[i].TrialCount > out.TopPIs[j].TrialCount })
	if len(out.TopPIs) > 10 {
		out.TopPIs = out.TopPIs[:10]
	}

	for c, n := range condMap {
		out.TopConditions = append(out.TopConditions, CTConditionAggregate{Condition: c, TrialCount: n})
	}
	sort.SliceStable(out.TopConditions, func(i, j int) bool { return out.TopConditions[i].TrialCount > out.TopConditions[j].TrialCount })
	if len(out.TopConditions) > 10 {
		out.TopConditions = out.TopConditions[:10]
	}

	for s, n := range statusMap {
		out.StatusBreakdown = append(out.StatusBreakdown, CTStatusAggregate{Status: s, TrialCount: n})
	}
	sort.SliceStable(out.StatusBreakdown, func(i, j int) bool { return out.StatusBreakdown[i].TrialCount > out.StatusBreakdown[j].TrialCount })

	for c := range countrySet {
		out.UniqueSponsorCountries = append(out.UniqueSponsorCountries, c)
	}
	sort.Strings(out.UniqueSponsorCountries)

	out.TotalEnrollment = totalEnrollment
	if minYear != "" && maxYear != "" {
		if minYear == maxYear {
			out.YearRange = minYear
		} else {
			out.YearRange = minYear + "-" + maxYear
		}
	}

	out.HighlightFindings = buildCTHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func ctSearch(ctx context.Context, client *http.Client, mode, query string, limit int, statusFilter string) ([]CTStudy, error) {
	params := url.Values{}
	switch mode {
	case "condition":
		params.Set("query.cond", query)
	case "sponsor":
		params.Set("query.spons", query)
	case "pi_name":
		params.Set("query.term", "AREA[OverallOfficialName]"+query)
	case "text":
		params.Set("query.term", query)
	default:
		return nil, fmt.Errorf("unknown mode '%s' — use one of: condition, sponsor, pi_name, text, nct_lookup", mode)
	}
	params.Set("pageSize", fmt.Sprintf("%d", limit))
	params.Set("format", "json")
	params.Set("sort", "LastUpdatePostDate:desc")
	if statusFilter != "" {
		params.Set("filter.overallStatus", statusFilter)
	}

	endpoint := "https://clinicaltrials.gov/api/v2/studies?" + params.Encode()
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clinicaltrials: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("clinicaltrials %d: %s", resp.StatusCode, string(body))
	}
	var raw ctRawResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := []CTStudy{}
	for _, r := range raw.Studies {
		out = append(out, materializeCTStudy(&r))
	}
	return out, nil
}

func ctFetchByNCT(ctx context.Context, client *http.Client, nctID string) (*CTStudy, error) {
	endpoint := "https://clinicaltrials.gov/api/v2/studies/" + url.PathEscape(strings.ToUpper(nctID)) + "?format=json"
	req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/0.1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ct %d", resp.StatusCode)
	}
	var raw ctRawStudy
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.ProtocolSection.IdentificationModule.NCTID == "" {
		return nil, nil
	}
	s := materializeCTStudy(&raw)
	return &s, nil
}

func materializeCTStudy(r *ctRawStudy) CTStudy {
	ps := r.ProtocolSection
	s := CTStudy{
		NCTID:         ps.IdentificationModule.NCTID,
		BriefTitle:    ps.IdentificationModule.BriefTitle,
		OfficialTitle: ps.IdentificationModule.OfficialTitle,
		Status:        ps.StatusModule.OverallStatus,
		StartDate:     ps.StatusModule.StartDateStruct.Date,
		CompletionDate: ps.StatusModule.CompletionDateStruct.Date,
		Sponsor:       ps.SponsorCollaboratorsModule.LeadSponsor.Name,
		BriefSummary:  hfTruncate(ps.DescriptionModule.BriefSummary, 600),
		StudyType:     ps.DesignModule.StudyType,
		Phases:        ps.DesignModule.Phases,
		Enrollment:    ps.DesignModule.EnrollmentInfo.Count,
		Conditions:    ps.ConditionsModule.Conditions,
	}
	for _, c := range ps.SponsorCollaboratorsModule.Collaborators {
		s.Collaborators = append(s.Collaborators, c.Name)
	}
	for _, intv := range ps.ArmsInterventionsModule.Interventions {
		if intv.Name != "" {
			s.Interventions = append(s.Interventions, fmt.Sprintf("%s: %s", intv.Type, intv.Name))
		}
	}
	for _, o := range ps.ContactsLocationsModule.OverallOfficials {
		s.OverallOfficials = append(s.OverallOfficials, CTOfficial{
			Name:        o.Name,
			Affiliation: o.Affiliation,
			Role:        o.Role,
		})
	}
	for _, l := range ps.ContactsLocationsModule.Locations {
		s.Locations = append(s.Locations, CTLocation{
			Facility: l.Facility,
			City:     l.City,
			State:    l.State,
			Country:  l.Country,
		})
	}
	if len(s.Locations) > 50 {
		s.Locations = s.Locations[:50]
	}
	s.URL = "https://clinicaltrials.gov/study/" + s.NCTID
	return s
}

func buildCTHighlights(o *CTSearchOutput) []string {
	hi := []string{}
	hi = append(hi, fmt.Sprintf("✓ %d clinical trials returned for mode=%s query='%s'", o.Returned, o.Mode, o.Query))
	if o.YearRange != "" {
		hi = append(hi, "start-date year range: "+o.YearRange)
	}
	if o.TotalEnrollment > 0 {
		hi = append(hi, fmt.Sprintf("📊 total enrollment across returned trials: %d participants", o.TotalEnrollment))
	}
	if len(o.TopSponsors) > 0 {
		topS := []string{}
		for _, s := range o.TopSponsors[:min2(5, len(o.TopSponsors))] {
			topS = append(topS, fmt.Sprintf("%s (%dx)", s.Sponsor, s.TrialCount))
		}
		hi = append(hi, "🏛 top sponsors: "+strings.Join(topS, " | "))
	}
	if len(o.TopPIs) > 0 {
		topP := []string{}
		for _, p := range o.TopPIs[:min2(5, len(o.TopPIs))] {
			suffix := ""
			if p.Affiliation != "" {
				suffix = " @ " + p.Affiliation
			}
			topP = append(topP, fmt.Sprintf("%s (%dx)%s", p.Name, p.TrialCount, suffix))
		}
		hi = append(hi, "🧪 top PIs (per-trial affiliation snapshot): "+strings.Join(topP, " | "))
	}
	if len(o.TopConditions) > 0 {
		topC := []string{}
		for _, c := range o.TopConditions[:min2(6, len(o.TopConditions))] {
			topC = append(topC, fmt.Sprintf("%s (%d)", c.Condition, c.TrialCount))
		}
		hi = append(hi, "🦠 top conditions: "+strings.Join(topC, ", "))
	}
	if len(o.StatusBreakdown) > 0 {
		parts := []string{}
		for _, s := range o.StatusBreakdown {
			parts = append(parts, fmt.Sprintf("%s=%d", s.Status, s.TrialCount))
		}
		hi = append(hi, "status: "+strings.Join(parts, ", "))
	}
	if len(o.UniqueSponsorCountries) > 1 {
		hi = append(hi, fmt.Sprintf("🌍 multi-country trial network: %d countries — %s", len(o.UniqueSponsorCountries), strings.Join(o.UniqueSponsorCountries, ", ")))
	}
	return hi
}
