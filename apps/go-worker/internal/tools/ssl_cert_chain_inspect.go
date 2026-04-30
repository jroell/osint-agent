package tools

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

type CertSummary struct {
	Position           int      `json:"position"` // 0=leaf, 1=intermediate, etc.
	Subject            string   `json:"subject"`
	Issuer             string   `json:"issuer"`
	SerialNumber       string   `json:"serial_number"`
	NotBefore          string   `json:"not_before"`
	NotAfter           string   `json:"not_after"`
	DaysUntilExpiry    int      `json:"days_until_expiry,omitempty"`
	IsExpired          bool     `json:"is_expired"`
	IsCA               bool     `json:"is_ca"`
	SignatureAlgorithm string   `json:"signature_algorithm"`
	PublicKeyAlgorithm string   `json:"public_key_algorithm"`
	KeyBitLength       int      `json:"key_bit_length,omitempty"`
	SHA256Fingerprint  string   `json:"sha256_fingerprint"`
	SHA1Fingerprint    string   `json:"sha1_fingerprint"`
	SubjectKeyID       string   `json:"subject_key_id,omitempty"`
	AuthKeyID          string   `json:"auth_key_id,omitempty"`
	DNSNames           []string `json:"dns_names,omitempty"`
	EmailAddresses     []string `json:"email_addresses,omitempty"`
	IPAddresses        []string `json:"ip_addresses,omitempty"`
	URIs               []string `json:"uris,omitempty"`
	IssuingAuthorityURL []string `json:"issuing_authority_urls,omitempty"`
	OCSPServers        []string `json:"ocsp_servers,omitempty"`
	CRLDistribution    []string `json:"crl_distribution_points,omitempty"`
	IsSelfSigned       bool     `json:"is_self_signed"`
}

type SSLCertChainOutput struct {
	Target              string        `json:"target"`
	Host                string        `json:"host"`
	Port                int           `json:"port"`
	HandshakeMs         int64         `json:"handshake_ms"`
	TLSVersion          string        `json:"tls_version"`
	CipherSuite         string        `json:"cipher_suite"`
	NegotiatedProtocol  string        `json:"negotiated_protocol,omitempty"` // ALPN
	OCSPStapled         bool          `json:"ocsp_stapled"`
	ChainLen            int           `json:"chain_length"`
	Chain               []CertSummary `json:"chain"`
	LeafFingerprint     string        `json:"leaf_sha256_fingerprint,omitempty"`
	LeafSerialNumber    string        `json:"leaf_serial_number,omitempty"`
	LeafIssuer          string        `json:"leaf_issuer,omitempty"`
	LeafIssuerOrg       string        `json:"leaf_issuer_org,omitempty"`
	WildcardSANs        []string      `json:"wildcard_sans,omitempty"`
	HighlightFindings   []string      `json:"highlight_findings"`
	Source              string        `json:"source"`
	TookMs              int64         `json:"tookMs"`
	Note                string        `json:"note,omitempty"`
}

// SSLCertChainInspect performs a live TLS handshake to a target host and
// extracts every certificate in the chain (leaf + intermediates + root if
// served). For each cert returns: subject, issuer, validity dates, key
// metadata, SHA-256/SHA-1 fingerprints, SAN list, OCSP/CRL endpoints.
//
// Use cases:
//   - ER via cert reuse: same SHA-256 fingerprint across multiple domains
//     means SAME CERT (often: load balancer with multi-domain SAN, or
//     cross-domain operator footprint)
//   - Operational maturity signal: Let's Encrypt vs DigiCert vs internal CA
//     reveals security/spend posture
//   - Self-signed detection: red flag for production endpoints
//   - Wildcard scope: a *.target.com cert reveals admin's blast radius
//   - Expiry monitoring: catch upcoming expiry before downtime
//
// Pure Go via crypto/tls. No external API. Pairs with `cert_transparency`
// (CT log historical data) and `favicon_pivot` (favicon-based ER).
func SSLCertChainInspect(ctx context.Context, input map[string]any) (*SSLCertChainOutput, error) {
	target, _ := input["target"].(string)
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return nil, errors.New("input.target required (e.g. 'vurvey.app' or 'vurvey.app:443')")
	}
	target = strings.TrimPrefix(target, "https://")
	target = strings.TrimPrefix(target, "http://")
	if i := strings.Index(target, "/"); i >= 0 {
		target = target[:i]
	}

	host := target
	port := 443
	if strings.Contains(target, ":") {
		h, p, err := net.SplitHostPort(target)
		if err == nil {
			host = h
			fmt.Sscanf(p, "%d", &port)
		}
	}
	servername, _ := input["server_name"].(string)
	if servername == "" {
		servername = host
	}

	start := time.Now()
	out := &SSLCertChainOutput{
		Target: target, Host: host, Port: port,
		Source: "live_tls_handshake",
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	tlsConfig := &tls.Config{
		ServerName:         servername,
		InsecureSkipVerify: true, // we still get the cert chain, just don't reject
		NextProtos:         []string{"h2", "http/1.1"},
	}

	hsStart := time.Now()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(cctx, "tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, fmt.Errorf("tcp dial failed: %w", err)
	}
	defer conn.Close()
	tlsConn := tls.Client(conn, tlsConfig)
	if err := tlsConn.HandshakeContext(cctx); err != nil {
		return nil, fmt.Errorf("tls handshake failed: %w", err)
	}
	out.HandshakeMs = time.Since(hsStart).Milliseconds()

	state := tlsConn.ConnectionState()
	out.TLSVersion = tlsVersionString(state.Version)
	out.CipherSuite = tls.CipherSuiteName(state.CipherSuite)
	out.NegotiatedProtocol = state.NegotiatedProtocol
	out.OCSPStapled = len(state.OCSPResponse) > 0

	chain := state.PeerCertificates
	out.ChainLen = len(chain)
	now := time.Now()

	wildcardSet := map[string]bool{}
	for i, cert := range chain {
		summary := certToSummary(cert, i, now)
		out.Chain = append(out.Chain, summary)
		for _, san := range summary.DNSNames {
			if strings.HasPrefix(san, "*.") {
				wildcardSet[san] = true
			}
		}
	}
	for w := range wildcardSet {
		out.WildcardSANs = append(out.WildcardSANs, w)
	}

	// Extract leaf-specific
	if len(out.Chain) > 0 {
		leaf := out.Chain[0]
		out.LeafFingerprint = leaf.SHA256Fingerprint
		out.LeafSerialNumber = leaf.SerialNumber
		out.LeafIssuer = leaf.Issuer
		// Issuer org parse — extract O= field
		if idx := strings.Index(leaf.Issuer, "O="); idx >= 0 {
			rest := leaf.Issuer[idx+2:]
			if endIdx := strings.IndexAny(rest, ","); endIdx >= 0 {
				out.LeafIssuerOrg = strings.TrimSpace(rest[:endIdx])
			} else {
				out.LeafIssuerOrg = strings.TrimSpace(rest)
			}
		}
	}

	// Highlights
	highlights := []string{}
	if len(out.Chain) > 0 {
		leaf := out.Chain[0]
		highlights = append(highlights, fmt.Sprintf("leaf cert: %s — %d days until expiry, %d SANs",
			truncate(leaf.Subject, 80), leaf.DaysUntilExpiry, len(leaf.DNSNames)))
		highlights = append(highlights, fmt.Sprintf("issuer: %s", truncate(out.LeafIssuer, 100)))
		if leaf.IsSelfSigned {
			highlights = append(highlights, "⚠️  SELF-SIGNED leaf certificate (red flag for production)")
		}
		if leaf.DaysUntilExpiry < 14 && leaf.DaysUntilExpiry >= 0 {
			highlights = append(highlights, fmt.Sprintf("⚠️  cert expires in %d days", leaf.DaysUntilExpiry))
		}
		if leaf.IsExpired {
			highlights = append(highlights, "⛔ CERT IS EXPIRED")
		}
	}
	if len(out.WildcardSANs) > 0 {
		highlights = append(highlights, fmt.Sprintf("wildcard SANs: %s — broad cert scope", strings.Join(out.WildcardSANs, ", ")))
	}
	if out.LeafFingerprint != "" {
		highlights = append(highlights, fmt.Sprintf("leaf SHA-256: %s — pivot via this fingerprint to find other domains using the same cert", out.LeafFingerprint[:48]+"..."))
	}
	if !out.OCSPStapled {
		highlights = append(highlights, "no OCSP stapling (operational hygiene signal)")
	}
	highlights = append(highlights, fmt.Sprintf("TLS: %s / %s", out.TLSVersion, out.CipherSuite))
	out.HighlightFindings = highlights
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func certToSummary(c *x509.Certificate, pos int, now time.Time) CertSummary {
	sha256Hash := sha256.Sum256(c.Raw)
	sha1Hash := sha1.Sum(c.Raw)

	// Self-signed: subject == issuer AND signed-by-self
	isSelf := c.Subject.String() == c.Issuer.String()
	if isSelf && c.AuthorityKeyId != nil && c.SubjectKeyId != nil {
		isSelf = string(c.AuthorityKeyId) == string(c.SubjectKeyId)
	}

	keyBits := 0
	pkAlgo := c.PublicKeyAlgorithm.String()
	switch pk := c.PublicKey.(type) {
	case interface{ Size() int }:
		keyBits = pk.Size() * 8
	default:
		_ = pk
	}

	ipStrs := []string{}
	for _, ip := range c.IPAddresses {
		ipStrs = append(ipStrs, ip.String())
	}
	uriStrs := []string{}
	for _, u := range c.URIs {
		uriStrs = append(uriStrs, u.String())
	}

	daysUntil := -1
	if !c.NotAfter.IsZero() {
		daysUntil = int(c.NotAfter.Sub(now).Hours() / 24)
	}
	isExpired := !c.NotAfter.IsZero() && c.NotAfter.Before(now)

	return CertSummary{
		Position:           pos,
		Subject:            c.Subject.String(),
		Issuer:             c.Issuer.String(),
		SerialNumber:       fmt.Sprintf("%x", c.SerialNumber),
		NotBefore:          c.NotBefore.Format(time.RFC3339),
		NotAfter:           c.NotAfter.Format(time.RFC3339),
		DaysUntilExpiry:    daysUntil,
		IsExpired:          isExpired,
		IsCA:               c.IsCA,
		SignatureAlgorithm: c.SignatureAlgorithm.String(),
		PublicKeyAlgorithm: pkAlgo,
		KeyBitLength:       keyBits,
		SHA256Fingerprint:  hex.EncodeToString(sha256Hash[:]),
		SHA1Fingerprint:    hex.EncodeToString(sha1Hash[:]),
		SubjectKeyID:       hex.EncodeToString(c.SubjectKeyId),
		AuthKeyID:          hex.EncodeToString(c.AuthorityKeyId),
		DNSNames:           c.DNSNames,
		EmailAddresses:     c.EmailAddresses,
		IPAddresses:        ipStrs,
		URIs:               uriStrs,
		IssuingAuthorityURL: c.IssuingCertificateURL,
		OCSPServers:        c.OCSPServer,
		CRLDistribution:    c.CRLDistributionPoints,
		IsSelfSigned:       isSelf,
	}
}

func tlsVersionString(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}
