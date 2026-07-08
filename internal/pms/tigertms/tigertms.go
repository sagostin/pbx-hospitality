// Package tigertms implements the TigerTMS iLink Asterisk REST protocol as
// documented in docs/tigertms/TigerTMS_AsteriskRestAPI.pdf and the CDR
// push protocol in docs/tigertms/TigerTMS_AsteriskPostCDRRestAPI.pdf.
//
// Wire-format summary (from the PDFs):
//
//   - Transport: HTTP/HTTPS POST, JSON body (Content-Type: text/json).
//   - Inbound multi-tenant discriminator: a long random secret
//     embedded in the URL path, e.g.
//     POST /api/v1/pms/inbound/<token>/API/setguest
//     The token identifies AND authenticates the tenant. We hash
//     (SHA-256) the inbound token at lookup time and never persist
//     the plaintext.
//   - Optional layered auth: bearer / basic. Strategies 'url_token',
//     'bearer', 'basic' are configured per-token via
//     tenant_inbound_tokens.auth_strategy.
//   - Response shape: `{"result":"success|failed","information":"..."}`.
//   - CDR is OUTBOUND from us to TigerTMS iLink (`POST /API/CDR` with
//     body `{"message":{...Asterisk CDR fields...}}`) — we do NOT
//     receive CDRs inbound. See the outbound dispatcher work tracked
//     in docs/integrations/tigertms-cloud-backend.md §6.
//
// Each token (one per tenant, possibly multiple) maps to an Adapter
// (events channel) keyed by tenant ID. The Handler dispatches inbound
// HTTP requests to the correct Adapter based on the resolved token.
package tigertms

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/pms"
)

func init() {
	pms.Register("tigertms", NewAdapter)
	pms.Register("tigertms_ilink", NewAdapter)
}

// Adapter is the per-tenant TigerTMS iLink event source. It owns the
// events channel that the tenant's event-processing goroutine reads
// from.
type Adapter struct {
	tenantID  string
	siteid    string
	events    chan pms.Event
	mu        sync.RWMutex
	cancel    context.CancelFunc
	connected bool
}

// NewAdapter creates a new Adapter. host/port are unused for iLink
// (iLink pushes to us) but kept to match the pms.AdapterFactory
// contract.
func NewAdapter(host string, port int, opts ...pms.AdapterOption) (pms.Adapter, error) {
	a := &Adapter{
		events: make(chan pms.Event, 100),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a, nil
}

// WithTenant configures the adapter's tenantID and siteid.
func WithTenant(tenantID, siteid string) pms.AdapterOption {
	return func(v interface{}) {
		if ad, ok := v.(*Adapter); ok {
			ad.tenantID = tenantID
			ad.siteid = siteid
		}
	}
}

func (a *Adapter) Protocol() string { return "tigertms" }

func (a *Adapter) TenantID() string { return a.tenantID }
func (a *Adapter) SiteID() string   { return a.siteid }

func (a *Adapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, a.cancel = context.WithCancel(ctx)
	a.connected = true
	log.Info().
		Str("protocol", "tigertms").
		Str("tenant", a.tenantID).
		Str("siteid", a.siteid).
		Msg("TigerTMS iLink adapter ready to receive HTTP events")
	return nil
}

func (a *Adapter) Events() <-chan pms.Event { return a.events }

func (a *Adapter) SendAck() error { return nil }
func (a *Adapter) SendNak() error { return nil }

func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
	a.connected = false
	close(a.events)
	return nil
}

func (a *Adapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

// TokenResolver maps a SHA-256 hex hash of an inbound URL token to a
// (tenantID, auth_strategy, basic_user, basic_pass_hash, bearer_hash)
// record. The DB implementation lives in api.Server; tests can pass
// a fake.
type TokenResolver interface {
	ResolveTokenHash(ctx context.Context, tokenHash string) (*ResolvedToken, error)
}

// ResolvedToken is the per-token authentication context returned by
// TokenResolver. Plaintext secrets are never present — only hashes —
// so strategy validation uses constant-time compare on the hashes.
type ResolvedToken struct {
	TokenID    int64
	TenantID   string
	Strategy   string
	BearerHash string // SHA-256 hex of bearer secret (strategy=bearer)
	BasicUser  string // (strategy=basic)
	BasicHash  string // SHA-256 hex of basic password (strategy=basic)
}

// AuthError is returned when the inbound request fails any auth
// strategy check. It carries the status code + reason for the
// iLink-shaped failure response.
type AuthError struct {
	Status int
	Reason string
}

func (e *AuthError) Error() string { return e.Reason }

// Handler routes inbound iLink HTTP requests to the per-tenant
// Adapter. One Handler per process; the token→adapter map is built at
// startup by the API layer (or on reload).
type Handler struct {
	mu       sync.RWMutex
	resolver TokenResolver
	adapters map[string]*Adapter // tenantID → adapter
}

// NewHandler creates a Handler with no resolver. Use SetResolver to
// wire in the DB-backed resolver at startup.
func NewHandler() *Handler {
	return &Handler{adapters: make(map[string]*Adapter)}
}

// SetResolver installs the token resolver. Safe to call once at
// startup; not safe for concurrent calls (call before Start).
func (h *Handler) SetResolver(r TokenResolver) {
	h.mu.Lock()
	h.resolver = r
	h.mu.Unlock()
}

// RegisterTenant registers a tenant's adapter. Calling with the same
// tenantID replaces the previous registration.
func (h *Handler) RegisterTenant(tenantID string, a *Adapter) {
	if tenantID == "" {
		return
	}
	h.mu.Lock()
	h.adapters[tenantID] = a
	h.mu.Unlock()
}

// DeregisterTenant removes a tenant adapter (used on tenant disable /
// delete).
func (h *Handler) DeregisterTenant(tenantID string) {
	h.mu.Lock()
	delete(h.adapters, tenantID)
	h.mu.Unlock()
}

// Routes registers all iLink endpoints on the supplied Fiber app. The
// Handler expects a `<token>` URL parameter on every request and uses
// the resolver to identify the tenant + validate the auth strategy.
func (h *Handler) Routes(app *fiber.App) {
	group := app.Group("/api/v1/pms/inbound/:token")
	group.Post("/API/setguest", h.handleSetGuest)
	group.Post("/API/setcos", h.handleSetCOS)
	group.Post("/API/setmw", h.handleSetMW)
	group.Post("/API/setsipdata", h.handleSetSIPData)
	group.Post("/API/setddi", h.handleSetDDI)
	group.Post("/API/setdnd", h.handleSetDND)
	group.Post("/API/setwakeup", h.handleSetWakeup)
}

// resolveAndAuth reads the URL token, hashes it, looks up the tenant,
// and validates the configured auth strategy. On success, returns the
// tenant's adapter. On failure, writes an iLink-shaped failure
// response and returns nil + error.
func (h *Handler) resolveAndAuth(c *fiber.Ctx) (*Adapter, error) {
	token := c.Params("token")
	if token == "" {
		writeFailed(c, fiber.StatusBadRequest, "missing inbound token")
		return nil, &AuthError{Status: 400, Reason: "missing token"}
	}
	if len(token) < 16 {
		writeFailed(c, fiber.StatusBadRequest, "inbound token too short")
		return nil, &AuthError{Status: 400, Reason: "token too short"}
	}

	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])

	h.mu.RLock()
	resolver := h.resolver
	h.mu.RUnlock()
	if resolver == nil {
		writeFailed(c, fiber.StatusServiceUnavailable, "token resolver not configured")
		return nil, &AuthError{Status: 503, Reason: "resolver missing"}
	}

	resolved, err := resolver.ResolveTokenHash(c.Context(), tokenHash)
	if err != nil {
		log.Error().Err(err).Msg("TigerTMS token resolver failed")
		writeFailed(c, fiber.StatusInternalServerError, "token resolver error")
		return nil, &AuthError{Status: 500, Reason: "resolver error"}
	}
	if resolved == nil {
		writeFailed(c, fiber.StatusUnauthorized, "invalid inbound token")
		return nil, &AuthError{Status: 401, Reason: "unknown token"}
	}

	if err := h.validateStrategy(c, resolved); err != nil {
		var ae *AuthError
		if errors.As(err, &ae) {
			writeFailed(c, ae.Status, ae.Reason)
			return nil, ae
		}
		writeFailed(c, fiber.StatusUnauthorized, "auth failed")
		return nil, &AuthError{Status: 401, Reason: "auth failed"}
	}

	h.mu.RLock()
	adapter, ok := h.adapters[resolved.TenantID]
	h.mu.RUnlock()
	if !ok || !adapter.Connected() {
		writeFailed(c, fiber.StatusServiceUnavailable, "tenant adapter not connected")
		return nil, &AuthError{Status: 503, Reason: "tenant adapter unavailable"}
	}
	return adapter, nil
}

// validateStrategy enforces the per-token auth strategy. Default is
// url_token (the URL token IS the only auth). bearer/basic add a
// header-based check on top.
func (h *Handler) validateStrategy(c *fiber.Ctx, resolved *ResolvedToken) error {
	switch resolved.Strategy {
	case "url_token", "":
		// URL token validated by resolver lookup. No header check.
		return nil

	case "bearer":
		auth := c.Get("Authorization")
		const want = "Bearer "
		if !strings.HasPrefix(auth, want) {
			return &AuthError{Status: 401, Reason: "missing bearer token"}
		}
		gotSum := sha256.Sum256([]byte(strings.TrimPrefix(auth, want)))
		gotHex := hex.EncodeToString(gotSum[:])
		if subtle.ConstantTimeCompare([]byte(gotHex), []byte(resolved.BearerHash)) != 1 {
			return &AuthError{Status: 401, Reason: "invalid bearer token"}
		}
		return nil

	case "basic":
		// fasthttp doesn't expose Request.BasicAuth; parse the header
		// ourselves. Format: "Basic <base64(user:pass)>".
		raw := c.Get("Authorization")
		const want = "Basic "
		if !strings.HasPrefix(raw, want) {
			return &AuthError{Status: 401, Reason: "missing basic auth"}
		}
		decoded, err := decodeBasicCreds(strings.TrimPrefix(raw, want))
		if err != nil {
			return &AuthError{Status: 401, Reason: "malformed basic auth"}
		}
		user, pass := decoded[0], decoded[1]
		if subtle.ConstantTimeCompare([]byte(user), []byte(resolved.BasicUser)) != 1 {
			return &AuthError{Status: 401, Reason: "invalid basic user"}
		}
		passSum := sha256.Sum256([]byte(pass))
		passHex := hex.EncodeToString(passSum[:])
		if subtle.ConstantTimeCompare([]byte(passHex), []byte(resolved.BasicHash)) != 1 {
			return &AuthError{Status: 401, Reason: "invalid basic password"}
		}
		return nil

	default:
		return &AuthError{Status: 500, Reason: fmt.Sprintf("unknown auth strategy %q", resolved.Strategy)}
	}
}

// decodeBasicCreds splits a base64-encoded "user:pass" string.
func decodeBasicCreds(encoded string) ([2]string, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return [2]string{}, err
	}
	s := string(decoded)
	idx := strings.IndexByte(s, ':')
	if idx < 0 {
		return [2]string{}, errors.New("missing colon in basic auth")
	}
	return [2]string{s[:idx], s[idx+1:]}, nil
}

// enqueue tries to push the event onto the adapter's channel. Drops
// the event with a warning if the channel is full rather than blocking
// the HTTP response (iLink will retry on non-2xx).
func (a *Adapter) enqueue(evt pms.Event) {
	select {
	case a.events <- evt:
	default:
		log.Warn().
			Str("tenant", a.tenantID).
			Str("siteid", a.siteid).
			Str("event", evt.Type.String()).
			Str("extn", evt.Room).
			Msg("TigerTMS iLink event channel full, dropping event")
	}
}

// =============================================================================
// Request body shapes (per the PDF)
// =============================================================================

type setGuestBody struct {
	Extn      string `json:"extn"`
	Status    string `json:"status"` // "occupied" / "vacant"
	Title     string `json:"title"`
	FirstName string `json:"firstname"`
	LastName  string `json:"lastname"`
	Language  string `json:"language"`
	Group     string `json:"group"`
	VIP       string `json:"vip"`
}

type setCOSBody struct {
	Extn string `json:"extn"`
	COS  string `json:"cos"`
}

type setMWBody struct {
	Extn string `json:"extn"`
	MW   string `json:"mw"` // "on" / "off"
}

type setSipDataBody struct {
	Extn        string `json:"extn"`
	SipPassword string `json:"sippassword"`
}

type setDDIBody struct {
	Extn      string `json:"extn"`
	DDI       string `json:"ddi"`
	Operation string `json:"operation"` // "set" / "clear"
}

type setDNDBody struct {
	Extn string `json:"extn"`
	DND  string `json:"dnd"` // "on" / "off"
}

type setWakeupBody struct {
	Extn       string `json:"extn"`
	Action     string `json:"action"`     // "set" / "clear" / "clearall"
	WakeupTime string `json:"wakeuptime"` // "dd-mm-yyyy hh:mm:ss"
}

// =============================================================================
// Handlers
// =============================================================================

// handleSetGuest receives check-in / check-out events. Per the iLink
// PDF, when status transitions to "vacant" the iLink server clears MWI,
// DDI, and DND on the extension server-side. We defensively do the
// same on our side by emitting a check-out event with ilink_clear=true
// metadata; tenant.handleCheckOut already clears those on Bicom.
func (h *Handler) handleSetGuest(c *fiber.Ctx) error {
	adapter, err := h.resolveAndAuth(c)
	if err != nil {
		return nil
	}

	var body setGuestBody
	if err := c.BodyParser(&body); err != nil {
		writeFailed(c, fiber.StatusBadRequest, "invalid JSON body")
		return nil
	}
	if body.Extn == "" {
		writeFailed(c, fiber.StatusBadRequest, "extn required")
		return nil
	}

	guestName := strings.TrimSpace(body.FirstName + " " + body.LastName)

	evt := pms.Event{
		Room:      body.Extn,
		GuestName: guestName,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source": "tigertms_ilink",
			"siteid": adapter.SiteID(),
			"title":  body.Title,
			"group":  body.Group,
			"vip":    body.VIP,
			"status": body.Status,
		},
	}

	switch body.Status {
	case "occupied":
		evt.Type = pms.EventCheckIn
		evt.Status = true
		evt.Metadata["language"] = body.Language
	case "vacant":
		evt.Type = pms.EventCheckOut
		evt.Status = false
		evt.Metadata["ilink_clear"] = "true"
	default:
		writeFailed(c, fiber.StatusBadRequest,
			fmt.Sprintf("invalid status %q (expected occupied or vacant)", body.Status))
		return nil
	}

	adapter.enqueue(evt)

	log.Info().
		Str("tenant", adapter.TenantID()).
		Str("siteid", adapter.SiteID()).
		Str("extn", body.Extn).
		Str("status", body.Status).
		Str("guest", guestName).
		Msg("TigerTMS iLink setguest")

	writeSuccess(c, fmt.Sprintf("updated guest for extension %s", body.Extn))
	return nil
}

func (h *Handler) handleSetCOS(c *fiber.Ctx) error {
	adapter, err := h.resolveAndAuth(c)
	if err != nil {
		return nil
	}

	var body setCOSBody
	if err := c.BodyParser(&body); err != nil {
		writeFailed(c, fiber.StatusBadRequest, "invalid JSON body")
		return nil
	}
	if body.Extn == "" {
		writeFailed(c, fiber.StatusBadRequest, "extn required")
		return nil
	}

	evt := pms.Event{
		Type:      pms.EventRoomStatus,
		Room:      body.Extn,
		Status:    true,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source":           "tigertms_ilink",
			"siteid":           adapter.SiteID(),
			"class_of_service": body.COS,
		},
	}
	adapter.enqueue(evt)

	log.Info().
		Str("tenant", adapter.TenantID()).
		Str("siteid", adapter.SiteID()).
		Str("extn", body.Extn).
		Str("cos", body.COS).
		Msg("TigerTMS iLink setcos")

	writeSuccess(c, fmt.Sprintf("updated cos for extension %s", body.Extn))
	return nil
}

func (h *Handler) handleSetMW(c *fiber.Ctx) error {
	adapter, err := h.resolveAndAuth(c)
	if err != nil {
		return nil
	}

	var body setMWBody
	if err := c.BodyParser(&body); err != nil {
		writeFailed(c, fiber.StatusBadRequest, "invalid JSON body")
		return nil
	}
	if body.Extn == "" {
		writeFailed(c, fiber.StatusBadRequest, "extn required")
		return nil
	}

	mwOn, perr := parseOnOff(body.MW)
	if perr != nil {
		writeFailed(c, fiber.StatusBadRequest, perr.Error())
		return nil
	}

	evt := pms.Event{
		Type:      pms.EventMessageWaiting,
		Room:      body.Extn,
		Status:    mwOn,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source": "tigertms_ilink",
			"siteid": adapter.SiteID(),
		},
	}
	adapter.enqueue(evt)

	log.Info().
		Str("tenant", adapter.TenantID()).
		Str("siteid", adapter.SiteID()).
		Str("extn", body.Extn).
		Bool("mw", mwOn).
		Msg("TigerTMS iLink setmw")

	writeSuccess(c, fmt.Sprintf("updated mw for extension %s", body.Extn))
	return nil
}

// handleSetSIPData receives iConnect BYOD password updates. Per the PDF
// "If we are using iConnect to add Bring Your Own Devices (guest
// mobiles) to a system then we need the ability to modify the SIP
// password on Check-in and Check-out." There is no PBX consumer yet
// (see gap doc Tier E); we emit a RoomStatus event so the metadata is
// preserved and surfaced in the audit log.
func (h *Handler) handleSetSIPData(c *fiber.Ctx) error {
	adapter, err := h.resolveAndAuth(c)
	if err != nil {
		return nil
	}

	var body setSipDataBody
	if err := c.BodyParser(&body); err != nil {
		writeFailed(c, fiber.StatusBadRequest, "invalid JSON body")
		return nil
	}
	if body.Extn == "" {
		writeFailed(c, fiber.StatusBadRequest, "extn required")
		return nil
	}

	evt := pms.Event{
		Type:      pms.EventRoomStatus,
		Room:      body.Extn,
		Status:    true,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source":       "tigertms_ilink",
			"siteid":       adapter.SiteID(),
			"sip_password": body.SipPassword,
		},
	}
	adapter.enqueue(evt)

	log.Info().
		Str("tenant", adapter.TenantID()).
		Str("siteid", adapter.SiteID()).
		Str("extn", body.Extn).
		Bool("sip_password_set", body.SipPassword != "").
		Msg("TigerTMS iLink setsipdata")

	writeSuccess(c, fmt.Sprintf("updated sipdata for extension %s", body.Extn))
	return nil
}

func (h *Handler) handleSetDDI(c *fiber.Ctx) error {
	adapter, err := h.resolveAndAuth(c)
	if err != nil {
		return nil
	}

	var body setDDIBody
	if err := c.BodyParser(&body); err != nil {
		writeFailed(c, fiber.StatusBadRequest, "invalid JSON body")
		return nil
	}
	if body.Extn == "" {
		writeFailed(c, fiber.StatusBadRequest, "extn required")
		return nil
	}
	if body.Operation != "set" && body.Operation != "clear" {
		writeFailed(c, fiber.StatusBadRequest,
			fmt.Sprintf("invalid operation %q (expected set or clear)", body.Operation))
		return nil
	}

	evt := pms.Event{
		Type:      pms.EventRoomStatus,
		Room:      body.Extn,
		Status:    body.Operation == "set",
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source": "tigertms_ilink",
			"siteid": adapter.SiteID(),
			"ddi":    body.DDI,
			"ddi_op": body.Operation,
		},
	}
	adapter.enqueue(evt)

	log.Info().
		Str("tenant", adapter.TenantID()).
		Str("siteid", adapter.SiteID()).
		Str("extn", body.Extn).
		Str("ddi", body.DDI).
		Str("operation", body.Operation).
		Msg("TigerTMS iLink setddi")

	writeSuccess(c, fmt.Sprintf("updated ddi for extension %s", body.Extn))
	return nil
}

func (h *Handler) handleSetDND(c *fiber.Ctx) error {
	adapter, err := h.resolveAndAuth(c)
	if err != nil {
		return nil
	}

	var body setDNDBody
	if err := c.BodyParser(&body); err != nil {
		writeFailed(c, fiber.StatusBadRequest, "invalid JSON body")
		return nil
	}
	if body.Extn == "" {
		writeFailed(c, fiber.StatusBadRequest, "extn required")
		return nil
	}

	dndOn, perr := parseOnOff(body.DND)
	if perr != nil {
		writeFailed(c, fiber.StatusBadRequest, perr.Error())
		return nil
	}

	evt := pms.Event{
		Type:      pms.EventDND,
		Room:      body.Extn,
		Status:    dndOn,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source": "tigertms_ilink",
			"siteid": adapter.SiteID(),
		},
	}
	adapter.enqueue(evt)

	log.Info().
		Str("tenant", adapter.TenantID()).
		Str("siteid", adapter.SiteID()).
		Str("extn", body.Extn).
		Bool("dnd", dndOn).
		Msg("TigerTMS iLink setdnd")

	writeSuccess(c, fmt.Sprintf("updated dnd for extension %s", body.Extn))
	return nil
}

// handleSetWakeup handles set / clear / clearall. Per the PDF:
//
//   - action=set:       schedule wake-up at wakeuptime
//   - action=clear:     cancel the wake-up at wakeuptime
//   - action=clearall:  cancel ALL wake-ups for this extension
//     (wakeuptime is blank for clearall)
func (h *Handler) handleSetWakeup(c *fiber.Ctx) error {
	adapter, err := h.resolveAndAuth(c)
	if err != nil {
		return nil
	}

	var body setWakeupBody
	if err := c.BodyParser(&body); err != nil {
		writeFailed(c, fiber.StatusBadRequest, "invalid JSON body")
		return nil
	}
	if body.Extn == "" {
		writeFailed(c, fiber.StatusBadRequest, "extn required")
		return nil
	}
	switch body.Action {
	case "set", "clear", "clearall":
	default:
		writeFailed(c, fiber.StatusBadRequest,
			fmt.Sprintf("invalid action %q (expected set, clear, or clearall)", body.Action))
		return nil
	}
	if body.Action != "clearall" && body.WakeupTime == "" {
		writeFailed(c, fiber.StatusBadRequest,
			fmt.Sprintf("wakeuptime required for action %q", body.Action))
		return nil
	}

	evt := pms.Event{
		Type:      pms.EventWakeUp,
		Room:      body.Extn,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source":          "tigertms_ilink",
			"siteid":          adapter.SiteID(),
			"wakeup_action":   body.Action,
			"wakeup_time_raw": body.WakeupTime,
		},
	}

	switch body.Action {
	case "set":
		evt.Status = true
		evt.Metadata["wakeup_time_full"] = body.WakeupTime
	case "clear", "clearall":
		evt.Status = false
		if body.WakeupTime != "" {
			evt.Metadata["wakeup_time_full"] = body.WakeupTime
		}
	}

	adapter.enqueue(evt)

	log.Info().
		Str("tenant", adapter.TenantID()).
		Str("siteid", adapter.SiteID()).
		Str("extn", body.Extn).
		Str("action", body.Action).
		Str("wakeuptime", body.WakeupTime).
		Msg("TigerTMS iLink setwakeup")

	if body.Action == "clearall" {
		writeSuccess(c, fmt.Sprintf("cleared all wakeups for extension %s", body.Extn))
	} else {
		writeSuccess(c, fmt.Sprintf("set wakeup for extension %s", body.Extn))
	}
	return nil
}

// =============================================================================
// Response shapes — match the PDF: {"result":"success|failed","information":"..."}
// =============================================================================

type ilinkResponse struct {
	Result      string `json:"result"`
	Information string `json:"information"`
}

func writeSuccess(c *fiber.Ctx, information string) {
	c.Set("Content-Type", "text/json")
	c.Status(fiber.StatusOK).JSON(ilinkResponse{Result: "success", Information: information})
}

// writeFailed writes an iLink-shaped failure response with the given
// HTTP status. The status code is preserved so the caller can signal
// 400 vs 404 vs 503 to the operator, but the body shape always matches
// the PDF.
func writeFailed(c *fiber.Ctx, status int, information string) {
	c.Set("Content-Type", "text/json")
	c.Status(status).JSON(ilinkResponse{Result: "failed", Information: information})
}

// parseOnOff parses the iLink "on"/"off" string into a bool.
func parseOnOff(s string) (bool, error) {
	switch s {
	case "on", "On", "ON", "1", "true", "TRUE", "True":
		return true, nil
	case "off", "Off", "OFF", "0", "false", "FALSE", "False", "":
		return false, nil
	default:
		return false, errors.New("invalid value: expected on or off")
	}
}
