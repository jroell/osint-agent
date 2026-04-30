package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type JWTDecoded struct {
	JWT             string                 `json:"jwt"`
	Header          map[string]any         `json:"header,omitempty"`
	Payload         map[string]any         `json:"payload,omitempty"`
	Algorithm       string                 `json:"algorithm,omitempty"`
	KeyID           string                 `json:"key_id,omitempty"`
	Type            string                 `json:"typ,omitempty"`
	Issuer          string                 `json:"iss,omitempty"`
	Audience        any                    `json:"aud,omitempty"`
	Subject         string                 `json:"sub,omitempty"`
	IssuedAt        string                 `json:"iat,omitempty"`
	NotBefore       string                 `json:"nbf,omitempty"`
	ExpiresAt       string                 `json:"exp,omitempty"`
	JWTID           string                 `json:"jti,omitempty"`
	Scope           string                 `json:"scope,omitempty"`
	Email           string                 `json:"email,omitempty"`
	Username        string                 `json:"preferred_username,omitempty"`
	IsExpired       bool                   `json:"is_expired"`
	SecondsToExpiry int64                  `json:"seconds_until_expiry,omitempty"`
	IssuerCategory  string                 `json:"issuer_category,omitempty"` // "auth0", "okta", "firebase", etc.
	SecurityFlags   []string               `json:"security_flags,omitempty"`
	SignaturePresent bool                  `json:"signature_present"`
	SignatureBytes  int                    `json:"signature_bytes,omitempty"`
	CustomClaims    map[string]any         `json:"custom_claims,omitempty"`
	ParseError      string                 `json:"parse_error,omitempty"`
}

type JWTDecoderOutput struct {
	Tokens         []JWTDecoded `json:"tokens"`
	TotalDecoded   int          `json:"total_decoded"`
	TotalExpired   int          `json:"total_expired"`
	TotalErrors    int          `json:"total_errors"`
	Source         string       `json:"source"`
	TookMs         int64        `json:"tookMs"`
}

// classifyIssuer maps an `iss` claim string to a known auth provider category.
func classifyIssuer(iss string) string {
	low := strings.ToLower(iss)
	switch {
	case strings.Contains(low, "auth0.com"):
		return "auth0"
	case strings.Contains(low, "okta.com"):
		return "okta"
	case strings.Contains(low, "cognito-idp"):
		return "aws-cognito"
	case strings.Contains(low, "securetoken.google.com"):
		return "firebase-auth"
	case strings.Contains(low, "accounts.google.com"):
		return "google-oauth2"
	case strings.Contains(low, "login.microsoftonline.com"):
		return "azure-ad"
	case strings.Contains(low, "graph.facebook.com") || strings.Contains(low, "www.facebook.com"):
		return "facebook"
	case strings.Contains(low, "appleid.apple.com"):
		return "apple-signin"
	case strings.Contains(low, "discord.com"):
		return "discord"
	case strings.Contains(low, "github.com"):
		return "github"
	case strings.Contains(low, "supabase"):
		return "supabase"
	case strings.Contains(low, "clerk"):
		return "clerk"
	case strings.Contains(low, "workos"):
		return "workos"
	case strings.Contains(low, "frontegg"):
		return "frontegg"
	case strings.Contains(low, "stytch"):
		return "stytch"
	case strings.Contains(low, "fronteggsec"):
		return "frontegg"
	}
	return "unknown"
}

// JWTDecoder parses one or more JWTs (JSON Web Tokens) WITHOUT verifying
// the signature — pure local base64-decode of header + payload. Identifies
// issuer, audience, subject, expiry. Detects:
//   - Known auth provider issuers (Auth0, Okta, Cognito, Firebase, Azure AD,
//     Google, Apple, GitHub, Supabase, Clerk, WorkOS, etc.)
//   - Expired tokens
//   - Common security flags: alg=none, very-long expiry windows, missing claims
//
// Use case: when the agent finds a JWT in a leak (github_code_search,
// postman_public_search, browser DevTools paste), this decodes it locally
// to expose what API/tenant/user it was issued for. Privacy-preserving —
// the JWT never leaves the worker.
//
// Pure Go, zero external dependencies, sub-millisecond response.
func JWTDecoder(_ context.Context, input map[string]any) (*JWTDecoderOutput, error) {
	tokens := []string{}
	if v, ok := input["jwt"].(string); ok && v != "" {
		tokens = append(tokens, v)
	}
	if v, ok := input["jwts"].([]any); ok {
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				tokens = append(tokens, s)
			}
		}
	}
	if len(tokens) == 0 {
		return nil, errors.New("input.jwt or input.jwts required")
	}

	start := time.Now()
	out := &JWTDecoderOutput{Source: "jwt_decoder"}

	for _, raw := range tokens {
		dec := decodeOneJWT(raw)
		out.Tokens = append(out.Tokens, dec)
		if dec.ParseError != "" {
			out.TotalErrors++
		} else {
			out.TotalDecoded++
		}
		if dec.IsExpired {
			out.TotalExpired++
		}
	}
	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}

func decodeOneJWT(raw string) JWTDecoded {
	dec := JWTDecoded{JWT: truncate(raw, 60) + "..."}
	raw = strings.TrimSpace(raw)
	// Strip "Bearer " prefix if present
	raw = strings.TrimPrefix(raw, "Bearer ")
	raw = strings.TrimPrefix(raw, "bearer ")

	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		dec.ParseError = fmt.Sprintf("invalid JWT: expected 3 parts, got %d", len(parts))
		return dec
	}

	headerBytes, err := base64URLDecode(parts[0])
	if err != nil {
		dec.ParseError = "header base64 decode failed: " + err.Error()
		return dec
	}
	payloadBytes, err := base64URLDecode(parts[1])
	if err != nil {
		dec.ParseError = "payload base64 decode failed: " + err.Error()
		return dec
	}
	signatureBytes, sigErr := base64URLDecode(parts[2])
	dec.SignaturePresent = sigErr == nil && len(signatureBytes) > 0
	dec.SignatureBytes = len(signatureBytes)

	var header map[string]any
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		dec.ParseError = "header JSON parse: " + err.Error()
		return dec
	}
	dec.Header = header
	if v, ok := header["alg"].(string); ok {
		dec.Algorithm = v
	}
	if v, ok := header["kid"].(string); ok {
		dec.KeyID = v
	}
	if v, ok := header["typ"].(string); ok {
		dec.Type = v
	}

	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		dec.ParseError = "payload JSON parse: " + err.Error()
		return dec
	}
	dec.Payload = payload
	if v, ok := payload["iss"].(string); ok {
		dec.Issuer = v
	}
	if v, ok := payload["aud"]; ok {
		dec.Audience = v
	}
	if v, ok := payload["sub"].(string); ok {
		dec.Subject = v
	}
	if v, ok := payload["jti"].(string); ok {
		dec.JWTID = v
	}
	if v, ok := payload["scope"].(string); ok {
		dec.Scope = v
	}
	if v, ok := payload["email"].(string); ok {
		dec.Email = v
	}
	if v, ok := payload["preferred_username"].(string); ok {
		dec.Username = v
	}

	now := time.Now().Unix()
	if v, ok := payload["iat"].(float64); ok {
		dec.IssuedAt = time.Unix(int64(v), 0).UTC().Format(time.RFC3339)
	}
	if v, ok := payload["nbf"].(float64); ok {
		dec.NotBefore = time.Unix(int64(v), 0).UTC().Format(time.RFC3339)
	}
	if v, ok := payload["exp"].(float64); ok {
		expUnix := int64(v)
		dec.ExpiresAt = time.Unix(expUnix, 0).UTC().Format(time.RFC3339)
		dec.SecondsToExpiry = expUnix - now
		dec.IsExpired = expUnix < now
	}

	dec.IssuerCategory = classifyIssuer(dec.Issuer)

	// Security flag heuristics
	flags := []string{}
	if strings.EqualFold(dec.Algorithm, "none") {
		flags = append(flags, "CRITICAL: alg=none — accepted without signature verification")
	}
	if dec.SecondsToExpiry > 60*60*24*365 {
		flags = append(flags, "long-lived token (>1y until expiry)")
	}
	if dec.ExpiresAt == "" {
		flags = append(flags, "no exp claim — token never expires")
	}
	if dec.Issuer == "" {
		flags = append(flags, "no iss claim — issuer unverified")
	}
	if dec.Audience == nil {
		flags = append(flags, "no aud claim — audience unbound")
	}
	dec.SecurityFlags = flags

	// Custom claims = anything not in the standard set
	standard := map[string]bool{
		"iss": true, "aud": true, "sub": true, "exp": true, "nbf": true,
		"iat": true, "jti": true, "scope": true, "email": true,
		"preferred_username": true, "azp": true,
	}
	custom := map[string]any{}
	for k, v := range payload {
		if !standard[k] {
			custom[k] = v
		}
	}
	if len(custom) > 0 {
		dec.CustomClaims = custom
	}

	return dec
}

func base64URLDecode(s string) ([]byte, error) {
	// Add padding if needed
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

