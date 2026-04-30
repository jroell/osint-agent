package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PubChemCompoundLookup wraps NCBI PubChem's free no-auth PUG REST API.
// 100M+ chemical compounds with canonical structural notation (SMILES),
// hash identifiers (InChIKey), IUPAC nomenclature, lipophilicity (XLogP),
// and extensive synonym lists.
//
// Why this matters for OSINT:
//   - Drug name resolution: "fentanyl" → CID 3345, lipophilicity 4.05,
//     697-synonym list including street-name variants and CAS numbers.
//   - Forensic toxicology: cross-reference compound from a chemical
//     formula or InChIKey extracted from a paper / product label.
//   - Controlled substance identification: CAS numbers from synonyms can
//     be cross-referenced with DEA scheduling data.
//   - Pharmaceutical ER: drug brand → API → competitor analogs (via
//     SMILES similarity, future enhancement).
//
// Pairs with `openfda_search` (FDA drug labels reference compounds by
// name) and `clinicaltrials_search` (drug-X trials).
//
// Single mode: lookup by name OR CID OR InChIKey → comprehensive record.

type PubChemCompound struct {
	CID                  int64    `json:"cid"`
	Name                 string   `json:"primary_name,omitempty"`     // first synonym
	IUPACName            string   `json:"iupac_name,omitempty"`
	MolecularFormula     string   `json:"molecular_formula,omitempty"`
	MolecularWeight      string   `json:"molecular_weight,omitempty"`
	CanonicalSMILES      string   `json:"canonical_smiles,omitempty"`
	InChIKey             string   `json:"inchi_key,omitempty"`
	XLogP                float64  `json:"xlogp,omitempty"`           // lipophilicity (drug-likeness)
	CASNumbers           []string `json:"cas_numbers,omitempty"`
	Synonyms             []string `json:"synonyms,omitempty"`        // capped at 50
	TotalSynonymCount    int      `json:"total_synonym_count,omitempty"`
	URL                  string   `json:"url,omitempty"`
}

type PubChemCompoundLookupOutput struct {
	Mode              string             `json:"mode"`
	Query             string             `json:"query,omitempty"`
	Compound          *PubChemCompound   `json:"compound,omitempty"`

	HighlightFindings []string           `json:"highlight_findings"`
	Source            string             `json:"source"`
	TookMs            int64              `json:"tookMs"`
	Note              string             `json:"note,omitempty"`
}

// CAS number regex: 1-7 digits + dash + 2 digits + dash + check digit
var casRegex = regexp.MustCompile(`^\d{1,7}-\d{2}-\d$`)

// InChIKey: 14 chars + dash + 10 chars + dash + 1 char (e.g. "BSYNRYMUTXBXSQ-UHFFFAOYSA-N")
var inchiKeyRegex = regexp.MustCompile(`^[A-Z]{14}-[A-Z]{10}-[A-Z]$`)

func PubChemCompoundLookup(ctx context.Context, input map[string]any) (*PubChemCompoundLookupOutput, error) {
	out := &PubChemCompoundLookupOutput{
		Mode:   "lookup",
		Source: "pubchem.ncbi.nlm.nih.gov/rest/pug",
	}
	start := time.Now()
	cli := &http.Client{Timeout: 30 * time.Second}

	// Resolve CID from name / CID / InChIKey
	var cid int64
	var inputType string
	if v, ok := input["cid"]; ok {
		cidStr := fmt.Sprintf("%v", v)
		if n, err := strconv.ParseInt(strings.TrimSpace(cidStr), 10, 64); err == nil && n > 0 {
			cid = n
			inputType = "cid"
		}
	}
	if cid == 0 {
		query, _ := input["query"].(string)
		query = strings.TrimSpace(query)
		if name, ok := input["name"].(string); ok && strings.TrimSpace(name) != "" {
			query = strings.TrimSpace(name)
		}
		if inchi, ok := input["inchi_key"].(string); ok && strings.TrimSpace(inchi) != "" {
			query = strings.TrimSpace(inchi)
		}
		if query == "" {
			return nil, fmt.Errorf("input.query, input.name, input.cid, or input.inchi_key required")
		}

		// Auto-detect input type: InChIKey, numeric CID, or name
		if inchiKeyRegex.MatchString(query) {
			inputType = "inchikey"
		} else if n, err := strconv.ParseInt(query, 10, 64); err == nil && n > 0 {
			cid = n
			inputType = "cid"
		} else {
			inputType = "name"
		}

		// If not a CID yet, resolve query → CID
		if cid == 0 {
			endpoint := fmt.Sprintf("https://pubchem.ncbi.nlm.nih.gov/rest/pug/compound/%s/%s/cids/JSON",
				inputType, url.PathEscape(query))
			body, err := pubchemGet(ctx, cli, endpoint)
			if err != nil {
				return nil, err
			}
			var raw struct {
				IdentifierList struct {
					CID []int64 `json:"CID"`
				} `json:"IdentifierList"`
			}
			if err := json.Unmarshal(body, &raw); err != nil {
				return nil, fmt.Errorf("pubchem CID resolve decode: %w", err)
			}
			if len(raw.IdentifierList.CID) == 0 {
				out.Note = fmt.Sprintf("no PubChem CID found for %s '%s'", inputType, query)
				out.HighlightFindings = []string{out.Note}
				out.TookMs = time.Since(start).Milliseconds()
				return out, nil
			}
			cid = raw.IdentifierList.CID[0]
		}
		out.Query = inputType + "=" + query
	} else {
		out.Query = fmt.Sprintf("cid=%d", cid)
	}

	// Now fetch properties + synonyms for the CID
	c := &PubChemCompound{
		CID: cid,
		URL: fmt.Sprintf("https://pubchem.ncbi.nlm.nih.gov/compound/%d", cid),
	}

	// Properties
	propsURL := fmt.Sprintf("https://pubchem.ncbi.nlm.nih.gov/rest/pug/compound/cid/%d/property/MolecularFormula,MolecularWeight,CanonicalSMILES,InChIKey,XLogP,IUPACName/JSON", cid)
	propsBody, err := pubchemGet(ctx, cli, propsURL)
	if err != nil {
		return nil, fmt.Errorf("pubchem properties: %w", err)
	}
	var propsRaw struct {
		PropertyTable struct {
			Properties []map[string]any `json:"Properties"`
		} `json:"PropertyTable"`
	}
	if err := json.Unmarshal(propsBody, &propsRaw); err != nil {
		return nil, fmt.Errorf("pubchem properties decode: %w", err)
	}
	if len(propsRaw.PropertyTable.Properties) > 0 {
		p := propsRaw.PropertyTable.Properties[0]
		c.MolecularFormula = gtString(p, "MolecularFormula")
		c.MolecularWeight = gtString(p, "MolecularWeight")
		// PubChem uses "ConnectivitySMILES" in newer responses, "CanonicalSMILES" in older
		c.CanonicalSMILES = gtString(p, "ConnectivitySMILES")
		if c.CanonicalSMILES == "" {
			c.CanonicalSMILES = gtString(p, "CanonicalSMILES")
		}
		c.InChIKey = gtString(p, "InChIKey")
		c.XLogP = gtFloat(p, "XLogP")
		c.IUPACName = gtString(p, "IUPACName")
	}

	// Synonyms
	synURL := fmt.Sprintf("https://pubchem.ncbi.nlm.nih.gov/rest/pug/compound/cid/%d/synonyms/JSON", cid)
	synBody, err := pubchemGet(ctx, cli, synURL)
	if err == nil {
		var synRaw struct {
			InformationList struct {
				Information []struct {
					CID     int64    `json:"CID"`
					Synonym []string `json:"Synonym"`
				} `json:"Information"`
			} `json:"InformationList"`
		}
		if json.Unmarshal(synBody, &synRaw) == nil && len(synRaw.InformationList.Information) > 0 {
			info := synRaw.InformationList.Information[0]
			c.TotalSynonymCount = len(info.Synonym)
			if len(info.Synonym) > 0 {
				c.Name = info.Synonym[0]
			}
			// Cap synonyms at 50 for response size, prioritize short/clean names
			synonymCap := 50
			if l, ok := input["max_synonyms"].(float64); ok && l > 0 && l <= 500 {
				synonymCap = int(l)
			}
			if len(info.Synonym) > synonymCap {
				c.Synonyms = info.Synonym[:synonymCap]
			} else {
				c.Synonyms = info.Synonym
			}
			// Extract CAS numbers from synonyms
			for _, s := range info.Synonym {
				if casRegex.MatchString(s) {
					c.CASNumbers = append(c.CASNumbers, s)
				}
			}
		}
	}

	out.Compound = c
	out.HighlightFindings = buildPubChemHighlights(out)
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func pubchemGet(ctx context.Context, cli *http.Client, urlStr string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	req.Header.Set("User-Agent", "osint-agent/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pubchem: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("pubchem: not found (404)")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("pubchem HTTP %d: %s", resp.StatusCode, hfTruncate(string(body), 200))
	}
	return body, nil
}

func buildPubChemHighlights(o *PubChemCompoundLookupOutput) []string {
	hi := []string{}
	if o.Compound == nil {
		hi = append(hi, "✗ no compound data")
		return hi
	}
	c := o.Compound
	hi = append(hi, fmt.Sprintf("✓ CID %d — %s", c.CID, c.Name))
	if c.IUPACName != "" {
		hi = append(hi, "  IUPAC: "+c.IUPACName)
	}
	props := []string{}
	if c.MolecularFormula != "" {
		props = append(props, "formula "+c.MolecularFormula)
	}
	if c.MolecularWeight != "" {
		props = append(props, "MW "+c.MolecularWeight)
	}
	if c.XLogP != 0 {
		props = append(props, fmt.Sprintf("XLogP %.2f", c.XLogP))
	}
	if len(props) > 0 {
		hi = append(hi, "  "+strings.Join(props, " · "))
	}
	if c.InChIKey != "" {
		hi = append(hi, "  InChIKey: "+c.InChIKey)
	}
	if c.CanonicalSMILES != "" {
		hi = append(hi, "  SMILES: "+hfTruncate(c.CanonicalSMILES, 100))
	}
	if len(c.CASNumbers) > 0 {
		hi = append(hi, "  CAS#: "+strings.Join(c.CASNumbers, ", "))
	}
	hi = append(hi, fmt.Sprintf("  synonyms: %d total (showing %d)", c.TotalSynonymCount, len(c.Synonyms)))
	if len(c.Synonyms) > 0 {
		topSyn := c.Synonyms
		if len(topSyn) > 8 {
			topSyn = topSyn[:8]
		}
		hi = append(hi, "  top names: "+strings.Join(topSyn, " · "))
	}
	hi = append(hi, "  url: "+c.URL)
	return hi
}
