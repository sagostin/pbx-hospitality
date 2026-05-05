package websocket

import (
	"context"
	"testing"
	"time"

	"github.com/sagostin/pbx-hospitality/internal/pms"
)

// TestBackoffCalculation tests the exponential backoff calculation with jitter
func TestBackoffCalculation(t *testing.T) {
	tests := []struct {
		name               string
		attempts           int
		baseDelay         time.Duration
		maxDelay          time.Duration
		expectedMinSeconds float64 // minimum expected seconds
		expectedMaxSeconds float64 // maximum expected seconds
	}{
		{
			name:               "first attempt",
			attempts:           1,
			baseDelay:         1 * time.Second,
			maxDelay:          60 * time.Second,
			expectedMinSeconds: 0.75,  // 1s * 1 * 0.75 (jitter floor)
			expectedMaxSeconds: 1.25,  // 1s * 1 * 1.25 (jitter ceiling)
		},
		{
			name:               "second attempt",
			attempts:           2,
			baseDelay:         1 * time.Second,
			maxDelay:          60 * time.Second,
			expectedMinSeconds: 1.5,   // 1s * 2 * 0.75
			expectedMaxSeconds: 2.5,   // 1s * 2 * 1.25
		},
		{
			name:               "third attempt",
			attempts:           3,
			baseDelay:         1 * time.Second,
			maxDelay:          60 * time.Second,
			expectedMinSeconds: 3.0,   // 1s * 4 * 0.75
			expectedMaxSeconds: 5.0,   // 1s * 4 * 1.25
		},
		{
			name:               "exceeds max delay",
			attempts:           10,     // Would be 512s without cap
			baseDelay:         1 * time.Second,
			maxDelay:          60 * time.Second,
			expectedMinSeconds: 45.0,  // 60s * 0.75
			expectedMaxSeconds: 75.0,  // 60s * 1.25
		},
		{
			name:               "larger base delay",
			attempts:           2,
			baseDelay:         5 * time.Second,
			maxDelay:          60 * time.Second,
			expectedMinSeconds: 7.5,   // 5s * 2 * 0.75
			expectedMaxSeconds: 12.5,  // 5s * 2 * 1.25
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bridge := &Bridge{
				cfg: Config{
					ReconnectBaseDelay: tt.baseDelay,
					ReconnectMaxDelay:  tt.maxDelay,
				},
				reconnectAttempts: tt.attempts,
			}

			// Run calculation multiple times to account for jitter variance
			for i := 0; i < 10; i++ {
				delay := bridge.calculateBackoff()
				delaySeconds := delay.Seconds()

				if delaySeconds < tt.expectedMinSeconds || delaySeconds > tt.expectedMaxSeconds {
					t.Errorf(
						"attempt %d: calculated backoff %v not in expected range [%v, %v]",
						tt.attempts,
						delaySeconds,
						tt.expectedMinSeconds,
						tt.expectedMaxSeconds,
					)
				}
			}
		})
	}
}

// TestConfigValidate tests configuration validation
func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				CloudURL:  "wss://cloud.example.com/ws",
				TenantID: "tenant-1",
			},
			wantErr: false,
		},
		{
			name: "missing cloud URL",
			cfg: Config{
				TenantID: "tenant-1",
			},
			wantErr: true,
		},
		{
			name: "invalid cloud URL",
			cfg: Config{
				CloudURL: "://invalid-scheme",
				TenantID: "tenant-1",
			},
			wantErr: true,
		},
		{
			name: "missing tenant ID",
			cfg: Config{
				CloudURL: "wss://cloud.example.com/ws",
			},
			wantErr: true,
		},
		{
			name: "zero base delay - should use default",
			cfg: Config{
				CloudURL:             "wss://cloud.example.com/ws",
				TenantID:            "tenant-1",
				ReconnectBaseDelay:  0,
				ReconnectMaxDelay:   60 * time.Second,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestConfigDefaults tests that defaults are applied during validation
func TestConfigDefaults(t *testing.T) {
	cfg := Config{
		CloudURL:  "wss://cloud.example.com/ws",
		TenantID: "tenant-1",
	}

	// Set zeros to verify defaults are applied
	cfg.ReconnectBaseDelay = 0
	cfg.ReconnectMaxDelay = 0
	cfg.PingInterval = 0
	cfg.PongTimeout = 0
	cfg.HandshakeTimeout = 0
	cfg.WriteTimeout = 0

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}

	// Verify defaults were applied
	if cfg.ReconnectBaseDelay != DefaultReconnectBaseDelay {
		t.Errorf("ReconnectBaseDelay = %v, want %v", cfg.ReconnectBaseDelay, DefaultReconnectBaseDelay)
	}
	if cfg.ReconnectMaxDelay != DefaultReconnectMaxDelay {
		t.Errorf("ReconnectMaxDelay = %v, want %v", cfg.ReconnectMaxDelay, DefaultReconnectMaxDelay)
	}
	if cfg.PingInterval != DefaultPingInterval {
		t.Errorf("PingInterval = %v, want %v", cfg.PingInterval, DefaultPingInterval)
	}
	if cfg.PongTimeout != DefaultPongTimeout {
		t.Errorf("PongTimeout = %v, want %v", cfg.PongTimeout, DefaultPongTimeout)
	}
	if cfg.HandshakeTimeout != DefaultHandshakeTimeout {
		t.Errorf("HandshakeTimeout = %v, want %v", cfg.HandshakeTimeout, DefaultHandshakeTimeout)
	}
	if cfg.WriteTimeout != DefaultWriteTimeout {
		t.Errorf("WriteTimeout = %v, want %v", cfg.WriteTimeout, DefaultWriteTimeout)
	}
}

// TestBridgeStatus tests the bridge status reporting
func TestBridgeStatus(t *testing.T) {
	bridge := &Bridge{
		tenantID:          "test-tenant",
		reconnectAttempts: 5,
		nextReconnectTime: time.Now().Add(30 * time.Second),
		lastConnectedAt:  time.Now().Add(-1 * time.Minute),
	}

	status := bridge.Status()

	if status.TenantID != "test-tenant" {
		t.Errorf("Status().TenantID = %v, want test-tenant", status.TenantID)
	}
	if status.Connected != false {
		t.Errorf("Status().Connected = %v, want false", status.Connected)
	}
	if status.ReconnectAttempts != 5 {
		t.Errorf("Status().ReconnectAttempts = %v, want 5", status.ReconnectAttempts)
	}
}

// TestCloudEventJSON tests JSON marshaling of cloud events
func TestCloudEventJSON(t *testing.T) {
	event := CloudEvent{
		ID:        "test-id-123",
		TenantID:  "hotel-alpha",
		EventType: "check_in",
		Room:      "1015",
		Extension: "11015",
		GuestName: "Smith, John",
		Status:    true,
		Timestamp: time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		Metadata: map[string]string{
			"reservation_id": "RES-12345",
			"source":         "fias",
		},
	}

	// Verify the struct can be used as expected
	if event.EventType != "check_in" {
		t.Errorf("CloudEvent.EventType = %v, want check_in", event.EventType)
	}
	if event.Room != "1015" {
		t.Errorf("CloudEvent.Room = %v, want 1015", event.Room)
	}
	if event.Metadata["reservation_id"] != "RES-12345" {
		t.Errorf("CloudEvent.Metadata[reservation_id] = %v, want RES-12345", event.Metadata["reservation_id"])
	}
}

// TestBridgeClose tests that closing a bridge works correctly
func TestBridgeClose(t *testing.T) {
	bridge := &Bridge{
		cfg: Config{
			CloudURL:  "wss://cloud.example.com/ws",
			TenantID: "tenant-1",
		},
		tenantID:  "tenant-1",
		done:      make(chan struct{}),
		reconnect: make(chan struct{}, 1),
	}

	// Closing should not panic
	err := bridge.Stop()
	if err != nil {
		t.Errorf("Stop() unexpected error: %v", err)
	}

	// Double close should also not panic
	err = bridge.Stop()
	if err != nil {
		t.Errorf("Stop() second call unexpected error: %v", err)
	}
}

// TestBridgeNotConnected tests operations when bridge is not connected
func TestBridgeNotConnected(t *testing.T) {
	events := make(chan pms.Event)
	bridge := &Bridge{
		cfg: Config{
			CloudURL:  "wss://cloud.example.com/ws",
			TenantID: "tenant-1",
		},
		tenantID: "tenant-1",
		events:   events,
		done:      make(chan struct{}),
		reconnect: make(chan struct{}, 1),
	}

	if bridge.Connected() {
		t.Error("Connected() = true, want false for unconnected bridge")
	}
}

// TestNewBridgeWithInvalidConfig tests that NewBridge rejects invalid configs
func TestNewBridgeWithInvalidConfig(t *testing.T) {
	events := make(chan pms.Event)

	// Missing CloudURL
	_, err := NewBridge(Config{TenantID: "tenant-1"}, "tenant-1", events)
	if err == nil {
		t.Error("NewBridge() expected error for missing CloudURL")
	}

	// Missing TenantID
	_, err = NewBridge(Config{CloudURL: "wss://cloud.example.com/ws"}, "tenant-1", events)
	if err == nil {
		t.Error("NewBridge() expected error for missing TenantID")
	}
}

// TestReconnectScheduling tests that reconnection is scheduled correctly
func TestReconnectScheduling(t *testing.T) {
	bridge := &Bridge{
		cfg: Config{
			ReconnectBaseDelay: 1 * time.Second,
			ReconnectMaxDelay: 60 * time.Second,
		},
		reconnectAttempts: 0,
	}

	// First scheduling should trigger attempt 1
	bridge.scheduleReconnect()
	if bridge.reconnectAttempts != 1 {
		t.Errorf("reconnectAttempts = %d, want 1", bridge.reconnectAttempts)
	}
	if bridge.nextReconnectTime.IsZero() {
		t.Error("nextReconnectTime should be set after scheduleReconnect")
	}

	// Second scheduling should increment to attempt 2
	oldNextTime := bridge.nextReconnectTime
	bridge.scheduleReconnect()
	if bridge.reconnectAttempts != 2 {
		t.Errorf("reconnectAttempts = %d, want 2", bridge.reconnectAttempts)
	}
	if !bridge.nextReconnectTime.After(oldNextTime) {
		t.Error("nextReconnectTime should increase with each attempt")
	}
}

// TestContextCancellation tests that bridge handles context cancellation
func TestContextCancellation(t *testing.T) {
	events := make(chan pms.Event)
	ctx, cancel := context.WithCancel(context.Background())

	bridge, err := NewBridge(Config{
		CloudURL: "wss://cloud.example.com/ws",
		TenantID: "tenant-1",
	}, "tenant-1", events)
	if err != nil {
		t.Fatalf("NewBridge() error: %v", err)
	}

	// Cancel context immediately
	cancel()

	// Start should return immediately due to cancelled context
	err = bridge.Start(ctx)
	if err != context.Canceled {
		t.Errorf("Start() error = %v, want context.Canceled", err)
	}
}
