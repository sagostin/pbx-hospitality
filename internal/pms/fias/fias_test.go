package fias

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagostin/pbx-hospitality/internal/pms"
)

func TestParseRecord(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      pms.EventType
		room      string
		guestName string
		status    bool
		wantErr   bool
	}{
		{
			name:      "guest check-in",
			input:     "GI|RN1015|GNSmith, John|DA260102|TI1430|",
			want:      pms.EventCheckIn,
			room:      "1015",
			guestName: "Smith, John",
			status:    true,
		},
		{
			name:   "guest check-out",
			input:  "GO|RN1015|DA260102|TI1100|",
			want:   pms.EventCheckOut,
			room:   "1015",
			status: false,
		},
		{
			name:   "message waiting ON",
			input:  "MW|RN1015|FL1|",
			want:   pms.EventMessageWaiting,
			room:   "1015",
			status: true,
		},
		{
			name:   "message waiting OFF",
			input:  "MW|RN1015|FL0|",
			want:   pms.EventMessageWaiting,
			room:   "1015",
			status: false,
		},
		{
			name:   "room status occupied",
			input:  "RS|RN2020|FL1|",
			want:   pms.EventRoomStatus,
			room:   "2020",
			status: true,
		},
		{
			name:   "wake-up call",
			input:  "WK|RN1015|TI0700|",
			want:   pms.EventWakeUp,
			room:   "1015",
			status: true,
		},
		{
			name:    "invalid format",
			input:   "XX",
			wantErr: true,
		},
		{
			name:    "unknown record type",
			input:   "ZZ|RN1015|",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt, err := ParseRecord(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if evt.Type != tt.want {
				t.Errorf("event type = %v, want %v", evt.Type, tt.want)
			}

			if evt.Room != tt.room {
				t.Errorf("room = %q, want %q", evt.Room, tt.room)
			}

			if tt.guestName != "" && evt.GuestName != tt.guestName {
				t.Errorf("guest name = %q, want %q", evt.GuestName, tt.guestName)
			}

			if evt.Status != tt.status {
				t.Errorf("status = %v, want %v", evt.Status, tt.status)
			}
		})
	}
}

func TestParseLinkRecords(t *testing.T) {
	// Link records should return errLinkRecord
	linkRecords := []string{
		"LR|DA|TI|RN|GN|FL|RI|",
		"LS|",
		"LA|",
		"LE|",
	}

	for _, line := range linkRecords {
		t.Run(line[:2], func(t *testing.T) {
			_, err := ParseRecord(line)
			if err != ErrLinkRecord {
				t.Errorf("expected ErrLinkRecord, got %v", err)
			}
		})
	}
}

func TestFieldExtraction(t *testing.T) {
	line := "GI|RN1015|GNDoe, Jane|DA260115|TI0900|RI12345|G#001|"
	evt, err := ParseRecord(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check metadata contains all fields
	expected := map[string]string{
		"RN": "1015",
		"GN": "Doe, Jane",
		"DA": "260115",
		"TI": "0900",
		"RI": "12345",
		"G#": "001",
	}

	for key, want := range expected {
		if got := evt.Metadata[key]; got != want {
			t.Errorf("metadata[%s] = %q, want %q", key, got, want)
		}
	}
}

// TestListenModeDetection tests that listen mode is correctly detected
func TestListenModeDetection(t *testing.T) {
	tests := []struct {
		name      string
		host      string
		port      int
		wantListen bool
	}{
		{
			name:      "connect mode - normal host and port",
			host:      "pms.example.com",
			port:      5000,
			wantListen: false,
		},
		{
			name:      "listen mode - empty host",
			host:      "",
			port:      5000,
			wantListen: true,
		},
		{
			name:      "listen mode - negative port",
			host:      "pms.example.com",
			port:      -5000,
			wantListen: true,
		},
		{
			name:      "listen mode - empty host and negative port",
			host:      "",
			port:      -5000,
			wantListen: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, _ := NewAdapter(tt.host, tt.port)
			if got := adapter.isListenMode(); got != tt.wantListen {
				t.Errorf("isListenMode() = %v, want %v", got, tt.wantListen)
			}
		})
	}
}

// TestGetListenPort tests the listen port calculation
func TestGetListenPort(t *testing.T) {
	tests := []struct {
		name         string
		port         int
		listenPort   int
		wantPort     int
	}{
		{
			name:       "negative port uses absolute value",
			port:       -5000,
			wantPort:   5000,
		},
		{
			name:       "zero port uses default",
			port:       0,
			wantPort:   DefaultFiasPort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, _ := NewAdapter("localhost", tt.port)
			if got := adapter.getListenPort(); got != tt.wantPort {
				t.Errorf("getListenPort() = %v, want %v", got, tt.wantPort)
			}
		})
	}
}

// TestListenConfigOption tests that WithListenConfig option works
func TestListenConfigOption(t *testing.T) {
	cfg := ListenConfig{
		Host:       "0.0.0.0",
		Port:       6000,
		AllowedIPs: []string{"192.168.1.100", "192.168.1.101"},
	}

	adapter, _ := NewAdapter("", -5000, WithListenConfig(cfg))

	if adapter.listenHost != cfg.Host {
		t.Errorf("listenHost = %q, want %q", adapter.listenHost, cfg.Host)
	}
	if adapter.listenPort != cfg.Port {
		t.Errorf("listenPort = %v, want %v", adapter.listenPort, cfg.Port)
	}
	if len(adapter.allowedPMSIPs) != len(cfg.AllowedIPs) {
		t.Errorf("allowedPMSIPs length = %d, want %d", len(adapter.allowedPMSIPs), len(cfg.AllowedIPs))
	}
}

// TestIsIPAllowed tests IP allowlist filtering
func TestIsIPAllowed(t *testing.T) {
	tests := []struct {
		name       string
		allowedIPs []string
		remoteAddr string
		want       bool
	}{
		{
			name:       "empty allowlist allows all",
			allowedIPs: []string{},
			remoteAddr: "192.168.1.100:12345",
			want:       true,
		},
		{
			name:       "matching IP allowed",
			allowedIPs: []string{"192.168.1.100", "192.168.1.101"},
			remoteAddr: "192.168.1.100:12345",
			want:       true,
		},
		{
			name:       "non-matching IP denied",
			allowedIPs: []string{"192.168.1.100"},
			remoteAddr: "192.168.1.200:12345",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, _ := NewAdapter("", -5000, WithListenConfig(ListenConfig{AllowedIPs: tt.allowedIPs}))
			if got := adapter.isIPAllowed(tt.remoteAddr); got != tt.want {
				t.Errorf("isIPAllowed(%q) = %v, want %v", tt.remoteAddr, got, tt.want)
			}
		})
	}
}

// TestListenModeIntegration tests listen mode with a real TCP connection
func TestListenModeIntegration(t *testing.T) {
	// Find an available port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to find available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Create adapter in listen mode
	adapter, err := NewAdapter("", -port)
	if err != nil {
		t.Fatalf("NewAdapter failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start adapter in listen mode
	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.Connect(ctx)
	}()

	// Give the server time to start
	time.Sleep(50 * time.Millisecond)

	// Connect a fake PMS client
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), time.Second)
	if err != nil {
		cancel()
		t.Fatalf("failed to connect to adapter: %v", err)
	}
	defer conn.Close()

	// Send a FIAS Link Record
	lr := "LR|DA|TI|RN|GN|FL|RI|\r\n"
	if _, err := conn.Write([]byte(lr)); err != nil {
		cancel()
		t.Fatalf("failed to send LR: %v", err)
	}

	// Read the response (should be our LR response)
	respBuf := make([]byte, 100)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := conn.Read(respBuf)
	if err != nil {
		cancel()
		t.Fatalf("failed to read response: %v", err)
	}

	expectedResp := "LR|RN|GN|FL|DA|\r\n"
	if string(respBuf[:n]) != expectedResp {
		t.Errorf("response = %q, want %q", string(respBuf[:n]), expectedResp)
	}

	// Send a guest check-in event
	gi := "GI|RN1015|GNSmith, John|DA260102|TI1430|RI12345|\r\n"
	if _, err := conn.Write([]byte(gi)); err != nil {
		cancel()
		t.Fatalf("failed to send GI: %v", err)
	}

	// Wait for the event to be received
	select {
	case evt := <-adapter.Events():
		if evt.Type != pms.EventCheckIn {
			t.Errorf("event type = %v, want %v", evt.Type, pms.EventCheckIn)
		}
		if evt.Room != "1015" {
			t.Errorf("room = %q, want %q", evt.Room, "1015")
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for event")
	}

	// Send link end to close connection gracefully
	le := "LE|\r\n"
	if _, err := conn.Write([]byte(le)); err != nil {
		cancel()
		t.Fatalf("failed to send LE: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	cancel()

	// Wait for adapter to close
	select {
	case err := <-errCh:
		// Error expected due to context cancellation
		if err != nil && err != context.Canceled {
			t.Logf("Connect returned (expected context.Canceled): %v", err)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for adapter to close")
	}
}

// TestConnectModeIntegration tests that existing connect mode still works
func TestConnectModeIntegration(t *testing.T) {
	// Create a local server that echoes FIAS records
	serverLn, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	defer serverLn.Close()

	port := serverLn.Addr().(*net.TCPAddr).Port

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := serverLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Read LR and respond
		buf := make([]byte, 100)
		conn.Read(buf)
		conn.Write([]byte("LR|RN|GN|FL|DA|\r\n"))

		// Read GI event
		conn.Read(buf)
	}()

	// Create adapter in connect mode
	adapter, err := NewAdapter("localhost", port)
	if err != nil {
		t.Fatalf("NewAdapter failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = adapter.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer adapter.Close()

	// Verify connected
	if !adapter.Connected() {
		t.Error("Expected Connected() to return true")
	}

	wg.Wait()
}

// TestBackwardCompatibility ensures existing deployments work
func TestBackwardCompatibility(t *testing.T) {
	// Create adapter with normal host/port (existing deployment style)
	adapter, err := NewAdapter("pms.example.com", 5000)
	if err != nil {
		t.Fatalf("NewAdapter failed: %v", err)
	}

	if adapter.Protocol() != "fias" {
		t.Errorf("Protocol() = %q, want %q", adapter.Protocol(), "fias")
	}

	if adapter.isListenMode() {
		t.Error("Expected connect mode for normal host/port")
	}
}
