package server

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestRequireSigned_OK(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	e := echo.New()
	e.Use(RequireSigned(SignedAuthConfig{PublicKey: pub}))
	e.POST("/x", func(c echo.Context) error { return c.String(200, "ok") })

	body := []byte(`{"hello":"world"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := ed25519.Sign(priv, []byte(ts+"\n"+string(body)))

	req := httptest.NewRequest("POST", "/x", bytes.NewReader(body))
	req.Header.Set("X-Osint-Ts", ts)
	req.Header.Set("X-Osint-Sig", hex.EncodeToString(sig))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRequireSigned_BadSig(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	e := echo.New()
	e.Use(RequireSigned(SignedAuthConfig{PublicKey: pub}))
	e.POST("/x", func(c echo.Context) error { return c.String(200, "ok") })

	req := httptest.NewRequest("POST", "/x", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-Osint-Ts", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("X-Osint-Sig", hex.EncodeToString([]byte("bogus-sig-bogus-sig-bogus-sig-bogus-sig-bogus-sig-bogus-sig-bogus-sig")))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}
