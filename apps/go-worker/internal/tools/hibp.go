package tools

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// =============================================================================
// pwned_password_check (FREE, k-anonymity, no API key)
// =============================================================================

type PwnedPasswordOutput struct {
	Pwned       bool   `json:"pwned"`
	OccurrenceN int    `json:"occurrence_count"`
	SHA1Prefix  string `json:"sha1_prefix"` // first 5 chars of SHA1, sent to API
	Source      string `json:"source"`
	TookMs      int64  `json:"tookMs"`
	Note        string `json:"note"`
}

// PwnedPasswordCheck queries Troy Hunt's Pwned Passwords k-anonymity API. We
// SHA-1 the password, send only the first 5 hex chars to api.pwnedpasswords.com,
// and search the returned list locally for the remaining suffix. The full
// password is never transmitted.
func PwnedPasswordCheck(ctx context.Context, input map[string]any) (*PwnedPasswordOutput, error) {
	pw, _ := input["password"].(string)
	if pw == "" {
		return nil, errors.New("input.password required")
	}
	start := time.Now()
	sum := sha1.Sum([]byte(pw))
	full := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := full[:5], full[5:]

	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, "https://api.pwnedpasswords.com/range/"+prefix, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	req.Header.Set("Add-Padding", "true") // Cloudflare rec to prevent length-leak
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hibp range fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("hibp returned %d", resp.StatusCode)
	}

	out := &PwnedPasswordOutput{
		SHA1Prefix: prefix,
		Source:     "haveibeenpwned.com (k-anonymity)",
		Note:       "the full password is never transmitted; only the first 5 chars of its SHA-1 hash",
	}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: "<35-hex-suffix>:<count>"
		parts := strings.Split(strings.TrimSpace(line), ":")
		if len(parts) != 2 {
			continue
		}
		if strings.EqualFold(parts[0], suffix) {
			n, _ := strconv.Atoi(parts[1])
			out.Pwned = true
			out.OccurrenceN = n
			break
		}
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

// =============================================================================
// hibp_breach_lookup (PAID, requires HIBP_API_KEY env var, ~$3.95/mo)
// =============================================================================

type HIBPBreach struct {
	Name         string   `json:"Name"`
	Title        string   `json:"Title,omitempty"`
	Domain       string   `json:"Domain,omitempty"`
	BreachDate   string   `json:"BreachDate,omitempty"`
	AddedDate    string   `json:"AddedDate,omitempty"`
	PwnCount     int      `json:"PwnCount,omitempty"`
	Description  string   `json:"Description,omitempty"`
	DataClasses  []string `json:"DataClasses,omitempty"`
	IsVerified   bool     `json:"IsVerified,omitempty"`
	IsSensitive  bool     `json:"IsSensitive,omitempty"`
}

type HIBPBreachOutput struct {
	Account string       `json:"account"`
	Pwned   bool         `json:"pwned"`
	Breaches []HIBPBreach `json:"breaches"`
	Count   int          `json:"count"`
	Source  string       `json:"source"`
	TookMs  int64        `json:"tookMs"`
}

// HIBPBreachLookup queries Have I Been Pwned for breaches an email address has
// appeared in. Requires a paid HIBP_API_KEY (https://haveibeenpwned.com/API/Key).
func HIBPBreachLookup(ctx context.Context, input map[string]any) (*HIBPBreachOutput, error) {
	email, _ := input["email"].(string)
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, errors.New("input.email required")
	}
	key := os.Getenv("HIBP_API_KEY")
	if key == "" {
		return nil, errors.New("HIBP_API_KEY env var required — Have I Been Pwned breached-account API is paid (https://haveibeenpwned.com/API/Key)")
	}
	start := time.Now()
	endpoint := "https://haveibeenpwned.com/api/v3/breachedaccount/" + url.PathEscape(email) + "?truncateResponse=false"
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("hibp-api-key", key)
	req.Header.Set("User-Agent", "osint-agent/0.1.0 (+https://github.com/jroell/osint-agent)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hibp fetch: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	out := &HIBPBreachOutput{
		Account: email,
		Source:  "haveibeenpwned.com",
		TookMs:  time.Since(start).Milliseconds(),
	}
	if resp.StatusCode == 404 {
		out.Pwned = false
		return out, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("hibp returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var breaches []HIBPBreach
	if err := json.Unmarshal(body, &breaches); err != nil {
		return nil, fmt.Errorf("hibp decode: %w", err)
	}
	out.Pwned = len(breaches) > 0
	out.Breaches = breaches
	out.Count = len(breaches)
	return out, nil
}
