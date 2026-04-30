package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type FECContribution struct {
	ContributorName     string  `json:"contributor_name"`
	ContributorFirstName string `json:"first_name,omitempty"`
	ContributorLastName  string `json:"last_name,omitempty"`
	ContributorEmployer string  `json:"contributor_employer,omitempty"`
	ContributorOccupation string `json:"contributor_occupation,omitempty"`
	ContributorCity     string  `json:"contributor_city,omitempty"`
	ContributorState    string  `json:"contributor_state,omitempty"`
	ContributorZip      string  `json:"contributor_zip,omitempty"`
	ReceiptAmount       float64 `json:"receipt_amount"`
	ReceiptDate         string  `json:"receipt_date,omitempty"`
	CommitteeID         string  `json:"committee_id,omitempty"`
	CommitteeName       string  `json:"committee_name,omitempty"`
	CommitteeType       string  `json:"committee_type,omitempty"`
	CandidateName       string  `json:"candidate_name,omitempty"`
	CandidateOffice     string  `json:"candidate_office,omitempty"`
	CandidatePartyAffiliation string `json:"candidate_party,omitempty"`
}

type FECEmployerAggregate struct {
	Employer      string  `json:"employer"`
	Count         int     `json:"contribution_count"`
	TotalAmount   float64 `json:"total_amount_usd"`
}

type FECCommitteeAggregate struct {
	Name        string  `json:"committee_name"`
	ID          string  `json:"committee_id"`
	Type        string  `json:"committee_type,omitempty"`
	Count       int     `json:"contribution_count"`
	TotalAmount float64 `json:"total_amount_usd"`
}

type FECDonationsOutput struct {
	Query                 string                  `json:"query"`
	TotalRecords          int                     `json:"total_records"`
	Returned              int                     `json:"returned"`
	Contributions         []FECContribution       `json:"contributions"`
	TotalAmountUSD        float64                 `json:"total_amount_usd"`
	UniqueContributorCount int                    `json:"unique_contributors"`
	UniqueEmployerCount   int                     `json:"unique_employers"`
	UniqueRecipientCount  int                     `json:"unique_recipient_committees"`
	TopEmployers          []FECEmployerAggregate  `json:"top_employers,omitempty"`
	TopRecipients         []FECCommitteeAggregate `json:"top_recipient_committees,omitempty"`
	UniqueOccupations     []string                `json:"unique_occupations,omitempty"`
	HighlightFindings     []string                `json:"highlight_findings"`
	Source                string                  `json:"source"`
	TookMs                int64                   `json:"tookMs"`
	Note                  string                  `json:"note,omitempty"`
}

// FECDonationsLookup queries the FEC's open data API (api.open.fec.gov) for
// individual political contribution records. Every donation $200+ to a federal
// committee is publicly disclosed with: donor name, employer, occupation,
// city/state/zip, amount, date, and recipient committee.
//
// Use cases:
//   - Strong ER signal: same person + same employer in donations + GitHub
//     org_intel + LinkedIn = high-confidence match
//   - Political alignment without LinkedIn (donations reveal partisan tilt)
//   - Reverse: who donates to candidate X? (employer aggregations)
//   - Cross-reference with `wikidata_entity_lookup` for public figures
//
// Auth: free api.data.gov key (signup at api.data.gov/signup; DEMO_KEY works
// for low-volume testing — 30 req/hour). Set FEC_API_KEY env var.
func FECDonationsLookup(ctx context.Context, input map[string]any) (*FECDonationsOutput, error) {
	contributorName, _ := input["contributor_name"].(string)
	contributorName = strings.TrimSpace(contributorName)

	committeeID, _ := input["committee_id"].(string)
	committeeID = strings.TrimSpace(committeeID)

	employer, _ := input["employer"].(string)
	employer = strings.TrimSpace(employer)

	if contributorName == "" && committeeID == "" && employer == "" {
		return nil, errors.New("at least one of: input.contributor_name, input.committee_id, or input.employer required")
	}

	state, _ := input["state"].(string)
	state = strings.TrimSpace(strings.ToUpper(state))

	limit := 30
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 100 {
		limit = int(v)
	}

	apiKey := os.Getenv("FEC_API_KEY")
	if apiKey == "" {
		apiKey = "DEMO_KEY"
	}

	start := time.Now()
	out := &FECDonationsOutput{
		Source: "api.open.fec.gov",
	}

	params := url.Values{}
	params.Set("api_key", apiKey)
	params.Set("per_page", fmt.Sprintf("%d", limit))
	params.Set("sort", "-contribution_receipt_date")
	if contributorName != "" {
		params.Set("contributor_name", contributorName)
		out.Query = contributorName
	}
	if committeeID != "" {
		params.Set("committee_id", committeeID)
		if out.Query == "" {
			out.Query = "committee:" + committeeID
		}
	}
	if employer != "" {
		params.Set("contributor_employer", employer)
		if out.Query == "" {
			out.Query = "employer:" + employer
		}
	}
	if state != "" {
		params.Set("contributor_state", state)
	}

	endpoint := "https://api.open.fec.gov/v1/schedules/schedule_a/?" + params.Encode()
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/fec-donations")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fec fetch: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 429 {
		return nil, errors.New("fec rate-limited (DEMO_KEY is 30 req/hour). Set FEC_API_KEY env var with a free key from https://api.data.gov/signup")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fec status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		Pagination struct {
			Count int `json:"count"`
			Pages int `json:"pages"`
		} `json:"pagination"`
		Results []struct {
			ContributorName       string  `json:"contributor_name"`
			ContributorFirstName  string  `json:"contributor_first_name"`
			ContributorLastName   string  `json:"contributor_last_name"`
			ContributorEmployer   string  `json:"contributor_employer"`
			ContributorOccupation string  `json:"contributor_occupation"`
			ContributorCity       string  `json:"contributor_city"`
			ContributorState      string  `json:"contributor_state"`
			ContributorZip        string  `json:"contributor_zip"`
			ReceiptAmount         float64 `json:"contribution_receipt_amount"`
			ReceiptDate           string  `json:"contribution_receipt_date"`
			CommitteeID           string  `json:"committee_id"`
			Committee struct {
				Name             string `json:"name"`
				CommitteeType    string `json:"committee_type"`
				CommitteeTypeFull string `json:"committee_type_full"`
			} `json:"committee"`
			CandidateName     string `json:"candidate_name"`
			CandidateOfficeFull string `json:"candidate_office_full"`
			ContributorAggregateYTD float64 `json:"contributor_aggregate_ytd"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("fec parse: %w", err)
	}

	out.TotalRecords = parsed.Pagination.Count

	contributorSet := map[string]bool{}
	employerCounts := map[string]int{}
	employerAmounts := map[string]float64{}
	committeeCounts := map[string]int{}
	committeeAmounts := map[string]float64{}
	committeeMeta := map[string]FECCommitteeAggregate{}
	occupationSet := map[string]bool{}
	totalAmount := 0.0

	for _, r := range parsed.Results {
		c := FECContribution{
			ContributorName:       r.ContributorName,
			ContributorFirstName:  r.ContributorFirstName,
			ContributorLastName:   r.ContributorLastName,
			ContributorEmployer:   r.ContributorEmployer,
			ContributorOccupation: r.ContributorOccupation,
			ContributorCity:       r.ContributorCity,
			ContributorState:      r.ContributorState,
			ContributorZip:        r.ContributorZip,
			ReceiptAmount:         r.ReceiptAmount,
			ReceiptDate:           r.ReceiptDate,
			CommitteeID:           r.CommitteeID,
			CommitteeName:         r.Committee.Name,
			CommitteeType:         r.Committee.CommitteeTypeFull,
			CandidateName:         r.CandidateName,
			CandidateOffice:       r.CandidateOfficeFull,
		}
		out.Contributions = append(out.Contributions, c)
		totalAmount += r.ReceiptAmount

		if r.ContributorName != "" {
			contributorSet[r.ContributorName] = true
		}
		if r.ContributorEmployer != "" {
			employerCounts[r.ContributorEmployer]++
			employerAmounts[r.ContributorEmployer] += r.ReceiptAmount
		}
		if r.ContributorOccupation != "" {
			occupationSet[r.ContributorOccupation] = true
		}
		if r.CommitteeID != "" {
			committeeCounts[r.CommitteeID]++
			committeeAmounts[r.CommitteeID] += r.ReceiptAmount
			if _, ok := committeeMeta[r.CommitteeID]; !ok {
				committeeMeta[r.CommitteeID] = FECCommitteeAggregate{
					Name: r.Committee.Name,
					ID:   r.CommitteeID,
					Type: r.Committee.CommitteeTypeFull,
				}
			}
		}
	}

	out.Returned = len(out.Contributions)
	out.TotalAmountUSD = roundCents(totalAmount)
	out.UniqueContributorCount = len(contributorSet)
	out.UniqueEmployerCount = len(employerCounts)
	out.UniqueRecipientCount = len(committeeMeta)

	for k, c := range employerCounts {
		out.TopEmployers = append(out.TopEmployers, FECEmployerAggregate{
			Employer: k, Count: c, TotalAmount: roundCents(employerAmounts[k]),
		})
	}
	sort.Slice(out.TopEmployers, func(i, j int) bool {
		return out.TopEmployers[i].TotalAmount > out.TopEmployers[j].TotalAmount
	})
	if len(out.TopEmployers) > 10 {
		out.TopEmployers = out.TopEmployers[:10]
	}

	for id, meta := range committeeMeta {
		meta.Count = committeeCounts[id]
		meta.TotalAmount = roundCents(committeeAmounts[id])
		out.TopRecipients = append(out.TopRecipients, meta)
	}
	sort.Slice(out.TopRecipients, func(i, j int) bool {
		return out.TopRecipients[i].TotalAmount > out.TopRecipients[j].TotalAmount
	})
	if len(out.TopRecipients) > 10 {
		out.TopRecipients = out.TopRecipients[:10]
	}

	for o := range occupationSet {
		out.UniqueOccupations = append(out.UniqueOccupations, o)
	}
	sort.Strings(out.UniqueOccupations)

	// Highlights
	highlights := []string{
		fmt.Sprintf("%d total records (%d returned this page); $%.2f total in returned set", out.TotalRecords, out.Returned, out.TotalAmountUSD),
	}
	if out.UniqueContributorCount > 1 {
		highlights = append(highlights, fmt.Sprintf("⚠️  %d distinct contributor name variants — may include namesakes", out.UniqueContributorCount))
	}
	if len(out.TopEmployers) > 0 {
		te := []string{}
		for i, e := range out.TopEmployers {
			if i >= 3 {
				break
			}
			te = append(te, fmt.Sprintf("'%s' ($%.0f, %d×)", e.Employer, e.TotalAmount, e.Count))
		}
		highlights = append(highlights, "top self-reported employers: "+strings.Join(te, "; "))
	}
	if len(out.UniqueOccupations) > 0 {
		head := out.UniqueOccupations
		if len(head) > 5 {
			head = head[:5]
		}
		highlights = append(highlights, "occupations: "+strings.Join(head, ", "))
	}
	if len(out.TopRecipients) > 0 {
		tr := []string{}
		for i, r := range out.TopRecipients {
			if i >= 3 {
				break
			}
			tr = append(tr, fmt.Sprintf("%s ($%.0f)", truncate(r.Name, 35), r.TotalAmount))
		}
		highlights = append(highlights, "top recipient committees: "+strings.Join(tr, "; "))
	}
	if apiKey == "DEMO_KEY" {
		out.Note = "Using DEMO_KEY (30 req/hour). For production, sign up at api.data.gov and set FEC_API_KEY env var."
	}
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func roundCents(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
