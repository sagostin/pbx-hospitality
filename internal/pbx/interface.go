// Package pbx defines the interface for PBX providers.
// This abstraction allows supporting multiple PBX backends (Bicom, FreeSWITCH, 3CX, etc.)
// while keeping the core hospitality logic PBX-agnostic.
package pbx

import (
	"context"
	"net/http"
	"time"
)

// Provider defines the interface for hospitality-focused PBX operations.
// Implementations handle the specifics of each PBX platform.
type Provider interface {
	// Connection lifecycle
	Connect(ctx context.Context) error
	Close() error
	Connected() bool

	// Extension management
	UpdateExtensionName(ctx context.Context, ext, name string) error

	// Voicemail management
	DeleteAllVoicemails(ctx context.Context, ext string) error
	ResetVoicemailGreeting(ctx context.Context, ext string) error
	ClearVoicemailForGuest(ctx context.Context, ext string) error

	// Message Waiting Indicator
	SetMWI(ctx context.Context, ext string, on bool) error

	// Do Not Disturb
	SetDND(ctx context.Context, ext string, on bool) error

	// Wake-up calls
	ScheduleWakeUpCall(ctx context.Context, ext string, wakeTime time.Time) error
	CancelWakeUpCall(ctx context.Context, ext string) error

	// Call forwarding
	SetCallForward(ctx context.Context, ext, destination string, enabled bool) error
}

// Config holds common configuration for PBX providers.
// Specific providers may have additional fields.
type Config struct {
	// Type identifies the PBX backend: "bicom", "freeswitch", "3cx", etc.
	Type string `yaml:"type"`
}

// Capabilities describes what features a PBX provider supports.
// This allows graceful degradation when a feature isn't available.
type Capabilities struct {
	SupportsWakeUpCalls       bool
	SupportsVoicemailGreeting bool
	SupportsCallForward       bool
	SupportsMWI               bool
	SupportsDND               bool
	SupportsInboundEvents     bool
}

// ProviderWithCapabilities extends Provider with capability introspection.
type ProviderWithCapabilities interface {
	Provider
	Capabilities() Capabilities
}

// =============================================================================
// Inbound Call Events
// =============================================================================

// CallEventType identifies the type of call event from the PBX
type CallEventType int

const (
	// CallEventAccessCode is when a guest dials an access code (*411, etc.)
	CallEventAccessCode CallEventType = iota
	// CallEventIncoming is an incoming call to a room extension
	CallEventIncoming
	// CallEventVoicemailLeft is when a voicemail is left
	CallEventVoicemailLeft
	// CallEventCallEnded is when a call ends
	CallEventCallEnded
)

func (t CallEventType) String() string {
	switch t {
	case CallEventAccessCode:
		return "access_code"
	case CallEventIncoming:
		return "incoming"
	case CallEventVoicemailLeft:
		return "voicemail_left"
	case CallEventCallEnded:
		return "call_ended"
	default:
		return "unknown"
	}
}

// CallEvent represents an inbound call event from the PBX
type CallEvent struct {
	Type       CallEventType
	Extension  string // Called extension or access code dialed
	CallerID   string // Calling party number
	CallerName string // Calling party name (if available)
	AccessCode string // Access code dialed (for CallEventAccessCode)
	Timestamp  time.Time
	Metadata   map[string]string // PBX-specific additional fields
}

// EventProvider is implemented by PBX providers that can receive call events.
// This is used for providers with persistent connections (like ARI WebSocket).
type EventProvider interface {
	Provider
	Events() <-chan CallEvent
}

// WebhookProvider is implemented by providers that receive events via HTTP webhooks.
// The API layer calls HandleWebhook when a request arrives at the tenant's webhook URL.
type WebhookProvider interface {
	EventProvider
	// HandleWebhook processes an incoming webhook request from the PBX.
	// Returns an error if the request is invalid or cannot be processed.
	HandleWebhook(r *http.Request) error
	// WebhookSecret returns the secret for validating webhook requests.
	// Empty string means no validation required.
	WebhookSecret() string
}
