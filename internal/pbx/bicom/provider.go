// Package bicom provides the Bicom PBXware implementation of the pbx.Provider interface.
// It combines the Bicom REST API with ARI for Asterisk-based operations.
package bicom

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	ari "github.com/CyCoreSystems/ari/v6"
	"github.com/CyCoreSystems/ari/v6/client/native"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/bicom"
	"github.com/sagostin/pbx-hospitality/internal/pbx"
)

func init() {
	// Register this provider with the pbx registry
	pbx.Register("bicom", func(cfg pbx.ProviderConfig) (pbx.Provider, error) {
		return NewProvider(Config{
			APIURL:        cfg.BicomAPIURL,
			APIKey:        cfg.BicomAPIKey,
			TenantID:      cfg.BicomTenantID,
			ARIURL:        cfg.ARIURL,
			ARIWSUrl:      cfg.ARIWSUrl,
			ARIUser:       cfg.ARIUser,
			ARIPass:       cfg.ARIPass,
			ARIAppName:    cfg.ARIAppName,
		WebhookSecret: cfg.WebhookSecret,
		})
	})
}

// Config holds configuration for the Bicom provider
type Config struct {
	// Bicom REST API settings
	APIURL   string
	APIKey   string
	TenantID string

	// ARI settings (for Asterisk operations)
	ARIURL     string
	ARIWSUrl   string
	ARIUser    string
	ARIPass    string
	ARIAppName string

	// Webhook settings for inbound call events via HTTP
	WebhookSecret string
}

// Provider implements pbx.Provider for Bicom PBXware.
// It uses the REST API for configuration operations and ARI for real-time control.
type Provider struct {
	cfg       Config
	apiClient *bicom.Client
	ariClient ari.Client
	ariMu     sync.RWMutex
	connected bool
	cancel    context.CancelFunc

	// Event channel for inbound call events
	events chan pbx.CallEvent

	// Subscriptions for ARI event handlers
	eventSub ari.Subscription

	// Reconnection handling
	reconnectMu   sync.Mutex
	reconnecting  bool
	reconnectWg   sync.WaitGroup

	// Access code pattern (e.g., *411)
	accessCodePattern *regexp.Regexp
}

// NewProvider creates a new Bicom PBXware provider
func NewProvider(cfg Config) (*Provider, error) {
	p := &Provider{
		cfg: cfg,
	}

	// Initialize event channel with buffer
	p.events = make(chan pbx.CallEvent, 100)

	// Initialize access code pattern (matches *XXX patterns like *411)
	p.accessCodePattern = regexp.MustCompile(`^\*(\d+)$`)

	// Initialize Bicom REST API client if configured
	if cfg.APIURL != "" && cfg.APIKey != "" {
		client, err := bicom.NewClient(bicom.Config{
			BaseURL:  cfg.APIURL,
			APIKey:   cfg.APIKey,
			TenantID: cfg.TenantID,
		})
		if err != nil {
			return nil, fmt.Errorf("creating Bicom API client: %w", err)
		}
		p.apiClient = client
	}

	return p, nil
}

// Connect establishes connections to Bicom services (primarily ARI)
func (p *Provider) Connect(ctx context.Context) error {
	ctx, p.cancel = context.WithCancel(ctx)

	// Connect to ARI if settings are provided
	if p.cfg.ARIURL != "" {
		wsURL := p.cfg.ARIWSUrl
		if wsURL == "" {
			wsURL = p.cfg.ARIURL + "/events"
		}

		appName := p.cfg.ARIAppName
		if appName == "" {
			appName = "bicom-hospitality"
		}

		client, err := native.Connect(&native.Options{
			Application:  appName,
			URL:          p.cfg.ARIURL,
			WebsocketURL: wsURL,
			Username:     p.cfg.ARIUser,
			Password:     p.cfg.ARIPass,
		})
		if err != nil {
			return fmt.Errorf("connecting to ARI: %w", err)
		}

		p.ariMu.Lock()
		p.ariClient = client
		p.connected = true
		p.ariMu.Unlock()

		// Subscribe to ARI Stasis events
		p.subscribeToEvents()

		log.Info().
			Str("url", p.cfg.ARIURL).
			Str("app", appName).
			Msg("Bicom provider connected to ARI")
	} else {
		// Mark as connected even without ARI (API-only mode)
		p.connected = true
		log.Info().Msg("Bicom provider initialized (API-only mode, no ARI)")
	}

	return nil
}

// Close terminates all Bicom connections
func (p *Provider) Close() error {
	if p.cancel != nil {
		p.cancel()
	}

	p.ariMu.Lock()
	defer p.ariMu.Unlock()

	if p.eventSub != nil {
		p.eventSub.Cancel()
		p.eventSub = nil
	}

	if p.ariClient != nil {
		p.ariClient.Close()
		p.ariClient = nil
	}
	p.connected = false

	// Close event channel
	close(p.events)

	return nil
}

// Connected returns true if the provider is connected
func (p *Provider) Connected() bool {
	p.ariMu.RLock()
	defer p.ariMu.RUnlock()
	return p.connected
}

// Capabilities returns what this provider supports
func (p *Provider) Capabilities() pbx.Capabilities {
	return pbx.Capabilities{
		SupportsWakeUpCalls:       p.apiClient != nil,
		SupportsVoicemailGreeting: p.apiClient != nil,
		SupportsCallForward:       p.apiClient != nil,
		SupportsMWI:               true, // via ARI or API
		SupportsDND:               true, // via ARI or API
		SupportsInboundEvents:     p.cfg.ARIURL != "", // via ARI WebSocket or HTTP webhook
	}
}

// =============================================================================
// Extension Management
// =============================================================================

// UpdateExtensionName updates the caller ID name for an extension
func (p *Provider) UpdateExtensionName(ctx context.Context, ext, name string) error {
	if p.apiClient != nil {
		return p.apiClient.UpdateExtensionName(ctx, ext, name)
	}

	// Fallback: log warning if no API client
	log.Warn().
		Str("extension", ext).
		Str("name", name).
		Msg("Cannot update extension name: Bicom API not configured")
	return nil
}

// =============================================================================
// Voicemail Management
// =============================================================================

// DeleteAllVoicemails deletes all voicemail messages for an extension
func (p *Provider) DeleteAllVoicemails(ctx context.Context, ext string) error {
	if p.apiClient != nil {
		return p.apiClient.DeleteAllVoicemails(ctx, ext)
	}
	return fmt.Errorf("Bicom API not configured")
}

// ResetVoicemailGreeting resets the voicemail greeting to default
func (p *Provider) ResetVoicemailGreeting(ctx context.Context, ext string) error {
	if p.apiClient != nil {
		return p.apiClient.ResetVoicemailGreeting(ctx, ext)
	}
	return fmt.Errorf("Bicom API not configured")
}

// ClearVoicemailForGuest performs all voicemail cleanup for guest checkout
func (p *Provider) ClearVoicemailForGuest(ctx context.Context, ext string) error {
	if p.apiClient != nil {
		return p.apiClient.ClearVoicemailForGuest(ctx, ext)
	}
	return fmt.Errorf("Bicom API not configured")
}

// =============================================================================
// Message Waiting Indicator
// =============================================================================

// SetMWI sets the message waiting indicator for an extension
func (p *Provider) SetMWI(ctx context.Context, ext string, on bool) error {
	p.ariMu.RLock()
	defer p.ariMu.RUnlock()

	if p.ariClient == nil {
		return fmt.Errorf("ARI not connected")
	}

	// Use ARI to update mailbox state
	mailbox := ext + "@default"
	var newMessages, oldMessages int
	if on {
		newMessages = 1
	}

	mailboxKey := ari.NewKey(ari.MailboxKey, mailbox)
	if err := p.ariClient.Mailbox().Update(mailboxKey, oldMessages, newMessages); err != nil {
		return fmt.Errorf("updating mailbox MWI: %w", err)
	}

	log.Debug().
		Str("extension", ext).
		Bool("on", on).
		Msg("MWI updated via ARI")

	return nil
}

// =============================================================================
// Do Not Disturb
// =============================================================================

// SetDND enables or disables Do Not Disturb for an extension
func (p *Provider) SetDND(ctx context.Context, ext string, on bool) error {
	// Prefer API for persistent DND setting
	if p.apiClient != nil {
		return p.apiClient.SetDND(ctx, ext, on)
	}

	// Fallback to logging if no API
	log.Warn().
		Str("extension", ext).
		Bool("on", on).
		Msg("Cannot set DND: Bicom API not configured")
	return nil
}

// =============================================================================
// Wake-Up Calls
// =============================================================================

// ScheduleWakeUpCall schedules a wake-up call for an extension
func (p *Provider) ScheduleWakeUpCall(ctx context.Context, ext string, wakeTime time.Time) error {
	if p.apiClient == nil {
		return fmt.Errorf("Bicom API not configured")
	}
	return p.apiClient.ScheduleWakeUpCall(ctx, ext, wakeTime)
}

// CancelWakeUpCall cancels a scheduled wake-up call
func (p *Provider) CancelWakeUpCall(ctx context.Context, ext string) error {
	if p.apiClient == nil {
		return fmt.Errorf("Bicom API not configured")
	}
	return p.apiClient.CancelWakeUpCall(ctx, ext)
}

// =============================================================================
// Call Forwarding
// =============================================================================

// SetCallForward configures call forwarding for an extension
func (p *Provider) SetCallForward(ctx context.Context, ext, destination string, enabled bool) error {
	if p.apiClient == nil {
		return fmt.Errorf("Bicom API not configured")
	}
	return p.apiClient.SetCallForward(ctx, ext, destination, enabled)
}

// =============================================================================
// EventProvider Interface - Inbound Call Events via ARI Stasis WebSocket
// =============================================================================

// Events returns the channel of inbound call events
func (p *Provider) Events() <-chan pbx.CallEvent {
	return p.events
}

// =============================================================================
// WebhookProvider Interface - Inbound Call Events via HTTP
// =============================================================================

// WebhookSecret returns the secret for validating webhook requests
func (p *Provider) WebhookSecret() string {
	return p.cfg.WebhookSecret
}

// HandleWebhook processes an incoming webhook request from the PBX
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
	event := p.mapWebhookEventToCallEvent(payload)
	if event == nil {
		return nil // Unknown event type
	}

	// Non-blocking send to event channel
	select {
	case p.events <- *event:
		log.Debug().
			Str("event", payload.Event).
			Str("extension", payload.Extension).
			Msg("Bicom webhook event received")
	default:
		log.Warn().Msg("Bicom event channel full, dropping event")
	}

	return nil
}

// validateWebhookSignature validates the HMAC signature of a webhook request
func (p *Provider) validateWebhookSignature(r *http.Request) error {
	signature := r.Header.Get("X-Webhook-Signature")
	if signature == "" {
		return fmt.Errorf("missing signature header")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(strings.NewReader(string(body))) // Reset body for later reading

	mac := hmac.New(sha256.New, []byte(p.cfg.WebhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return fmt.Errorf("signature mismatch")
	}

	return nil
}

// mapWebhookEventToCallEvent converts a webhook payload to a CallEvent
func (p *Provider) mapWebhookEventToCallEvent(payload struct {
	Event      string `json:"event"`
	Extension  string `json:"extension"`
	CallerID   string `json:"caller_id"`
	CallerName string `json:"caller_name"`
	AccessCode string `json:"access_code"`
	Timestamp  string `json:"timestamp"`
}) *pbx.CallEvent {
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
		log.Debug().Str("event", payload.Event).Msg("Unknown Bicom webhook event type")
		return nil
	}

	return &pbx.CallEvent{
		Type:       eventType,
		Extension:  payload.Extension,
		CallerID:   payload.CallerID,
		CallerName: payload.CallerName,
		AccessCode: payload.AccessCode,
		Timestamp:  time.Now(),
		Metadata:   make(map[string]string),
	}
}

// =============================================================================
// ARI Event Subscription and Handling
// =============================================================================

// subscribeToEvents subscribes to ARI Stasis events
func (p *Provider) subscribeToEvents() {
	p.ariMu.RLock()
	client := p.ariClient
	p.ariMu.RUnlock()

	if client == nil {
		return
	}

	// Subscribe to Stasis events
	p.eventSub = client.Bridge().Subscribe(nil, "StasisStart", "StasisEnd")
	if p.eventSub == nil {
		log.Warn().Msg("Failed to subscribe to ARI Stasis events")
		return
	}

	// Handle events in a goroutine
	go p.handleARIEvents()

	log.Info().Msg("Subscribed to ARI Stasis events")
}

// handleARIEvents processes ARI events and converts them to pbx.CallEvent
func (p *Provider) handleARIEvents() {
	if p.eventSub == nil {
		return
	}

	for v := range p.eventSub.Events() {
		if v == nil {
			continue
		}
		p.processARIEvent(v)
	}

	// Subscription channel closed - attempt reconnect
	log.Info().Msg("ARI event subscription closed, attempting reconnect")
	p.handleReconnect()
}

// handleReconnect attempts to reconnect to ARI and resubscribe
func (p *Provider) handleReconnect() {
	p.reconnectMu.Lock()
	if p.reconnecting {
		p.reconnectMu.Unlock()
		return
	}
	p.reconnecting = true
	p.reconnectMu.Unlock()

	defer func() {
		p.reconnectMu.Lock()
		p.reconnecting = false
		p.reconnectMu.Unlock()
	}()

	for i := 0; i < 5; i++ {
		log.Info().Int("attempt", i+1).Msg("Attempting ARI reconnection")

		p.ariMu.RLock()
		connected := p.connected
		p.ariMu.RUnlock()

		if !connected {
			return
		}

		// Reconnect
		ctx, cancel := context.WithCancel(context.Background())
		if err := p.Connect(ctx); err != nil {
			cancel()
			log.Error().Err(err).Int("attempt", i+1).Msg("ARI reconnection failed")
			time.Sleep(time.Duration(i+1) * time.Second)
			continue
		}
		cancel()

		log.Info().Msg("ARI reconnection successful")
		return
	}

	log.Error().Msg("ARI reconnection failed after 5 attempts")
}

// processARIEvent converts an ARI event to a pbx.CallEvent
func (p *Provider) processARIEvent(v interface{}) {
	switch event := v.(type) {
	case *ari.StasisStart:
		p.handleStasisStart(event)
	case *ari.StasisEnd:
		p.handleStasisEnd(event)
	case *ari.ChannelStateChange:
		p.handleChannelStateChange(event)
	default:
		log.Debug().Str("type", fmt.Sprintf("%T", v)).Msg("Unhandled ARI event type")
	}
}

// handleStasisStart handles channel entering Stasis
func (p *Provider) handleStasisStart(event *ari.StasisStart) {
	if event == nil {
		return
	}

	// ChannelData is a struct, check if ID is empty
	if event.Channel.ID == "" {
		return
	}

	// Extract dialed number (extension)
	dialed := extractDialedNumber(event.Channel)

	// Determine event type based on dialed number
	var eventType pbx.CallEventType
	if p.isAccessCode(dialed) {
		eventType = pbx.CallEventAccessCode
	} else {
		eventType = pbx.CallEventIncoming
	}

	// Extract caller info
	callerID := ""
	callerName := ""
	if event.Channel.Caller != nil {
		callerID = event.Channel.Caller.Number
		callerName = event.Channel.Caller.Name
	}

	ce := pbx.CallEvent{
		Type:       eventType,
		Extension:  dialed,
		CallerID:   callerID,
		CallerName: callerName,
		Timestamp:  time.Now(),
		Metadata: map[string]string{
			"channel_id": event.Channel.ID,
		},
	}

	if eventType == pbx.CallEventAccessCode {
		ce.AccessCode = dialed
	}

	p.sendCallEvent(ce)
}

// handleStasisEnd handles channel leaving Stasis
func (p *Provider) handleStasisEnd(event *ari.StasisEnd) {
	if event == nil {
		return
	}

	if event.Channel.ID == "" {
		return
	}

	// Extract caller info before we lose it
	callerID := ""
	if event.Channel.Caller != nil {
		callerID = event.Channel.Caller.Number
	}

	ce := pbx.CallEvent{
		Type:       pbx.CallEventCallEnded,
		CallerID:   callerID,
		Timestamp:  time.Now(),
		Metadata: map[string]string{
			"channel_id": event.Channel.ID,
		},
	}

	p.sendCallEvent(ce)
}

// handleChannelStateChange handles channel state changes
func (p *Provider) handleChannelStateChange(event *ari.ChannelStateChange) {
	if event == nil {
		return
	}

	if event.Channel.ID == "" {
		return
	}

	// Log channel state changes for debugging voicemail scenarios
	log.Debug().
		Str("channel_id", event.Channel.ID).
		Str("state", event.Channel.State).
		Msg("Channel state change")
}

// isAccessCode checks if the dialed number is an access code (e.g., *411)
func (p *Provider) isAccessCode(dialed string) bool {
	if dialed == "" {
		return false
	}
	return p.accessCodePattern.MatchString(dialed)
}

// sendCallEvent sends a call event to the channel, with non-blocking behavior
func (p *Provider) sendCallEvent(event pbx.CallEvent) {
	select {
	case p.events <- event:
		log.Debug().
			Str("type", event.Type.String()).
			Str("extension", event.Extension).
			Msg("Call event sent")
	default:
		log.Warn().Msg("Event channel full, dropping call event")
	}
}

// extractDialedNumber extracts the dialed number from an ARI ChannelData
func extractDialedNumber(channel ari.ChannelData) string {
	// Try to get dialed number from channel dialplan fields
	if channel.Dialplan != nil && channel.Dialplan.Exten != "" {
		return channel.Dialplan.Exten
	}

	// Fallback: try to extract from connected channel
	if channel.Connected != nil && channel.Connected.Number != "" {
		return channel.Connected.Number
	}

	// Last resort: use channel ID (not ideal but better than nothing)
	return channel.ID
}

// Ensure Provider implements pbx.Provider and pbx.ProviderWithCapabilities
var _ pbx.Provider = (*Provider)(nil)
var _ pbx.ProviderWithCapabilities = (*Provider)(nil)
var _ pbx.EventProvider = (*Provider)(nil)
var _ pbx.WebhookProvider = (*Provider)(nil)
