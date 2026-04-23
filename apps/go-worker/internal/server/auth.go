package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
)

const (
	clockSkewTolerance = 60 * time.Second
	tsHeader           = "X-Osint-Ts"
	sigHeader          = "X-Osint-Sig"
)

type SignedAuthConfig struct {
	PublicKey ed25519.PublicKey
}

func RequireSigned(cfg SignedAuthConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			body, err := io.ReadAll(c.Request().Body)
			if err != nil {
				return echo.NewHTTPError(http.StatusBadRequest, "read body")
			}
			c.Request().Body = io.NopCloser(newBuf(body))

			ts := c.Request().Header.Get(tsHeader)
			sigHex := c.Request().Header.Get(sigHeader)
			if ts == "" || sigHex == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing signature headers")
			}

			tsInt, err := strconv.ParseInt(ts, 10, 64)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "bad ts")
			}
			now := time.Now().Unix()
			if now-tsInt > int64(clockSkewTolerance/time.Second) || tsInt-now > int64(clockSkewTolerance/time.Second) {
				return echo.NewHTTPError(http.StatusUnauthorized, "ts out of tolerance")
			}

			sig, err := hex.DecodeString(sigHex)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "bad sig hex")
			}

			msg := []byte(ts + "\n" + string(body))
			if !ed25519.Verify(cfg.PublicKey, msg, sig) {
				return echo.NewHTTPError(http.StatusUnauthorized, "sig verify failed")
			}
			return next(c)
		}
	}
}

// --- helpers ---

type byteBuf struct {
	b []byte
	o int
}

func newBuf(b []byte) *byteBuf { return &byteBuf{b: b} }
func (r *byteBuf) Read(p []byte) (int, error) {
	if r.o >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.o:])
	r.o += n
	return n, nil
}
