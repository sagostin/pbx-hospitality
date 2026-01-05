// Package bicom provides the Bicom PBXware implementation of the pbx.Provider interface.
// It combines the Bicom REST API with ARI for Asterisk-based operations.
package bicom

import (
	"context"
	"fmt"
	"sync"
	"time"

	arilib "github.com/CyCoreSystems/ari/v6"
	"github.com/CyCoreSystems/ari/v6/client/native"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/bicom"
	"github.com/sagostin/pbx-hospitality/internal/pbx"
)

func init() {
	// Register this provider with the pbx registry
	pbx.Register("bicom", func(cfg pbx.ProviderConfig) (pbx.Provider, error) {
		return NewProvider(Config{
			APIURL:     cfg.BicomAPIURL,
			APIKey:     cfg.BicomAPIKey,
			TenantID:   cfg.BicomTenantID,
			ARIURL:     cfg.ARIURL,
			ARIWSUrl:   cfg.ARIWSUrl,
			ARIUser:    cfg.ARIUser,
			ARIPass:    cfg.ARIPass,
			ARIAppName: cfg.ARIAppName,
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
}

// Provider implements pbx.Provider for Bicom PBXware.
// It uses the REST API for configuration operations and ARI for real-time control.
type Provider struct {
	cfg       Config
	apiClient *bicom.Client
	ariClient arilib.Client
	ariMu     sync.RWMutex
	connected bool
	cancel    context.CancelFunc
}

// NewProvider creates a new Bicom PBXware provider
func NewProvider(cfg Config) (*Provider, error) {
	p := &Provider{
		cfg: cfg,
	}

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

	if p.ariClient != nil {
		p.ariClient.Close()
		p.ariClient = nil
	}
	p.connected = false

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

	mailboxKey := arilib.NewKey(arilib.MailboxKey, mailbox)
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

// Ensure Provider implements pbx.Provider and pbx.ProviderWithCapabilities
var _ pbx.Provider = (*Provider)(nil)
var _ pbx.ProviderWithCapabilities = (*Provider)(nil)
