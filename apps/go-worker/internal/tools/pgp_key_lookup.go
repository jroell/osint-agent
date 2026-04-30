package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type PGPKeyResult struct {
	Server          string   `json:"server"`
	Fingerprint     string   `json:"fingerprint,omitempty"`
	KeyID           string   `json:"key_id,omitempty"`
	Algorithm       string   `json:"algorithm,omitempty"`
	BitLength       string   `json:"bit_length,omitempty"`
	Created         string   `json:"created,omitempty"`
	Expires         string   `json:"expires,omitempty"`
	UserIDs         []string `json:"user_ids,omitempty"`
	IdentityEmails  []string `json:"identity_emails,omitempty"`
	IdentityNames   []string `json:"identity_names,omitempty"`
	Found           bool     `json:"found"`
	RawResponse     string   `json:"raw_excerpt,omitempty"`
}

type PGPKeyLookupOutput struct {
	Query           string         `json:"query"`
	Results         []PGPKeyResult `json:"results"`
	UniqueEmails    []string       `json:"unique_emails_across_servers,omitempty"`
	UniqueNames     []string       `json:"unique_names_across_servers,omitempty"`
	UniqueKeyIDs    []string       `json:"unique_key_ids,omitempty"`
	Source          string         `json:"source"`
	TookMs          int64          `json:"tookMs"`
	Note            string         `json:"note,omitempty"`
}

// Public PGP keyservers — list ordered by reliability.
var pgpKeyservers = []string{
	"https://keys.openpgp.org",  // modern, validated, uid-search
	"https://keyserver.ubuntu.com",
	"https://pgp.mit.edu",
}

// PGPKeyLookup queries multiple public PGP keyservers in parallel for keys
// matching an email, name, or fingerprint. Returns:
//   - Fingerprints across servers (for cross-validation)
//   - All UIDs (User IDs) — each contains a name + email pair
//   - Aggregated unique emails/names across servers
//
// ER use case: given an email, find ALL alternate emails in the same key's
// UIDs (people often add personal+work emails to one key). Reveals identity
// correlation a single email lookup can't.
//
// Free, no key. HKP protocol; we use the HTTPS variant.
func PGPKeyLookup(ctx context.Context, input map[string]any) (*PGPKeyLookupOutput, error) {
	q, _ := input["query"].(string)
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, errors.New("input.query required (email, name, or fingerprint)")
	}

	start := time.Now()
	out := &PGPKeyLookupOutput{Query: q, Source: "pgp-keyservers"}

	results := make([]PGPKeyResult, len(pgpKeyservers))
	var wg sync.WaitGroup
	for i, srv := range pgpKeyservers {
		wg.Add(1)
		go func(idx int, server string) {
			defer wg.Done()
			results[idx] = pgpQuerySingleServer(ctx, server, q)
		}(i, srv)
	}
	wg.Wait()

	emailSet := map[string]bool{}
	nameSet := map[string]bool{}
	keyIDSet := map[string]bool{}
	for _, r := range results {
		out.Results = append(out.Results, r)
		for _, e := range r.IdentityEmails {
			emailSet[strings.ToLower(e)] = true
		}
		for _, n := range r.IdentityNames {
			nameSet[n] = true
		}
		if r.KeyID != "" {
			keyIDSet[r.KeyID] = true
		}
	}
	for e := range emailSet {
		out.UniqueEmails = append(out.UniqueEmails, e)
	}
	for n := range nameSet {
		out.UniqueNames = append(out.UniqueNames, n)
	}
	for k := range keyIDSet {
		out.UniqueKeyIDs = append(out.UniqueKeyIDs, k)
	}
	sort.Strings(out.UniqueEmails)
	sort.Strings(out.UniqueNames)
	sort.Strings(out.UniqueKeyIDs)
	out.TookMs = time.Since(start).Milliseconds()

	totalFound := 0
	for _, r := range out.Results {
		if r.Found {
			totalFound++
		}
	}
	if totalFound == 0 {
		out.Note = "No PGP keys found across queried servers. The user may not publish a PGP key, or the email/name may not be on a key UID."
	} else if len(out.UniqueEmails) > 1 {
		out.Note = fmt.Sprintf("⚠️  Multiple emails (%d) found on the same key — these are alternate identities of the same person", len(out.UniqueEmails))
	}
	return out, nil
}

var pgpUIDRE = regexp.MustCompile(`uid:[^:]*:(\d+):(\d*):([^:]+):`)
// HKP "vindex" format: pub:<keyid>:<algo>:<bits>:<created>:<expires>:<flags>
var pgpPubRE = regexp.MustCompile(`pub:([0-9A-Fa-f]+):(\d+):(\d+):(\d*):(\d*):`)
// Email extractor from UID strings (which look like "Name <email@host>")
var pgpEmailInUIDRE = regexp.MustCompile(`<([^<>]+@[^<>]+)>`)

func pgpQuerySingleServer(ctx context.Context, server, q string) PGPKeyResult {
	rec := PGPKeyResult{Server: server}
	endpoint := fmt.Sprintf("%s/pks/lookup?op=index&options=mr&search=%s", server, url.QueryEscape(q))
	cctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "osint-agent/pgp-lookup")
	req.Header.Set("Accept", "*/*")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rec
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	bodyStr := string(body)
	rec.RawResponse = truncate(bodyStr, 800)

	if resp.StatusCode == 404 || resp.StatusCode == 501 {
		return rec
	}
	if resp.StatusCode != 200 || strings.Contains(bodyStr, "No results") || strings.Contains(bodyStr, "not found") {
		return rec
	}

	// Parse machine-readable HKP response.
	if m := pgpPubRE.FindStringSubmatch(bodyStr); len(m) >= 6 {
		rec.Found = true
		rec.KeyID = m[1]
		rec.Algorithm = m[2]
		rec.BitLength = m[3]
		if m[4] != "" {
			if t, err := time.Parse("2006-01-02", time.Unix(parseInt(m[4]), 0).UTC().Format("2006-01-02")); err == nil {
				rec.Created = t.Format("2006-01-02")
			} else {
				rec.Created = m[4]
			}
		}
		if m[5] != "" {
			if t, err := time.Parse("2006-01-02", time.Unix(parseInt(m[5]), 0).UTC().Format("2006-01-02")); err == nil {
				rec.Expires = t.Format("2006-01-02")
			} else {
				rec.Expires = m[5]
			}
		}
	}

	// Extract UIDs (one per line in mr format)
	uidSet := map[string]bool{}
	emailSet := map[string]bool{}
	nameSet := map[string]bool{}
	for _, line := range strings.Split(bodyStr, "\n") {
		if !strings.HasPrefix(line, "uid:") {
			continue
		}
		fields := strings.SplitN(line, ":", 6)
		if len(fields) < 2 {
			continue
		}
		uid := fields[1]
		// URL-decode the UID
		if decoded, err := url.QueryUnescape(uid); err == nil {
			uid = decoded
		}
		uid = strings.TrimSpace(uid)
		if uid == "" || uidSet[uid] {
			continue
		}
		uidSet[uid] = true
		rec.UserIDs = append(rec.UserIDs, uid)
		// Extract email
		if em := pgpEmailInUIDRE.FindStringSubmatch(uid); len(em) >= 2 {
			emailSet[strings.ToLower(em[1])] = true
			// Name is everything before <
			if idx := strings.Index(uid, "<"); idx > 0 {
				name := strings.TrimSpace(uid[:idx])
				if name != "" {
					nameSet[name] = true
				}
			}
		}
	}
	for e := range emailSet {
		rec.IdentityEmails = append(rec.IdentityEmails, e)
	}
	for n := range nameSet {
		rec.IdentityNames = append(rec.IdentityNames, n)
	}
	sort.Strings(rec.IdentityEmails)
	sort.Strings(rec.IdentityNames)
	if len(rec.UserIDs) > 0 || rec.KeyID != "" {
		rec.Found = true
	}
	return rec
}

func parseInt(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
