// Package websocket implements a WebSocket bridge for forwarding PMS events
// to the cloud platform with multi-tenant routing and exponential backoff
// reconnection.
package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/metrics"
	"github.com/sagostin/pbx-hospitality/internal/pms"
)

const (
	// DefaultReconnectBaseDelay is the base delay for exponential backoff
	DefaultReconnectBaseDelay = 1 * time.Second

	// DefaultReconnectMaxDelay is the maximum delay cap for exponential backoff
	DefaultReconnectMaxDelay = 60 * time.Second

	// DefaultReconnectMaxAttempts is the maximum number of reconnection attempts
	// 0 means unlimited
	DefaultReconnectMaxAttempts = 0

	// DefaultPingInterval is the interval for sending ping frames to keep connection alive
	DefaultPingInterval = 30 * time.Second

	// DefaultPongTimeout is the time to wait for a pong response
	DefaultPongTimeout = 10 * time.Second

	// DefaultHandshakeTimeout is the timeout for WebSocket handshake
	DefaultHandshakeTimeout = 10 * time.Second

	// DefaultWriteTimeout is the timeout for write operations
	DefaultWriteTimeout = 5 * time.Second

	// BufferSize is the size of the event channel buffer
	BufferSize = 100
)

// Config holds WebSocket bridge configuration
type Config struct {
	// CloudURL is the WebSocket endpoint of the cloud platform
	CloudURL string

	// TenantID identifies this tenant in the cloud platform
	TenantID string

	// AuthToken is the bearer token for authentication (optional)
	AuthToken string

	// ReconnectBaseDelay is the base delay for exponential backoff
	ReconnectBaseDelay time.Duration

	// ReconnectMaxDelay is the maximum delay cap for exponential backoff
	ReconnectMaxDelay time.Duration

	// ReconnectMaxAttempts is the maximum number of reconnection attempts (0=unlimited)
	ReconnectMaxAttempts int

	// PingInterval is the interval for sending ping frames
	PingInterval time.Duration

	// PongTimeout is the time to wait for a pong response
	PongTimeout time.Duration

	// HandshakeTimeout is the timeout for WebSocket handshake
	HandshakeTimeout time.Duration

	// WriteTimeout is the timeout for write operations
	WriteTimeout time.Duration
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.CloudURL == "" {
		return fmt.Errorf("cloud_url is required")
	}
	if _, err := url.Parse(c.CloudURL); err != nil {
		return fmt.Errorf("invalid cloud_url: %w", err)
	}
	if c.TenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	if c.ReconnectBaseDelay <= 0 {
		c.ReconnectBaseDelay = DefaultReconnectBaseDelay
	}
	if c.ReconnectMaxDelay <= 0 {
		c.ReconnectMaxDelay = DefaultReconnectMaxDelay
	}
	if c.PingInterval <= 0 {
		c.PingInterval = DefaultPingInterval
	}
	if c.PongTimeout <= 0 {
		c.PongTimeout = DefaultPongTimeout
	}
	if c.HandshakeTimeout <= 0 {
		c.HandshakeTimeout = DefaultHandshakeTimeout
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = DefaultWriteTimeout
	}
	return nil
}

// CloudEvent represents a JSON event sent to the cloud platform
type CloudEvent struct {
	// ID is a unique identifier for this event
	ID string `json:"id"`
	// TenantID is the tenant that originated this event
	TenantID string `json:"tenant_id"`
	// EventType is the type of event (e.g., "check_in", "check_out")
	EventType string `json:"event_type"`
	// Room is the room number
	Room string `json:"room"`
	// Extension is the PBX extension associated with the room
	Extension string `json:"extension,omitempty"`
	// GuestName is the guest name (if applicable)
	GuestName string `json:"guest_name,omitempty"`
	// Status indicates the event status (e.g., true=on, false=off)
	Status bool `json:"status,omitempty"`
	// Timestamp is when the event occurred
	Timestamp time.Time `json:"timestamp"`
	// Metadata contains additional protocol-specific fields
	Metadata map[string]string `json:"metadata,omitempty"`
}

// CloudMessage is a wrapper for messages sent over the WebSocket
type CloudMessage struct {
	// Type identifies the message type ("event", "ping", "pong", "ack")
	Type string `json:"type"`
	// Payload contains the message data
	Payload interface{} `json:"payload,omitempty"`
	// Timestamp is when the message was created
	Timestamp time.Time `json:"timestamp"`
}

// Bridge maintains a persistent WebSocket connection to the cloud platform
// and forwards PMS events with tenant routing.
type Bridge struct {
	cfg        Config
	tenantID   string
	events     <-chan pms.Event
	conn       *websocket.Conn
	mu         sync.RWMutex
	closed     bool
	done       chan struct{}
	reconnect  chan struct{}
	wg         sync.WaitGroup

	// Reconnection state
	reconnectAttempts  int
	nextReconnectTime time.Time

	// Metrics
	connected       bool
	lastConnectedAt time.Time
}

// NewBridge creates a new WebSocket bridge for forwarding events to the cloud.
func NewBridge(cfg Config, tenantID string, events <-chan pms.Event) (*Bridge, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid bridge config: %w", err)
	}

	return &Bridge{
		cfg:        cfg,
		tenantID:   tenantID,
		events:     events,
		done:       make(chan struct{}),
		reconnect:  make(chan struct{}, 1),
	}, nil
}

// Start begins the WebSocket connection and event forwarding loop.
// It blocks until Stop is called or a fatal error occurs.
func (b *Bridge) Start(ctx context.Context) error {
	log.Info().
		Str("tenant", b.tenantID).
		Str("cloud_url", b.cfg.CloudURL).
		Msg("Starting WebSocket bridge")

	// Update metrics
	metrics.WebSocketConnectionStatus.WithLabelValues(b.tenantID).Set(0)

	for {
		select {
		case <-ctx.Done():
			log.Info().Str("tenant", b.tenantID).Msg("WebSocket bridge stopped: context cancelled")
			return ctx.Err()
		case <-b.done:
			return nil
		case <-b.reconnect:
			if err := b.connect(ctx); err != nil {
				log.Error().
					Err(err).
					Str("tenant", b.tenantID).
					Msg("WebSocket reconnection failed")
				b.scheduleReconnect()
				continue
			}
			// Reset reconnect state on successful connection
			b.reconnectAttempts = 0
		}
	}
}

// connect establishes the WebSocket connection to the cloud platform
func (b *Bridge) connect(ctx context.Context) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return fmt.Errorf("bridge is closed")
	}
	b.mu.Unlock()

	// Prepare headers
	header := http.Header{}
	header.Set("X-Tenant-ID", b.tenantID)
	if b.cfg.AuthToken != "" {
		header.Set("Authorization", "Bearer "+b.cfg.AuthToken)
	}

	// Connect to WebSocket
	dialer := websocket.Dialer{
		HandshakeTimeout: b.cfg.HandshakeTimeout,
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
	}

	conn, _, err := dialer.DialContext(ctx, b.cfg.CloudURL, header)
	if err != nil {
		return fmt.Errorf("WebSocket dial: %w", err)
	}

	b.mu.Lock()
	b.conn = conn
	b.connected = true
	b.lastConnectedAt = time.Now()
	b.mu.Unlock()

	// Update metrics
	metrics.WebSocketConnectionStatus.WithLabelValues(b.tenantID).Set(1)
	metrics.WebSocketLastConnected.WithLabelValues(b.tenantID).Set(float64(time.Now().Unix()))

	log.Info().
		Str("tenant", b.tenantID).
		Msg("WebSocket connected to cloud platform")

	// Start ping/pong handler and reader
	b.wg.Add(2)
	go b.pingPongHandler(ctx)
	go b.readLoop(ctx)

	return nil
}

// pingPongHandler periodically sends ping frames and handles pong responses
func (b *Bridge) pingPongHandler(ctx context.Context) {
	defer b.wg.Done()

	ticker := time.NewTicker(b.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case <-ticker.C:
			b.mu.RLock()
			conn := b.conn
			connected := b.connected
			b.mu.RUnlock()

			if !connected || conn == nil {
				return
			}

			conn.SetWriteDeadline(time.Now().Add(b.cfg.WriteTimeout))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Warn().
					Err(err).
					Str("tenant", b.tenantID).
					Msg("Failed to send ping, connection may be dead")
				b.handleDisconnect()
				return
			}
		}
	}
}

// readLoop handles incoming messages from the cloud platform
func (b *Bridge) readLoop(ctx context.Context) {
	defer b.wg.Done()

	for {
		b.mu.RLock()
		conn := b.conn
		connected := b.connected
		b.mu.RUnlock()

		if !connected || conn == nil {
			return
		}

		conn.SetReadDeadline(time.Now().Add(b.cfg.PingInterval + b.cfg.PongTimeout))
		msgType, reader, err := conn.NextReader()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Warn().
					Err(err).
					Str("tenant", b.tenantID).
					Msg("WebSocket read error")
			}
			b.handleDisconnect()
			return
		}

		switch msgType {
		case websocket.PongMessage:
			// Pong received, connection is alive
			log.Debug().Str("tenant", b.tenantID).Msg("Pong received")
		case websocket.TextMessage, websocket.BinaryMessage:
			// Handle application messages if needed
			if err := b.handleMessage(reader); err != nil {
				log.Warn().
					Err(err).
					Str("tenant", b.tenantID).
					Msg("Failed to handle message")
			}
		}
	}
}

// handleMessage processes an incoming WebSocket message
func (b *Bridge) handleMessage(reader io.Reader) error {
	// For now, we don't expect incoming messages from the cloud
	// This could be extended to handle commands, acks, etc.
	return nil
}

// handleDisconnect handles connection loss and triggers reconnection
func (b *Bridge) handleDisconnect() {
	b.mu.Lock()
	if !b.connected {
		b.mu.Unlock()
		return
	}
	b.connected = false
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
	}
	b.mu.Unlock()

	// Update metrics
	metrics.WebSocketConnectionStatus.WithLabelValues(b.tenantID).Set(0)

	log.Warn().
		Str("tenant", b.tenantID).
		Msg("WebSocket disconnected from cloud platform")

	// Schedule reconnection
	b.scheduleReconnect()

	// Trigger reconnect if not already pending
	select {
	case b.reconnect <- struct{}{}:
	default:
	}
}

// scheduleReconnect calculates the next reconnection time using exponential backoff
func (b *Bridge) scheduleReconnect() {
	b.reconnectAttempts++

	// Check if we've exceeded max attempts
	if b.cfg.ReconnectMaxAttempts > 0 && b.reconnectAttempts > b.cfg.ReconnectMaxAttempts {
		log.Error().
			Str("tenant", b.tenantID).
			Int("attempts", b.reconnectAttempts).
			Msg("Max reconnection attempts reached, giving up")
		return
	}

	// Calculate exponential backoff with jitter
	delay := b.calculateBackoff()

	b.nextReconnectTime = time.Now().Add(delay)
	metrics.WebSocketReconnectDelay.WithLabelValues(b.tenantID).Set(delay.Seconds())

	log.Info().
		Str("tenant", b.tenantID).
		Int("attempt", b.reconnectAttempts).
		Dur("delay", delay).
		Time("next_reconnect", b.nextReconnectTime).
		Msg("Scheduling WebSocket reconnection")

	// Notify for reconnection
	select {
	case b.reconnect <- struct{}{}:
	default:
	}
}

// calculateBackoff computes the backoff delay with exponential increase and jitter
func (b *Bridge) calculateBackoff() time.Duration {
	// Exponential backoff: base * 2^attempt
	multiplier := 1 << uint(b.reconnectAttempts-1)
	delay := float64(b.cfg.ReconnectBaseDelay) * float64(multiplier)
	if delay > float64(b.cfg.ReconnectMaxDelay) {
		delay = float64(b.cfg.ReconnectMaxDelay)
	}

	// Add jitter (±25%)
	jitter := delay * 0.25 * (rand.Float64()*2 - 1)
	delay += jitter

	return time.Duration(delay)
}

// SendEvent sends a PMS event to the cloud platform
func (b *Bridge) SendEvent(ctx context.Context, event pms.Event) error {
	b.mu.RLock()
	conn := b.conn
	connected := b.connected
	b.mu.RUnlock()

	if !connected || conn == nil {
		return fmt.Errorf("not connected to cloud platform")
	}

	// Convert PMS event to cloud event
	cloudEvent := CloudEvent{
		ID:        uuid.New().String(),
		TenantID:  b.tenantID,
		EventType: event.Type.String(),
		Room:      event.Room,
		GuestName: event.GuestName,
		Status:    event.Status,
		Timestamp: event.Timestamp,
		Metadata:  event.Metadata,
	}

	msg := CloudMessage{
		Type:      "event",
		Payload:   cloudEvent,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal cloud event: %w", err)
	}

	conn.SetWriteDeadline(time.Now().Add(b.cfg.WriteTimeout))
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		b.handleDisconnect()
		return fmt.Errorf("write message: %w", err)
	}

	metrics.WebSocketEventsSent.WithLabelValues(b.tenantID, event.Type.String()).Inc()
	return nil
}

// Stop closes the WebSocket connection and stops the bridge
func (b *Bridge) Stop() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()

	log.Info().Str("tenant", b.tenantID).Msg("Stopping WebSocket bridge")

	// Signal done
	close(b.done)

	// Close connection
	b.mu.Lock()
	if b.conn != nil {
		b.conn.Close()
		b.conn = nil
	}
	b.connected = false
	b.mu.Unlock()

	// Wait for goroutines
	b.wg.Wait()

	// Update metrics
	metrics.WebSocketConnectionStatus.WithLabelValues(b.tenantID).Set(0)

	log.Info().Str("tenant", b.tenantID).Msg("WebSocket bridge stopped")
	return nil
}

// Connected returns true if the bridge is currently connected
func (b *Bridge) Connected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connected
}

// Status returns the current bridge status
func (b *Bridge) Status() BridgeStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return BridgeStatus{
		TenantID:          b.tenantID,
		Connected:         b.connected,
		ReconnectAttempts: b.reconnectAttempts,
		NextReconnectTime: b.nextReconnectTime,
		LastConnectedAt:   b.lastConnectedAt,
	}
}

// BridgeStatus represents the current state of the WebSocket bridge
type BridgeStatus struct {
	TenantID          string     `json:"tenant_id"`
	Connected         bool       `json:"connected"`
	ReconnectAttempts int        `json:"reconnect_attempts"`
	NextReconnectTime time.Time  `json:"next_reconnect_time,omitempty"`
	LastConnectedAt   time.Time  `json:"last_connected_at,omitempty"`
}
