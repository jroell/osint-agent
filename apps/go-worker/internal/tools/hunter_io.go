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

type HunterEmail struct {
	Value      string   `json:"value"`
	Type       string   `json:"type,omitempty"`         // "personal" | "generic"
	Confidence int      `json:"confidence,omitempty"`
	FirstName  string   `json:"first_name,omitempty"`
	LastName   string   `json:"last_name,omitempty"`
	Position   string   `json:"position,omitempty"`
	Department string   `json:"department,omitempty"`
	Seniority  string   `json:"seniority,omitempty"`
	LinkedIn   string   `json:"linkedin,omitempty"`
	Twitter    string   `json:"twitter,omitempty"`
	Phone      string   `json:"phone_number,omitempty"`
	Sources    []string `json:"sources,omitempty"`
}

type HunterIOOutput struct {
	Domain         string        `json:"domain"`
	Organization   string        `json:"organization,omitempty"`
	Industry       string        `json:"industry,omitempty"`
	Disposable     bool          `json:"disposable,omitempty"`
	Webmail        bool          `json:"webmail,omitempty"`
	Pattern        string        `json:"pattern,omitempty"`     // e.g. "{first}.{last}"
	EmailsFound    int           `json:"emails_found"`
	Emails         []HunterEmail `json:"emails"`
	Source         string        `json:"source"`
	TookMs         int64         `json:"tookMs"`
}

// HunterIOEmailFinder searches Hunter.io's database of public emails
// associated with a domain. The free tier is 25 searches/month.
func HunterIOEmailFinder(ctx context.Context, input map[string]any) (*HunterIOOutput, error) {
	domain, _ := input["domain"].(string)
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return nil, errors.New("input.domain required")
	}
	apiKey := os.Getenv("HUNTER_IO_API_KEY")
	if apiKey == "" {
		return nil, errors.New("HUNTER_IO_API_KEY env var required (free tier: 25 searches/month, https://hunter.io/users/sign_up)")
	}
	limit := 25
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	dept, _ := input["department"].(string)
	seniority, _ := input["seniority"].(string)

	start := time.Now()
	args := url.Values{}
	args.Set("domain", domain)
	args.Set("limit", fmt.Sprint(limit))
	args.Set("api_key", apiKey)
	if dept != "" {
		args.Set("department", dept)
	}
	if seniority != "" {
		args.Set("seniority", seniority)
	}
	endpoint := "https://api.hunter.io/v2/domain-search?" + args.Encode()
	body, err := httpGetJSON(ctx, endpoint, 20*time.Second)
	if err != nil {
		return nil, fmt.Errorf("hunter.io: %w", err)
	}
	var resp struct {
		Data struct {
			Domain       string `json:"domain"`
			Organization string `json:"organization"`
			Industry     string `json:"industry"`
			Disposable   bool   `json:"disposable"`
			Webmail      bool   `json:"webmail"`
			Pattern      string `json:"pattern"`
			Emails       []struct {
				Value      string `json:"value"`
				Type       string `json:"type"`
				Confidence int    `json:"confidence"`
				FirstName  string `json:"first_name"`
				LastName   string `json:"last_name"`
				Position   string `json:"position"`
				Department string `json:"department"`
				Seniority  string `json:"seniority"`
				LinkedIn   string `json:"linkedin"`
				Twitter    string `json:"twitter"`
				Phone      string `json:"phone_number"`
				Sources    []struct {
					Domain string `json:"domain"`
					URI    string `json:"uri"`
				} `json:"sources"`
			} `json:"emails"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("hunter.io parse: %w", err)
	}
	out := &HunterIOOutput{
		Domain: resp.Data.Domain, Organization: resp.Data.Organization,
		Industry: resp.Data.Industry, Disposable: resp.Data.Disposable,
		Webmail: resp.Data.Webmail, Pattern: resp.Data.Pattern,
		EmailsFound: len(resp.Data.Emails),
		Source:      "api.hunter.io",
		TookMs:      time.Since(start).Milliseconds(),
	}
	for _, e := range resp.Data.Emails {
		he := HunterEmail{
			Value: e.Value, Type: e.Type, Confidence: e.Confidence,
			FirstName: e.FirstName, LastName: e.LastName, Position: e.Position,
			Department: e.Department, Seniority: e.Seniority,
			LinkedIn: e.LinkedIn, Twitter: e.Twitter, Phone: e.Phone,
		}
		for _, s := range e.Sources {
			he.Sources = append(he.Sources, s.URI)
		}
		out.Emails = append(out.Emails, he)
	}
	return out, nil
}
