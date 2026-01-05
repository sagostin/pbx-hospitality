package ari

import (
	"context"
	"fmt"
	"sync"
	"time"

	arilib "github.com/CyCoreSystems/ari/v6"
	"github.com/CyCoreSystems/ari/v6/client/native"
	"github.com/rs/zerolog/log"
)

// Config holds ARI client configuration
type Config struct {
	URL      string
	WSUrl    string
	Username string
	Password string
	AppName  string
}

// Client wraps the CyCoreSystems ARI client with reconnection logic
type Client struct {
	cfg       Config
	client    arilib.Client
	mu        sync.RWMutex
	connected bool
	cancel    context.CancelFunc
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

	return &Client{cfg: cfg}, nil
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

	log.Info().
		Str("url", c.cfg.URL).
		Str("app", c.cfg.AppName).
		Msg("Connected to ARI")

	// Start reconnection monitor
	go c.monitorConnection(ctx)

	return nil
}

// monitorConnection watches for disconnection and attempts reconnect
func (c *Client) monitorConnection(ctx context.Context) {
	// TODO: Implement reconnection logic based on ARI client signals
	// For now, just log that we're monitoring
	<-ctx.Done()
}

// Close terminates the ARI connection
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
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

	// The caller ID name is typically set via the dialplan or endpoint configuration
	// For ARI, we would need to use channel variables or AMI for endpoint updates
	// This is a placeholder - actual implementation depends on Bicom's ARI capabilities
	log.Debug().
		Str("extension", extension).
		Str("name", name).
		Msg("SetCallerIDName called (placeholder)")

	// TODO: Implement via appropriate Bicom API
	// Options:
	// 1. AMI action to update the endpoint
	// 2. Update via Bicom's REST API
	// 3. Set channel variable on next call

	return nil
}

// SetMWI sets the message waiting indicator for an extension
func (c *Client) SetMWI(ctx context.Context, extension string, on bool) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("not connected to ARI")
	}

	// MWI is typically controlled via mailbox state
	// The actual implementation depends on Bicom's mailbox configuration
	// This uses the Mailboxes resource from ARI
	mailbox := extension + "@default"

	var newMessages, oldMessages int
	if on {
		newMessages = 1
		oldMessages = 0
	} else {
		newMessages = 0
		oldMessages = 0
	}

	// Update mailbox state via ARI Mailboxes endpoint
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

// SetDND sets the do-not-disturb state for an extension
func (c *Client) SetDND(ctx context.Context, extension string, on bool) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.connected || c.client == nil {
		return fmt.Errorf("not connected to ARI")
	}

	// DND is typically a device state or feature code
	// This is a placeholder - actual implementation depends on Bicom's capabilities
	log.Debug().
		Str("extension", extension).
		Bool("on", on).
		Msg("SetDND called (placeholder)")

	// TODO: Implement via:
	// 1. Device state: DEVICE_STATE(Custom:DND<extension>)
	// 2. Feature code execution
	// 3. Bicom API call

	return nil
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
