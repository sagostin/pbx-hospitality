package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/db"
)

// tokenResponse is what we return for admin endpoints. Plaintext is
// only ever returned ONCE on create — after that only metadata.
type tokenResponse struct {
	ID           int64      `json:"id"`
	TenantID     string     `json:"tenant_id"`
	AuthStrategy string     `json:"auth_strategy"`
	BasicUser    string     `json:"basic_user,omitempty"`
	Enabled      bool       `json:"enabled"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`

	// Plaintext is populated only on the create response so the
	// operator can capture it once. Never returned by GET.
	Plaintext string `json:"plaintext,omitempty"`
}

type createTokenRequest struct {
	AuthStrategy string `json:"auth_strategy"` // 'url_token' (default), 'bearer', 'basic'
	BearerSecret string `json:"bearer_secret,omitempty"`
	BasicUser    string `json:"basic_user,omitempty"`
	BasicSecret  string `json:"basic_secret,omitempty"`
}

// createTenantToken creates a new inbound token for a tenant. The
// plaintext token is generated server-side for url_token (the default)
// and returned exactly once in the response. For bearer/basic, the
// caller provides the secret in the request — that secret is then
// hashed (SHA-256) before storage; the plaintext bearer/basic secret
// is never persisted and is echoed back in the response so the
// operator can configure the upstream sender.
func (s *Server) createTenantToken(c *fiber.Ctx) error {
	if s.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "database not configured"})
	}
	tenantID := c.Params("id")
	if !validateSiteID(tenantID) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid tenant id"})
	}

	var req createTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}
	strategy := req.AuthStrategy
	if strategy == "" {
		strategy = "url_token"
	}
	if strategy != "url_token" && strategy != "bearer" && strategy != "basic" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "auth_strategy must be one of url_token, bearer, basic",
		})
	}

	row := &db.TenantInboundToken{
		TenantID:     tenantID,
		AuthStrategy: strategy,
		Enabled:      true,
	}

	plaintext := ""
	switch strategy {
	case "url_token":
		tok, err := generateToken(32) // 32 random bytes → 64 hex chars
		if err != nil {
			log.Error().Err(err).Str("tenant", tenantID).Msg("generate url_token failed")
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "token generation failed"})
		}
		plaintext = tok
		row.TokenHash = sha256Hex(tok)

	case "bearer":
		if req.BearerSecret == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "bearer_secret required for bearer strategy",
			})
		}
		plaintext = req.BearerSecret
		row.TokenHash = sha256Hex(plaintext)
		row.BearerHash = sha256Hex(plaintext)

	case "basic":
		if req.BasicUser == "" || req.BasicSecret == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "basic_user and basic_secret required for basic strategy",
			})
		}
		row.TokenHash = sha256Hex(req.BasicUser + ":" + req.BasicSecret)
		row.BasicUser = req.BasicUser
		row.BasicHash = sha256Hex(req.BasicSecret)
	}

	if err := s.db.CreateTenantInboundToken(c.Context(), row); err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("create tenant token failed")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "create failed"})
	}

	resp := tokenResponse{
		ID:           row.ID,
		TenantID:     row.TenantID,
		AuthStrategy: row.AuthStrategy,
		BasicUser:    row.BasicUser,
		Enabled:      row.Enabled,
		CreatedAt:    row.CreatedAt,
		Plaintext:    plaintext, // only on create
	}
	return c.Status(fiber.StatusCreated).JSON(resp)
}

// listTenantTokens returns all tokens for a tenant. Never includes
// plaintext or hashes.
func (s *Server) listTenantTokens(c *fiber.Ctx) error {
	if s.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "database not configured"})
	}
	tenantID := c.Params("id")
	if !validateSiteID(tenantID) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid tenant id"})
	}
	rows, err := s.db.ListTenantInboundTokens(c.Context(), tenantID)
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("list tenant tokens failed")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "list failed"})
	}
	out := make([]tokenResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, tokenResponse{
			ID:           r.ID,
			TenantID:     r.TenantID,
			AuthStrategy: r.AuthStrategy,
			BasicUser:    r.BasicUser,
			Enabled:      r.Enabled,
			LastUsedAt:   r.LastUsedAt,
			CreatedAt:    r.CreatedAt,
		})
	}
	return c.JSON(out)
}

// revokeTenantToken disables a token (sets enabled=false). The row
// stays for audit; a new token can be created to rotate.
func (s *Server) revokeTenantToken(c *fiber.Ctx) error {
	if s.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "database not configured"})
	}
	tenantID := c.Params("id")
	if !validateSiteID(tenantID) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid tenant id"})
	}
	idStr := c.Params("tokenId")
	var id int64
	if _, err := fmtSscan(idStr, &id); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid token id"})
	}
	if err := s.db.DisableTenantInboundToken(c.Context(), tenantID, id); err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Int64("token_id", id).Msg("revoke token failed")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "revoke failed"})
	}
	return c.JSON(fiber.Map{"status": "revoked", "id": id})
}

// generateToken returns n random bytes hex-encoded (2n chars).
func generateToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// fmtSscan is a tiny shim to avoid importing fmt across the file —
// avoids the extra dependency churn.
func fmtSscan(s string, p *int64) (int, error) {
	var v int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, &numErr{s: s}
		}
		v = v*10 + int64(c-'0')
	}
	*p = v
	return 1, nil
}

type numErr struct{ s string }

func (e *numErr) Error() string { return "invalid number: " + e.s }

// ensure the strings import survives if a future change drops other uses
var _ = strings.TrimSpace
var _ = base64.StdEncoding
