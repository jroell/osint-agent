package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type CVEMetric struct {
	Source       string  `json:"source,omitempty"`
	Type         string  `json:"type,omitempty"`
	BaseScore    float64 `json:"base_score,omitempty"`
	Severity     string  `json:"severity,omitempty"`
	VectorString string  `json:"vector_string,omitempty"`
}

type CVEAffectedProduct struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	Version string `json:"version,omitempty"`
}

type EPSSData struct {
	Score       float64 `json:"epss_score"`
	Percentile  float64 `json:"epss_percentile"`
	Date        string  `json:"epss_date,omitempty"`
}

type KEVData struct {
	InCatalog          bool   `json:"in_catalog"`
	VendorProject      string `json:"vendor_project,omitempty"`
	Product            string `json:"product,omitempty"`
	VulnerabilityName  string `json:"vulnerability_name,omitempty"`
	DateAdded          string `json:"date_added,omitempty"`
	ShortDescription   string `json:"short_description,omitempty"`
	RequiredAction     string `json:"required_action,omitempty"`
	DueDate            string `json:"due_date,omitempty"`
	KnownRansomware    string `json:"known_ransomware_use,omitempty"`
	Notes              string `json:"notes,omitempty"`
}

type CVEIntelChainOutput struct {
	CVEID            string               `json:"cve_id"`
	Description      string               `json:"description,omitempty"`
	Published        string               `json:"published,omitempty"`
	LastModified     string               `json:"last_modified,omitempty"`
	NVDStatus        string               `json:"nvd_status,omitempty"`
	Metrics          []CVEMetric          `json:"metrics,omitempty"`
	PrimaryCVSS      float64              `json:"primary_cvss_score,omitempty"`
	PrimarySeverity  string               `json:"primary_severity,omitempty"`
	AffectedProducts []CVEAffectedProduct `json:"affected_products,omitempty"`
	References       []string             `json:"references,omitempty"`
	CWE              []string             `json:"cwe,omitempty"`
	EPSS             *EPSSData            `json:"epss,omitempty"`
	KEV              *KEVData             `json:"kev,omitempty"`
	GithubPoCSearch  string               `json:"github_poc_search_url,omitempty"`
	ExploitDBSearch  string               `json:"exploitdb_search_url,omitempty"`
	OverallSeverity  string               `json:"overall_severity"` // critical | high | medium | low | informational
	Rationale        string               `json:"rationale,omitempty"`
	Source           string               `json:"source"`
	TookMs           int64                `json:"tookMs"`
	Errors           map[string]string    `json:"errors,omitempty"`
}

var cveRE = regexp.MustCompile(`^CVE-\d{4}-\d{4,}$`)

// CVEIntelChain fans out to NVD + EPSS + CISA KEV in parallel for a CVE ID.
// Returns:
//   - NVD: description, CVSS metrics (v3.1/v4), affected products, references, CWE
//   - EPSS: exploit probability score (0-1) + percentile rank
//   - CISA KEV: whether the CVE is in the Known Exploited Vulnerabilities catalog,
//     when it was added, what action CISA requires
//   - GitHub PoC + ExploitDB pre-built search URLs for manual follow-up
//   - Composite "overall_severity" combining CVSS + EPSS + KEV signals
//
// The composite severity is the killer feature:
//   - CRITICAL = in KEV (actively exploited) OR EPSS > 0.5 (high prob exploit)
//   - HIGH = CVSS >= 7.0 OR EPSS > 0.1
//   - MEDIUM = CVSS 4.0-6.9
//   - LOW = CVSS < 4.0
func CVEIntelChain(ctx context.Context, input map[string]any) (*CVEIntelChainOutput, error) {
	cveID, _ := input["cve_id"].(string)
	cveID = strings.ToUpper(strings.TrimSpace(cveID))
	if !cveRE.MatchString(cveID) {
		return nil, errors.New("input.cve_id must be in CVE-YYYY-NNNN format")
	}

	start := time.Now()
	out := &CVEIntelChainOutput{
		CVEID:           cveID,
		Source:          "nvd+epss+cisa-kev",
		Errors:          map[string]string{},
		GithubPoCSearch: fmt.Sprintf("https://github.com/search?q=%s+poc&type=repositories", url.QueryEscape(cveID)),
		ExploitDBSearch: fmt.Sprintf("https://www.exploit-db.com/search?cve=%s", url.QueryEscape(strings.TrimPrefix(cveID, "CVE-"))),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	// 1. NVD
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := cveFetchNVD(ctx, cveID, out)
		mu.Lock()
		if err != nil {
			out.Errors["nvd"] = err.Error()
		}
		mu.Unlock()
	}()

	// 2. EPSS
	wg.Add(1)
	go func() {
		defer wg.Done()
		epss, err := cveFetchEPSS(ctx, cveID)
		mu.Lock()
		if err != nil {
			out.Errors["epss"] = err.Error()
		} else {
			out.EPSS = epss
		}
		mu.Unlock()
	}()

	// 3. CISA KEV
	wg.Add(1)
	go func() {
		defer wg.Done()
		kev, err := cveFetchKEV(ctx, cveID)
		mu.Lock()
		if err != nil {
			out.Errors["kev"] = err.Error()
		} else {
			out.KEV = kev
		}
		mu.Unlock()
	}()

	wg.Wait()

	// Composite severity
	rationale := []string{}
	severity := "informational"
	if out.KEV != nil && out.KEV.InCatalog {
		severity = "critical"
		rationale = append(rationale, fmt.Sprintf("⛔ in CISA KEV catalog (added %s) — actively exploited in the wild", out.KEV.DateAdded))
		if out.KEV.KnownRansomware != "" && strings.ToLower(out.KEV.KnownRansomware) == "known" {
			rationale = append(rationale, "ransomware groups known to use this")
		}
	}
	if out.EPSS != nil {
		rationale = append(rationale, fmt.Sprintf("EPSS=%.4f (top %.1f%%)", out.EPSS.Score, (1-out.EPSS.Percentile)*100))
		if out.EPSS.Score > 0.5 && severity != "critical" {
			severity = "critical"
			rationale = append(rationale, "EPSS>0.5 indicates very high near-term exploit probability")
		} else if out.EPSS.Score > 0.1 && (severity == "low" || severity == "medium" || severity == "informational") {
			severity = "high"
		}
	}
	if out.PrimaryCVSS >= 9.0 {
		if severity == "low" || severity == "medium" || severity == "informational" {
			severity = "critical"
		}
		rationale = append(rationale, fmt.Sprintf("CVSS=%.1f (%s)", out.PrimaryCVSS, out.PrimarySeverity))
	} else if out.PrimaryCVSS >= 7.0 {
		if severity == "low" || severity == "medium" || severity == "informational" {
			severity = "high"
		}
		rationale = append(rationale, fmt.Sprintf("CVSS=%.1f (%s)", out.PrimaryCVSS, out.PrimarySeverity))
	} else if out.PrimaryCVSS >= 4.0 {
		if severity == "low" || severity == "informational" {
			severity = "medium"
		}
		rationale = append(rationale, fmt.Sprintf("CVSS=%.1f (%s)", out.PrimaryCVSS, out.PrimarySeverity))
	} else if out.PrimaryCVSS > 0 {
		if severity == "informational" {
			severity = "low"
		}
		rationale = append(rationale, fmt.Sprintf("CVSS=%.1f (%s)", out.PrimaryCVSS, out.PrimarySeverity))
	}
	out.OverallSeverity = severity
	out.Rationale = strings.Join(rationale, "; ")

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func cveFetchNVD(ctx context.Context, cveID string, out *CVEIntelChainOutput) error {
	endpoint := "https://services.nvd.nist.gov/rest/json/cves/2.0?cveId=" + url.QueryEscape(cveID)
	cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/cve-intel")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("nvd status %d", resp.StatusCode)
	}
	var parsed struct {
		Vulnerabilities []struct {
			CVE struct {
				ID           string `json:"id"`
				Published    string `json:"published"`
				LastModified string `json:"lastModified"`
				VulnStatus   string `json:"vulnStatus"`
				Descriptions []struct {
					Lang  string `json:"lang"`
					Value string `json:"value"`
				} `json:"descriptions"`
				Metrics struct {
					CvssMetricV31 []struct {
						Source   string `json:"source"`
						Type     string `json:"type"`
						CvssData struct {
							BaseScore             float64 `json:"baseScore"`
							BaseSeverity          string  `json:"baseSeverity"`
							VectorString          string  `json:"vectorString"`
						} `json:"cvssData"`
					} `json:"cvssMetricV31"`
					CvssMetricV40 []struct {
						Source   string `json:"source"`
						Type     string `json:"type"`
						CvssData struct {
							BaseScore    float64 `json:"baseScore"`
							BaseSeverity string  `json:"baseSeverity"`
							VectorString string  `json:"vectorString"`
						} `json:"cvssData"`
					} `json:"cvssMetricV40"`
				} `json:"metrics"`
				Weaknesses []struct {
					Description []struct {
						Value string `json:"value"`
					} `json:"description"`
				} `json:"weaknesses"`
				References []struct {
					URL string `json:"url"`
				} `json:"references"`
				Configurations []struct {
					Nodes []struct {
						CPEMatch []struct {
							Criteria string `json:"criteria"`
						} `json:"cpeMatch"`
					} `json:"nodes"`
				} `json:"configurations"`
			} `json:"cve"`
		} `json:"vulnerabilities"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("nvd parse: %w", err)
	}
	if len(parsed.Vulnerabilities) == 0 {
		return fmt.Errorf("not found in NVD")
	}
	v := parsed.Vulnerabilities[0].CVE
	out.Published = v.Published
	out.LastModified = v.LastModified
	out.NVDStatus = v.VulnStatus
	for _, d := range v.Descriptions {
		if d.Lang == "en" {
			out.Description = d.Value
			break
		}
	}
	primaryScore := 0.0
	primarySeverity := ""
	// Prefer v4 over v3.1 if present
	for _, m := range v.Metrics.CvssMetricV40 {
		out.Metrics = append(out.Metrics, CVEMetric{
			Source: m.Source, Type: "CVSS:4.0", BaseScore: m.CvssData.BaseScore,
			Severity: m.CvssData.BaseSeverity, VectorString: m.CvssData.VectorString,
		})
		if primaryScore == 0 {
			primaryScore = m.CvssData.BaseScore
			primarySeverity = m.CvssData.BaseSeverity
		}
	}
	for _, m := range v.Metrics.CvssMetricV31 {
		out.Metrics = append(out.Metrics, CVEMetric{
			Source: m.Source, Type: "CVSS:3.1", BaseScore: m.CvssData.BaseScore,
			Severity: m.CvssData.BaseSeverity, VectorString: m.CvssData.VectorString,
		})
		if primaryScore == 0 {
			primaryScore = m.CvssData.BaseScore
			primarySeverity = m.CvssData.BaseSeverity
		}
	}
	out.PrimaryCVSS = primaryScore
	out.PrimarySeverity = primarySeverity
	for _, ref := range v.References {
		if ref.URL != "" {
			out.References = append(out.References, ref.URL)
		}
	}
	if len(out.References) > 15 {
		out.References = out.References[:15]
	}
	for _, w := range v.Weaknesses {
		for _, d := range w.Description {
			if d.Value != "" && d.Value != "NVD-CWE-noinfo" {
				out.CWE = append(out.CWE, d.Value)
			}
		}
	}
	// Affected products via CPE.
	prodSet := map[string]bool{}
	for _, cfg := range v.Configurations {
		for _, n := range cfg.Nodes {
			for _, cp := range n.CPEMatch {
				p := parseCPE(cp.Criteria)
				if p != nil {
					key := p.Vendor + "|" + p.Product + "|" + p.Version
					if !prodSet[key] {
						prodSet[key] = true
						out.AffectedProducts = append(out.AffectedProducts, *p)
					}
				}
			}
		}
	}
	if len(out.AffectedProducts) > 25 {
		out.AffectedProducts = out.AffectedProducts[:25]
	}
	return nil
}

func parseCPE(criteria string) *CVEAffectedProduct {
	// cpe:2.3:a:vendor:product:version:...
	parts := strings.Split(criteria, ":")
	if len(parts) < 6 {
		return nil
	}
	vendor := parts[3]
	product := parts[4]
	version := parts[5]
	if version == "*" || version == "-" {
		version = ""
	}
	return &CVEAffectedProduct{Vendor: vendor, Product: product, Version: version}
}

func cveFetchEPSS(ctx context.Context, cveID string) (*EPSSData, error) {
	endpoint := "https://api.first.org/data/v1/epss?cve=" + url.QueryEscape(cveID)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/epss")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("epss status %d", resp.StatusCode)
	}
	var parsed struct {
		Data []struct {
			CVE        string `json:"cve"`
			EPSS       string `json:"epss"`
			Percentile string `json:"percentile"`
			Date       string `json:"date"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, fmt.Errorf("no EPSS data — CVE may be too new")
	}
	d := parsed.Data[0]
	score := parseFloat(d.EPSS)
	pct := parseFloat(d.Percentile)
	return &EPSSData{Score: score, Percentile: pct, Date: d.Date}, nil
}

// CISA KEV catalog cache (refreshed once per process lifetime — small file).
var (
	kevMu      sync.Mutex
	kevCache   map[string]*KEVData
	kevFetched bool
)

func cveFetchKEV(ctx context.Context, cveID string) (*KEVData, error) {
	kevMu.Lock()
	if kevFetched && kevCache != nil {
		kevMu.Unlock()
		if v, ok := kevCache[cveID]; ok {
			return v, nil
		}
		return &KEVData{InCatalog: false}, nil
	}
	kevMu.Unlock()

	// Fetch and cache
	endpoint := "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/cisa-kev")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("kev status %d", resp.StatusCode)
	}
	var parsed struct {
		Vulnerabilities []struct {
			CVE_ID            string `json:"cveID"`
			VendorProject     string `json:"vendorProject"`
			Product           string `json:"product"`
			VulnerabilityName string `json:"vulnerabilityName"`
			DateAdded         string `json:"dateAdded"`
			ShortDescription  string `json:"shortDescription"`
			RequiredAction    string `json:"requiredAction"`
			DueDate           string `json:"dueDate"`
			KnownRansomware   string `json:"knownRansomwareCampaignUse"`
			Notes             string `json:"notes"`
		} `json:"vulnerabilities"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	cache := map[string]*KEVData{}
	for _, v := range parsed.Vulnerabilities {
		cache[v.CVE_ID] = &KEVData{
			InCatalog:         true,
			VendorProject:     v.VendorProject,
			Product:           v.Product,
			VulnerabilityName: v.VulnerabilityName,
			DateAdded:         v.DateAdded,
			ShortDescription:  v.ShortDescription,
			RequiredAction:    v.RequiredAction,
			DueDate:           v.DueDate,
			KnownRansomware:   v.KnownRansomware,
			Notes:             v.Notes,
		}
	}
	kevMu.Lock()
	kevCache = cache
	kevFetched = true
	kevMu.Unlock()

	if v, ok := cache[cveID]; ok {
		return v, nil
	}
	return &KEVData{InCatalog: false}, nil
}

func parseFloat(s string) float64 {
	var n float64
	var sign float64 = 1
	i := 0
	if i < len(s) && s[i] == '-' {
		sign = -1
		i++
	}
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + float64(s[i]-'0')
	}
	if i < len(s) && s[i] == '.' {
		i++
		mult := 0.1
		for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
			n += float64(s[i]-'0') * mult
			mult *= 0.1
		}
	}
	return n * sign
}
