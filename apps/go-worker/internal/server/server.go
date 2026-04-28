package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"os"
	"time"

	"github.com/jroell/osint-agent/apps/go-worker/internal/tools"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type ToolRequest struct {
	RequestID string         `json:"requestId"`
	TenantID  string         `json:"tenantId"`
	UserID    string         `json:"userId"`
	Tool      string         `json:"tool"`
	Input     map[string]any `json:"input"`
	TimeoutMs int            `json:"timeoutMs"`
}

type ToolResponse struct {
	RequestID string      `json:"requestId"`
	OK        bool        `json:"ok"`
	Output    interface{} `json:"output,omitempty"`
	Error     *ToolError  `json:"error,omitempty"`
	Telemetry Telemetry   `json:"telemetry"`
}

type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Telemetry struct {
	TookMs    int64  `json:"tookMs"`
	CacheHit  bool   `json:"cacheHit"`
	ProxyUsed string `json:"proxyUsed,omitempty"`
}

func NewServer() *echo.Echo {
	pubHex := os.Getenv("WORKER_PUBLIC_KEY_HEX")
	if pubHex == "" {
		panic("WORKER_PUBLIC_KEY_HEX required")
	}
	pubBytes, err := hex.DecodeString(pubHex)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		panic("invalid WORKER_PUBLIC_KEY_HEX")
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimit("5MB"))
	e.GET("/healthz", func(c echo.Context) error {
		return c.JSON(200, map[string]string{"ok": "true", "service": "go-worker"})
	})

	authed := e.Group("", RequireSigned(SignedAuthConfig{PublicKey: ed25519.PublicKey(pubBytes)}))
	authed.POST("/tool", handleTool)

	return e
}

func handleTool(c echo.Context) error {
	var req ToolRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	start := time.Now()

	var (
		out interface{}
		err error
	)
	switch req.Tool {
	case "subfinder_passive":
		out, err = tools.Subfinder(c.Request().Context(), req.Input)
	case "dns_lookup_comprehensive":
		out, err = tools.DNS(c.Request().Context(), req.Input)
	default:
		return c.JSON(http.StatusOK, ToolResponse{
			RequestID: req.RequestID,
			OK:        false,
			Error:     &ToolError{Code: "unknown_tool", Message: req.Tool},
			Telemetry: Telemetry{TookMs: time.Since(start).Milliseconds()},
		})
	}

	resp := ToolResponse{
		RequestID: req.RequestID,
		OK:        err == nil,
		Telemetry: Telemetry{TookMs: time.Since(start).Milliseconds()},
	}
	if err != nil {
		resp.Error = &ToolError{Code: "tool_failure", Message: err.Error()}
	} else {
		resp.Output = out
	}
	return c.JSON(http.StatusOK, resp)
}
