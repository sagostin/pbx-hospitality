// Package zultys provides the Zultys PBX implementation of the pbx.Provider interface.
// It uses session-based authentication and HTTP webhooks for bidirectional communication.
package zultys

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/topsoffice/bicom-hospitality/internal/pbx"
)

// ErrWakeUpNotSupported is returned when wake-up calls are attempted on Zultys
var ErrWakeUpNotSupported = errors.New("wake-up calls not directly supported on Zultys (requires external scheduler)")

func init() {
	// Register this provider with the pbx registry
	pbx.Register("zultys", func(cfg pbx.ProviderConfig) (pbx.Provider, error) {
		return NewProvider(Config{
			APIURL:        cfg.APIURL,
			AuthURL:       cfg.AuthURL,
			Username:      cfg.Username,
			Password:      cfg.Password,
			WebhookSecret: cfg.WebhookSecret,
		})
	})
}

// Config holds configuration for the Zultys provider
type Config struct {
	APIURL        string // Base API URL, e.g., "https://zultys.hotel.com/api"
	AuthURL       string // Login endpoint, e.g., "/auth/login"
	Username      string
	Password      string
	WebhookSecret string // Secret for validating inbound webhooks
}

// Session holds an authenticated session token
type Session struct {
	Token     string
	ExpiresAt time.Time
}

// Provider implements pbx.Provider and pbx.WebhookProvider for Zultys PBX.
type Provider struct {
	cfg       Config
	session   *Session
	sessionMu sync.RWMutex
	events    chan pbx.CallEvent
	connected bool
	cancel    context.CancelFunc
	client    *http.Client
}

// NewProvider creates a new Zultys PBX provider
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.APIURL == "" {
		return nil, fmt.Errorf("Zultys API URL is required")
	}

	return &Provider{
		cfg:    cfg,
		events: make(chan pbx.CallEvent, 100),
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Connect establishes connection (fetches initial session token)
func (p *Provider) Connect(ctx context.Context) error {
	ctx, p.cancel = context.WithCancel(ctx)

	// Fetch initial session if auth is configured
	if p.cfg.AuthURL != "" && p.cfg.Username != "" {
		if _, err := p.getSession(ctx); err != nil {
			log.Warn().Err(err).Msg("Failed to fetch initial Zultys session (will retry on demand)")
		}
	}

	p.connected = true
	log.Info().Str("api_url", p.cfg.APIURL).Msg("Zultys provider connected")
	return nil
}

// Close terminates the provider
func (p *Provider) Close() error {
	if p.cancel != nil {
		p.cancel()
	}
	close(p.events)
	p.connected = false
	return nil
}

// Connected returns true if the provider is connected
func (p *Provider) Connected() bool {
	return p.connected
}

// Capabilities returns what this provider supports
func (p *Provider) Capabilities() pbx.Capabilities {
	return pbx.Capabilities{
		SupportsWakeUpCalls:       false, // Requires external scheduler (e.g., FreeSWITCH)
		SupportsVoicemailGreeting: true,
		SupportsCallForward:       true,
		SupportsMWI:               true,
		SupportsDND:               true,
		SupportsInboundEvents:     true,
	}
}

// Events returns the channel of inbound call events
func (p *Provider) Events() <-chan pbx.CallEvent {
	return p.events
}

// WebhookSecret returns the secret for validating webhook requests
func (p *Provider) WebhookSecret() string {
	return p.cfg.WebhookSecret
}

// =============================================================================
// Session Management
// =============================================================================

// getSession returns a valid session, refreshing if needed
func (p *Provider) getSession(ctx context.Context) (*Session, error) {
	p.sessionMu.RLock()
	if p.session != nil && time.Now().Before(p.session.ExpiresAt) {
		defer p.sessionMu.RUnlock()
		return p.session, nil
	}
	p.sessionMu.RUnlock()

	// Need to refresh session
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()

	// Double-check after acquiring write lock
	if p.session != nil && time.Now().Before(p.session.ExpiresAt) {
		return p.session, nil
	}

	session, err := p.fetchSession(ctx)
	if err != nil {
		return nil, err
	}

	p.session = session
	log.Debug().Time("expires", session.ExpiresAt).Msg("Zultys session refreshed")
	return session, nil
}

// fetchSession authenticates and returns a new session
func (p *Provider) fetchSession(ctx context.Context) (*Session, error) {
	authURL := p.cfg.APIURL + p.cfg.AuthURL

	payload := map[string]string{
		"username": p.cfg.Username,
		"password": p.cfg.Password,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", authURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating auth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth failed with status %d", resp.StatusCode)
	}

	var result struct {
		Token     string `json:"token"`
		SessionID string `json:"session_id"` // Alternative field name
		ExpiresIn int    `json:"expires_in"` // Seconds until expiry
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing auth response: %w", err)
	}

	token := result.Token
	if token == "" {
		token = result.SessionID
	}
	if token == "" {
		return nil, fmt.Errorf("no token in auth response")
	}

	expiresIn := result.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600 // Default 1 hour
	}

	return &Session{
		Token:     token,
		ExpiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}

// doRequest performs an authenticated API request
func (p *Provider) doRequest(ctx context.Context, method, path string, payload interface{}) (*http.Response, error) {
	session, err := p.getSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}

	url := p.cfg.APIURL + path

	var body io.Reader
	if payload != nil {
		data, _ := json.Marshal(payload)
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+session.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}

	// If unauthorized, clear session and retry once
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		p.sessionMu.Lock()
		p.session = nil
		p.sessionMu.Unlock()

		session, err = p.getSession(ctx)
		if err != nil {
			return nil, fmt.Errorf("re-auth failed: %w", err)
		}

		req, _ = http.NewRequestWithContext(ctx, method, url, body)
		req.Header.Set("Authorization", "Bearer "+session.Token)
		req.Header.Set("Content-Type", "application/json")
		return p.client.Do(req)
	}

	return resp, nil
}

// =============================================================================
// Webhook Handling
// =============================================================================

// HandleWebhook processes an incoming webhook request from Zultys
func (p *Provider) HandleWebhook(r *http.Request) error {
	// Validate signature if secret is configured
	if p.cfg.WebhookSecret != "" {
		if err := p.validateWebhookSignature(r); err != nil {
			return fmt.Errorf("invalid webhook signature: %w", err)
		}
	}

	// Parse the webhook payload
	var payload struct {
		Event      string `json:"event"`
		Extension  string `json:"extension"`
		CallerID   string `json:"caller_id"`
		CallerName string `json:"caller_name"`
		AccessCode string `json:"access_code"`
		Timestamp  string `json:"timestamp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return fmt.Errorf("parsing webhook payload: %w", err)
	}

	// Map to CallEvent
	var eventType pbx.CallEventType
	switch payload.Event {
	case "access_code", "feature_code":
		eventType = pbx.CallEventAccessCode
	case "incoming", "call_start":
		eventType = pbx.CallEventIncoming
	case "voicemail", "voicemail_left":
		eventType = pbx.CallEventVoicemailLeft
	case "hangup", "call_end":
		eventType = pbx.CallEventCallEnded
	default:
		log.Debug().Str("event", payload.Event).Msg("Unknown Zultys webhook event type")
		return nil // Ignore unknown events
	}

	event := pbx.CallEvent{
		Type:       eventType,
		Extension:  payload.Extension,
		CallerID:   payload.CallerID,
		CallerName: payload.CallerName,
		AccessCode: payload.AccessCode,
		Timestamp:  time.Now(),
		Metadata:   make(map[string]string),
	}

	// Non-blocking send to event channel
	select {
	case p.events <- event:
		log.Debug().
			Str("event", payload.Event).
			Str("extension", payload.Extension).
			Msg("Zultys webhook event received")
	default:
		log.Warn().Msg("Zultys event channel full, dropping event")
	}

	return nil
}

func (p *Provider) validateWebhookSignature(r *http.Request) error {
	signature := r.Header.Get("X-Webhook-Signature")
	if signature == "" {
		return fmt.Errorf("missing signature header")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body)) // Reset body for later reading

	mac := hmac.New(sha256.New, []byte(p.cfg.WebhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return fmt.Errorf("signature mismatch")
	}

	return nil
}

// =============================================================================
// Provider Interface Implementation (Outbound Operations)
// =============================================================================

// UpdateExtensionName updates the caller ID name for an extension
func (p *Provider) UpdateExtensionName(ctx context.Context, ext, name string) error {
	resp, err := p.doRequest(ctx, "POST", "/extensions/"+ext+"/name", map[string]string{
		"name": name,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to update extension name: status %d", resp.StatusCode)
	}

	log.Info().Str("extension", ext).Str("name", name).Msg("Zultys extension name updated")
	return nil
}

// DeleteAllVoicemails deletes all voicemail messages for an extension
func (p *Provider) DeleteAllVoicemails(ctx context.Context, ext string) error {
	resp, err := p.doRequest(ctx, "DELETE", "/voicemail/"+ext+"/messages", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to delete voicemails: status %d", resp.StatusCode)
	}

	log.Info().Str("extension", ext).Msg("Zultys voicemails deleted")
	return nil
}

// ResetVoicemailGreeting resets the voicemail greeting to default
func (p *Provider) ResetVoicemailGreeting(ctx context.Context, ext string) error {
	resp, err := p.doRequest(ctx, "POST", "/voicemail/"+ext+"/greeting/reset", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to reset greeting: status %d", resp.StatusCode)
	}

	log.Info().Str("extension", ext).Msg("Zultys voicemail greeting reset")
	return nil
}

// ClearVoicemailForGuest performs all voicemail cleanup for guest checkout
func (p *Provider) ClearVoicemailForGuest(ctx context.Context, ext string) error {
	if err := p.DeleteAllVoicemails(ctx, ext); err != nil {
		log.Error().Err(err).Str("extension", ext).Msg("Failed to delete voicemails")
	}

	if err := p.ResetVoicemailGreeting(ctx, ext); err != nil {
		return err
	}

	return nil
}

// SetMWI sets the message waiting indicator for an extension
func (p *Provider) SetMWI(ctx context.Context, ext string, on bool) error {
	resp, err := p.doRequest(ctx, "POST", "/extensions/"+ext+"/mwi", map[string]bool{
		"enabled": on,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set MWI: status %d", resp.StatusCode)
	}

	log.Debug().Str("extension", ext).Bool("on", on).Msg("Zultys MWI updated")
	return nil
}

// SetDND enables or disables Do Not Disturb for an extension
func (p *Provider) SetDND(ctx context.Context, ext string, on bool) error {
	resp, err := p.doRequest(ctx, "POST", "/extensions/"+ext+"/dnd", map[string]bool{
		"enabled": on,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set DND: status %d", resp.StatusCode)
	}

	log.Info().Str("extension", ext).Bool("on", on).Msg("Zultys DND updated")
	return nil
}

// ScheduleWakeUpCall is not directly supported on Zultys
// Wake-up calls require an external scheduler (e.g., FreeSWITCH originate)
func (p *Provider) ScheduleWakeUpCall(ctx context.Context, ext string, wakeTime time.Time) error {
	log.Warn().
		Str("extension", ext).
		Str("time", wakeTime.Format("15:04")).
		Msg("Wake-up calls not supported on Zultys (requires external scheduler)")
	return ErrWakeUpNotSupported
}

// CancelWakeUpCall is not directly supported on Zultys
func (p *Provider) CancelWakeUpCall(ctx context.Context, ext string) error {
	log.Warn().
		Str("extension", ext).
		Msg("Wake-up call cancellation not supported on Zultys")
	return ErrWakeUpNotSupported
}

// SetCallForward configures call forwarding for an extension
func (p *Provider) SetCallForward(ctx context.Context, ext, destination string, enabled bool) error {
	resp, err := p.doRequest(ctx, "POST", "/extensions/"+ext+"/forward", map[string]interface{}{
		"destination": destination,
		"enabled":     enabled,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to set call forward: status %d", resp.StatusCode)
	}

	log.Info().Str("extension", ext).Str("destination", destination).Bool("enabled", enabled).Msg("Zultys call forward updated")
	return nil
}

// Ensure Provider implements the required interfaces
var _ pbx.Provider = (*Provider)(nil)
var _ pbx.ProviderWithCapabilities = (*Provider)(nil)
var _ pbx.WebhookProvider = (*Provider)(nil)
