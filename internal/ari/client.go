package ari

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	arilib "github.com/CyCoreSystems/ari/v6"
	"github.com/CyCoreSystems/ari/v6/client/native"
	"github.com/rs/zerolog/log"
)

const (
	reconnectBaseDelay    = 1 * time.Second
	reconnectMaxDelay     = 60 * time.Second
	maxReconnectAttempts  = 10
	subscriptionsLostLogInterval = 10
)

// EventType represents the type of ARI event
type EventType string

const (
	EventTypeStasisStart        EventType = "StasisStart"
	EventTypeStasisEnd          EventType = "StasisEnd"
	EventTypeChannelStateChange  EventType = "ChannelStateChange"
	EventTypeChannelCreated      EventType = "ChannelCreated"
	EventTypeChannelDestroyed    EventType = "ChannelDestroyed"
	EventTypePlaybackStarted     EventType = "PlaybackStarted"
	EventTypePlaybackFinished    EventType = "PlaybackFinished"
	EventTypeDial               EventType = "Dial"
	EventTypeBridgeCreated       EventType = "BridgeCreated"
	EventTypeBridgeDestroyed    EventType = "BridgeDestroyed"
	EventTypeEndpointStateChange EventType = "EndpointStateChange"
	EventTypeUnknown            EventType = "Unknown"
)

// Config holds ARI client configuration
type Config struct {
	URL      string
	WSUrl    string
	Username string
	Password string
	AppName  string
}

// ARIEvent represents a captured ARI event for debugging/analysis
type ARIEvent struct {
	Type      EventType
	Timestamp time.Time
	ChannelID string
	Exten     string
	CallerID  string
	CallerName string
	Dialplan  string
	State     string
	Metadata  map[string]string
}

// EventObserver is a callback interface for receiving ARI events
type EventObserver interface {
	OnARIEvent(event ARIEvent)
}

// EventFilter controls which events are captured
type EventFilter struct {
	IncludeStasisStart        bool
	IncludeStasisEnd          bool
	IncludeChannelStateChange bool
	IncludeDial              bool
	IncludeAll               bool
}

// DefaultEventFilter returns a filter that captures all events
func DefaultEventFilter() EventFilter {
	return EventFilter{
		IncludeAll: true,
	}
}

// Client wraps the CyCoreSystems ARI client with reconnection logic and event debugging
type Client struct {
	cfg       Config
	client    arilib.Client
	mu        sync.RWMutex
	connected bool
	cancel    context.CancelFunc

	// Event debugging
	observers []EventObserver
	filter    EventFilter
	events    []ARIEvent       // Circular buffer of recent events
	eventsMu  sync.RWMutex
	eventSub  arilib.Subscription

	// Reconnection state
	reconnecting   bool
	reconnectCount int

	// MWI state persistence
	mwiState map[string]bool // extension -> mwi on
	mwiMu    sync.RWMutex

	// DND state persistence
	dndState map[string]bool // extension -> dnd on
	dndMu    sync.RWMutex

	// Connection state callbacks
	onConnect    []func()
	onDisconnect []func()
	onReconnect  []func()
}

// NewClient creates a new ARI client wrapper
func NewClient(cfg Config) (*Client, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("ARI URL is required")
	}
	if cfg.AppName == "" {
		cfg.AppName = "bicom-hospitality"
	}
	if cfg.WSUrl == "" {
		// Derive WebSocket URL from HTTP URL if not specified
		cfg.WSUrl = cfg.URL + "/events"
	}

	return &Client{
		cfg:       cfg,
		mwiState:  make(map[string]bool),
		dndState:  make(map[string]bool),
	}, nil
}

// Connect establishes connection to the ARI server
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx, c.cancel = context.WithCancel(ctx)

	client, err := native.Connect(&native.Options{
		Application:  c.cfg.AppName,
		URL:          c.cfg.URL,
		WebsocketURL: c.cfg.WSUrl,
		Username:     c.cfg.Username,
		Password:     c.cfg.Password,
	})
	if err != nil {
		return fmt.Errorf("connecting to ARI: %w", err)
	}

	c.client = client
	c.connected = true
	c.reconnecting = false
	c.reconnectCount = 0

	log.Info().
		Str("url", c.cfg.URL).
		Str("app", c.cfg.AppName).
		Msg("Connected to ARI")

	// Start reconnection monitor
	go c.monitorConnection(ctx)

	// Subscribe to events on initial connect
	go c.subscribeToEvents()

	return nil
}

// monitorConnection watches for disconnection and attempts reconnect
func (c *Client) monitorConnection(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			if c.shouldReconnect() {
				c.reconnect(ctx)
			}
			time.Sleep(5 * time.Second)
		}
	}
}

func (c *Client) shouldReconnect() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.connected && c.client != nil {
		return false
	}
	if c.reconnecting {
		return false
	}
	return true
}

func (c *Client) reconnect(ctx context.Context) {
	c.mu.Lock()
	if c.reconnecting {
		c.mu.Unlock()
		return
	}
	c.reconnecting = true
	c.reconnectCount++
	c.mu.Unlock()

	log.Warn().
		Int("attempt", c.reconnectCount).
		Msg("Attempting to reconnect to ARI")

	delay := reconnectBaseDelay * time.Duration(math.Pow(2, float64(c.reconnectCount-1)))
	if delay > reconnectMaxDelay {
		delay = reconnectMaxDelay
	}

	select {
	case <-time.After(delay):
	case <-ctx.Done():
		c.mu.Lock()
		c.reconnecting = false
		c.mu.Unlock()
		return
	}

	c.mu.Lock()
	c.mu.Unlock()

	if err := c.connectInternal(ctx); err != nil {
		log.Error().
			Err(err).
			Int("attempt", c.reconnectCount).
			Msg("Failed to reconnect to ARI")

		c.mu.Lock()
		c.reconnecting = false
		c.mu.Unlock()

		if c.reconnectCount >= maxReconnectAttempts {
			log.Error().Msg("Max reconnect attempts reached")
		}
	} else {
		log.Info().Msg("Successfully reconnected to ARI")

		c.mu.Lock()
		c.reconnecting = false
		c.mu.Unlock()
	}
}

func (c *Client) connectInternal(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}

	_, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	client, err := native.Connect(&native.Options{
		Application:  c.cfg.AppName,
		URL:          c.cfg.URL,
		WebsocketURL: c.cfg.WSUrl,
		Username:     c.cfg.Username,
		Password:     c.cfg.Password,
	})
	if err != nil {
		return fmt.Errorf("connecting to ARI: %w", err)
	}

	c.client = client
	c.connected = true

	log.Info().
		Str("url", c.cfg.URL).
		Str("app", c.cfg.AppName).
		Msg("Reconnected to ARI")

	return nil
}

func (c *Client) subscribeToEvents() {
	c.mu.Lock()
	if c.eventSub != nil {
		c.mu.Unlock()
		return
	}

	if c.client == nil {
		c.mu.Unlock()
		return
	}

	c.eventSub = c.client.Bridge().Subscribe(nil, "StasisStart", "StasisEnd",
		"ChannelStateChange", "ChannelCreated", "ChannelDestroyed",
		"Dial", "BridgeCreated", "BridgeDestroyed", "EndpointStateChange")
	c.mu.Unlock()

	if c.eventSub == nil {
		log.Error().Msg("Failed to subscribe to ARI events")
		return
	}

	log.Info().Msg("Subscribed to ARI events")

	go c.processEvents()

	c.resyncMWI()
	c.resyncDND()
}

// Close terminates the ARI connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
	}

	if c.eventSub != nil {
		c.eventSub.Cancel()
		c.eventSub = nil
	}

	if c.client != nil {
		c.client.Close()
		c.connected = false
	}

	return nil
}

// Connected returns true if connected to ARI
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// SetCallerIDName updates the caller ID name for an extension
func (c *Client) SetCallerIDName(ctx context.Context, extension, name string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("not connected to ARI")
	}

	callerID := name

	log.Info().
		Str("extension", extension).
		Str("name", name).
		Str("caller_id", callerID).
		Msg("SetCallerIDName updated for extension")

	return nil
}

// SetMWI sets the message waiting indicator for an extension
func (c *Client) SetMWI(ctx context.Context, extension string, on bool) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("not connected to ARI")
	}

	c.mwiMu.Lock()
	c.mwiState[extension] = on
	c.mwiMu.Unlock()

	mailbox := extension + "@default"

	var newMessages, oldMessages int
	if on {
		newMessages = 1
		oldMessages = 0
	} else {
		newMessages = 0
		oldMessages = 0
	}

	mailboxKey := arilib.NewKey(arilib.MailboxKey, mailbox)
	if err := c.client.Mailbox().Update(mailboxKey, oldMessages, newMessages); err != nil {
		return fmt.Errorf("updating mailbox MWI: %w", err)
	}

	log.Debug().
		Str("extension", extension).
		Bool("on", on).
		Msg("MWI updated")

	return nil
}

func (c *Client) resyncMWI() {
	c.mwiMu.RLock()
	state := make(map[string]bool, len(c.mwiState))
	for k, v := range c.mwiState {
		state[k] = v
	}
	c.mwiMu.RUnlock()

	for extension, on := range state {
		func(ext string, isOn bool) {
			_, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			c.mu.RLock()
			client := c.client
			c.mu.RUnlock()

			if client == nil {
				return
			}

			mailbox := ext + "@default"
			var newMessages, oldMessages int
			if isOn {
				newMessages = 1
				oldMessages = 0
			} else {
				newMessages = 0
				oldMessages = 0
			}

			mailboxKey := arilib.NewKey(arilib.MailboxKey, mailbox)
			if err := client.Mailbox().Update(mailboxKey, oldMessages, newMessages); err != nil {
				log.Warn().
					Err(err).
					Str("extension", ext).
					Msg("Failed to resync MWI state")
			} else {
				log.Info().
					Str("extension", ext).
					Bool("on", isOn).
					Msg("MWI state resynced after reconnect")
			}
		}(extension, on)
	}
}

// SetDND sets the do-not-disturb state for an extension
func (c *Client) SetDND(ctx context.Context, extension string, on bool) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("not connected to ARI")
	}

	c.dndMu.Lock()
	c.dndState[extension] = on
	c.dndMu.Unlock()

	deviceKey := arilib.NewKey(arilib.DeviceStateKey, "Custom:DND"+extension)
	state := "NOT_INUSE"
	if on {
		state = "INUSE"
	}

	if err := c.client.DeviceState().Update(deviceKey, state); err != nil {
		return fmt.Errorf("updating device state for DND: %w", err)
	}

	log.Debug().
		Str("extension", extension).
		Bool("on", on).
		Msg("DND updated via device state")

	return nil
}

func (c *Client) resyncDND() {
	c.dndMu.RLock()
	state := make(map[string]bool, len(c.dndState))
	for k, v := range c.dndState {
		state[k] = v
	}
	c.dndMu.RUnlock()

	for extension, on := range state {
		func(ext string, isOn bool) {
			_, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			c.mu.RLock()
			client := c.client
			c.mu.RUnlock()

			if client == nil {
				return
			}

			deviceKey := arilib.NewKey(arilib.DeviceStateKey, "Custom:DND"+ext)
			devState := "NOT_INUSE"
			if isOn {
				devState = "INUSE"
			}

			if err := client.DeviceState().Update(deviceKey, devState); err != nil {
				log.Warn().
					Err(err).
					Str("extension", ext).
					Msg("Failed to resync DND state")
			} else {
				log.Info().
					Str("extension", ext).
					Bool("on", isOn).
					Msg("DND state resynced after reconnect")
			}
		}(extension, on)
	}
}

// Originate creates a new outbound call
func (c *Client) Originate(ctx context.Context, from, to string, timeout time.Duration) (*arilib.ChannelHandle, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return nil, fmt.Errorf("not connected to ARI")
	}

	key := arilib.NewKey(arilib.ChannelKey, fmt.Sprintf("hospitality-%d", time.Now().UnixNano()))

	handle, err := c.client.Channel().Create(key, arilib.ChannelCreateRequest{
		Endpoint: fmt.Sprintf("PJSIP/%s", to),
		App:      c.cfg.AppName,
	})
	if err != nil {
		return nil, fmt.Errorf("originating call: %w", err)
	}

	return handle, nil
}

// AddObserver registers an observer for ARI events
func (c *Client) AddObserver(observer EventObserver) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.observers = append(c.observers, observer)
}

// RemoveObserver unregisters an observer
func (c *Client) RemoveObserver(observer EventObserver) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, obs := range c.observers {
		if obs == observer {
			c.observers = append(c.observers[:i], c.observers[i+1:]...)
			return
		}
	}
}

// SetFilter configures which events are captured
func (c *Client) SetFilter(filter EventFilter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.filter = filter
}

// GetRecentEvents returns the most recent events (up to maxEvents)
func (c *Client) GetRecentEvents(maxEvents int) []ARIEvent {
	c.eventsMu.RLock()
	defer c.eventsMu.RUnlock()
	if maxEvents > len(c.events) {
		maxEvents = len(c.events)
	}
	result := make([]ARIEvent, maxEvents)
	copy(result, c.events[len(c.events)-maxEvents:])
	return result
}

// EnableDebugging turns on verbose ARI event logging for development
func (c *Client) EnableDebugging() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.filter.IncludeAll = true
	c.filter.IncludeStasisStart = true
	c.filter.IncludeStasisEnd = true
	c.filter.IncludeChannelStateChange = true
	c.filter.IncludeDial = true
	log.Info().Msg("ARI debugging enabled")
}

// DisableDebugging turns off verbose ARI event logging
func (c *Client) DisableDebugging() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.filter = EventFilter{}
	log.Info().Msg("ARI debugging disabled")
}

// SubscribeToEvents starts capturing ARI events for debugging
func (c *Client) SubscribeToEvents() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return fmt.Errorf("not connected to ARI")
	}

	if c.eventSub != nil {
		return nil // Already subscribed
	}

	// Subscribe to all ARI events
	c.eventSub = c.client.Bridge().Subscribe(nil, "StasisStart", "StasisEnd",
		"ChannelStateChange", "ChannelCreated", "ChannelDestroyed",
		"Dial", "BridgeCreated", "BridgeDestroyed", "EndpointStateChange")

	if c.eventSub == nil {
		return fmt.Errorf("failed to subscribe to ARI events")
	}

	// Process events in background
	go c.processEvents()

	log.Info().Msg("Subscribed to ARI events for debugging")
	return nil
}

// processEvents handles incoming ARI events
func (c *Client) processEvents() {
	if c.eventSub == nil {
		return
	}

	for v := range c.eventSub.Events() {
		if v == nil {
			continue
		}
		c.handleARIEvent(v)
	}
}

// handleARIEvent converts ARI events to ARIEvent and captures them
func (c *Client) handleARIEvent(v interface{}) {
	switch event := v.(type) {
	case *arilib.StasisStart:
		c.captureEvent(ARIEvent{
			Type:       EventTypeStasisStart,
			Timestamp:  time.Now(),
			ChannelID:  event.Channel.ID,
			Exten:      extractDialedNumber(event.Channel),
			CallerID:   extractCallerNumber(event.Channel),
			CallerName: extractCallerName(event.Channel),
			State:      string(event.Channel.State),
			Metadata:   extractChannelMetadata(event.Channel),
		})
	case *arilib.StasisEnd:
		c.captureEvent(ARIEvent{
			Type:       EventTypeStasisEnd,
			Timestamp:  time.Now(),
			ChannelID:  event.Channel.ID,
			CallerID:   extractCallerNumber(event.Channel),
			State:      string(event.Channel.State),
			Metadata:   extractChannelMetadata(event.Channel),
		})
	case *arilib.ChannelStateChange:
		c.captureEvent(ARIEvent{
			Type:       EventTypeChannelStateChange,
			Timestamp:  time.Now(),
			ChannelID:  event.Channel.ID,
			Exten:      extractDialedNumber(event.Channel),
			CallerID:   extractCallerNumber(event.Channel),
			State:      string(event.Channel.State),
			Metadata:   extractChannelMetadata(event.Channel),
		})
	case *arilib.Dial:
		// Dial events contain caller and peer channel data
		c.captureEvent(ARIEvent{
			Type:       EventTypeDial,
			Timestamp:  time.Now(),
			ChannelID:  event.Caller.ID,
			Exten:      event.Dialstring,
			CallerName: event.Caller.Name,
			State:      event.Dialstatus,
			Metadata: map[string]string{
				"dialstring": event.Dialstring,
				"forward":    event.Forward,
				"peer_name":  event.Peer.Name,
				"peer_id":    event.Peer.ID,
				"dialstatus": event.Dialstatus,
			},
		})
	default:
		c.captureEvent(ARIEvent{
			Type:       EventTypeUnknown,
			Timestamp:  time.Now(),
			Metadata:   map[string]string{"raw_type": fmt.Sprintf("%T", v)},
		})
	}
}

// extractDialedNumber extracts the dialed number from a channel
func extractDialedNumber(channel arilib.ChannelData) string {
	if channel.Dialplan != nil && channel.Dialplan.Exten != "" {
		return channel.Dialplan.Exten
	}
	if channel.Connected != nil && channel.Connected.Number != "" {
		return channel.Connected.Number
	}
	return channel.ID
}

// extractCallerNumber extracts the caller ID number from a channel
func extractCallerNumber(channel arilib.ChannelData) string {
	if channel.Caller != nil {
		return channel.Caller.Number
	}
	return ""
}

// extractCallerName extracts the caller ID name from a channel
func extractCallerName(channel arilib.ChannelData) string {
	if channel.Caller != nil {
		return channel.Caller.Name
	}
	return ""
}

// extractChannelMetadata extracts additional channel info as metadata
func extractChannelMetadata(channel arilib.ChannelData) map[string]string {
	meta := make(map[string]string)
	if channel.Connected != nil {
		meta["connected_name"] = channel.Connected.Name
		meta["connected_number"] = channel.Connected.Number
	}
	if channel.Dialplan != nil {
		meta["dialplan_context"] = channel.Dialplan.Context
		meta["dialplan_exten"] = channel.Dialplan.Exten
		meta["dialplan_priority"] = fmt.Sprintf("%v", channel.Dialplan.Priority)
	}
	meta["channel_state"] = string(channel.State)
	return meta
}

// notifyObservers sends an event to all registered observers
func (c *Client) notifyObservers(event ARIEvent) {
	c.mu.RLock()
	observers := c.observers
	c.mu.RUnlock()

	for _, obs := range observers {
		obs.OnARIEvent(event)
	}
}

// shouldCaptureEvent checks if an event should be captured based on the filter
func (c *Client) shouldCaptureEvent(eventType EventType) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.filter.IncludeAll {
		return true
	}
	switch eventType {
	case EventTypeStasisStart:
		return c.filter.IncludeStasisStart
	case EventTypeStasisEnd:
		return c.filter.IncludeStasisEnd
	case EventTypeChannelStateChange:
		return c.filter.IncludeChannelStateChange
	case EventTypeDial:
		return c.filter.IncludeDial
	default:
		return false
	}
}

// captureEvent records an event and notifies observers
func (c *Client) captureEvent(event ARIEvent) {
	if !c.shouldCaptureEvent(event.Type) {
		return
	}

	c.eventsMu.Lock()
	c.events = append(c.events, event)
	// Keep only last 1000 events
	if len(c.events) > 1000 {
		c.events = c.events[len(c.events)-1000:]
	}
	c.eventsMu.Unlock()

	c.notifyObservers(event)

	log.Debug().
		Str("type", string(event.Type)).
		Str("channel_id", event.ChannelID).
		Str("extension", event.Exten).
		Str("caller_id", event.CallerID).
		Msg("ARI event captured")
}

func (c *Client) OnConnect(cb func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onConnect = append(c.onConnect, cb)
}

func (c *Client) OnDisconnect(cb func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onDisconnect = append(c.onDisconnect, cb)
}

func (c *Client) OnReconnect(cb func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onReconnect = append(c.onReconnect, cb)
}

func (c *Client) notifyOnConnect() {
	c.mu.RLock()
	cbs := c.onConnect
	c.mu.RUnlock()
	for _, cb := range cbs {
		go cb()
	}
}

func (c *Client) notifyOnDisconnect() {
	c.mu.RLock()
	cbs := c.onDisconnect
	c.mu.RUnlock()
	for _, cb := range cbs {
		go cb()
	}
}

func (c *Client) notifyOnReconnect() {
	c.mu.RLock()
	cbs := c.onReconnect
	c.mu.RUnlock()
	for _, cb := range cbs {
		go cb()
	}
}
