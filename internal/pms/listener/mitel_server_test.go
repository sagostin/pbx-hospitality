package listener

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagostin/pbx-hospitality/internal/pms"
)

func TestListenerDefaults(t *testing.T) {
	events := make(chan pms.Event, 100)
	l, err := NewMitelListener(pms.ListenerConfig{ListenHost: "localhost", ListenPort: DefaultPort}, events)
	if err != nil {
		t.Fatalf("NewMitelListener() error = %v", err)
	}
	if l.Host() != "localhost" {
		t.Errorf("Host() = %q, want %q", l.Host(), "localhost")
	}
	if l.Port() != DefaultPort {
		t.Errorf("Port() = %d, want %d", l.Port(), DefaultPort)
	}
}

func TestParseMessage(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    pms.EventType
		room    string
		status  bool
		wantErr bool
	}{
		{
			name:   "check-in room 2129",
			input:  "CHK1  2129",
			want:   pms.EventCheckIn,
			room:   "2129",
			status: true,
		},
		{
			name:   "check-out room 2129",
			input:  "CHK0  2129",
			want:   pms.EventCheckOut,
			room:   "2129",
			status: false,
		},
		{
			name:   "message waiting ON for room 101",
			input:  "MW 1   101",
			want:   pms.EventMessageWaiting,
			room:   "101",
			status: true,
		},
		{
			name:   "message waiting OFF for room 101",
			input:  "MW 0   101",
			want:   pms.EventMessageWaiting,
			room:   "101",
			status: false,
		},
		{
			name:   "DND on for room 500",
			input:  "DND1   500",
			want:   pms.EventDND,
			room:   "500",
			status: true,
		},
		{
			name:   "room status occupied",
			input:  "RM 1  1015",
			want:   pms.EventRoomStatus,
			room:   "1015",
			status: true,
		},
		{
			name:   "name update for room 2129",
			input:  "NAM1  2129",
			want:   pms.EventNameUpdate,
			room:   "2129",
			status: true,
		},
		{
			name:    "message too short",
			input:   "CHK1",
			wantErr: true,
		},
		{
			name:    "unknown function code",
			input:   "XXX1 2129",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt, err := ParseMessage([]byte(tt.input))

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

			if evt.Status != tt.status {
				t.Errorf("status = %v, want %v", evt.Status, tt.status)
			}
		})
	}
}

// buildMitelMessage builds a complete Mitel message with STX/ETX framing
func buildMitelMessage(payload string) []byte {
	msg := make([]byte, 0, len(payload)+2)
	msg = append(msg, STX)
	msg = append(msg, payload...)
	msg = append(msg, ETX)
	return msg
}

// STX, ETX, ENQ, ACK, NAK are control characters used in Mitel protocol
const (
	STX = 0x02
	ETX = 0x03
	ENQ = 0x05
	ACK = 0x06
	NAK = 0x15
)

func TestListenerIntegration(t *testing.T) {
	// Start listener on random port
	ln := newTestMitelListener("localhost", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start listener in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- ln.Listen(ctx)
	}()

	// Give listener time to start
	time.Sleep(100 * time.Millisecond)

	// Get actual port
	ln.mu.RLock()
	addr := ln.listener.Addr().String()
	ln.mu.RUnlock()

	// Connect to listener
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect to listener: %v", err)
	}
	defer conn.Close()

	// Test ENQ handling
	_, err = conn.Write([]byte{ENQ})
	if err != nil {
		t.Fatalf("Failed to send ENQ: %v", err)
	}

	// Read ACK response
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	resp := make([]byte, 1)
	n, err := conn.Read(resp)
	if err != nil {
		t.Fatalf("Failed to read ACK: %v", err)
	}
	if n != 1 || resp[0] != ACK {
		t.Errorf("Expected ACK (0x06), got 0x%02x", resp[0])
	}

	// Send a check-in message: <STX>CHK1  2129<ETX>
	payload := "CHK1  2129"
	msg := buildMitelMessage(payload)

	_, err = conn.Write(msg)
	if err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}

	// Read ACK response
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, err = conn.Read(resp)
	if err != nil {
		t.Fatalf("Failed to read ACK: %v", err)
	}
	if n != 1 || resp[0] != ACK {
		t.Errorf("Expected ACK (0x06), got 0x%02x", resp[0])
	}

	// Wait for event
	select {
	case evt := <-ln.Events():
		if evt.Type != pms.EventCheckIn {
			t.Errorf("Event type = %v, want %v", evt.Type, pms.EventCheckIn)
		}
		if evt.Room != "2129" {
			t.Errorf("Room = %q, want %q", evt.Room, "2129")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for event")
	}

	// Send a NAK case (message too short)
	shortMsg := []byte{STX, 'C', 'H', 'K', ETX}
	_, err = conn.Write(shortMsg)
	if err != nil {
		t.Fatalf("Failed to send short message: %v", err)
	}

	// Read NAK response
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, err = conn.Read(resp)
	if err != nil {
		t.Fatalf("Failed to read NAK: %v", err)
	}
	if n != 1 || resp[0] != NAK {
		t.Errorf("Expected NAK (0x15), got 0x%02x", resp[0])
	}

	// Cancel context to stop listener
	cancel()

	// Wait for listener to stop
	select {
	case <-errCh:
		// Expected
	case <-time.After(2 * time.Second):
		t.Error("Listener did not stop in time")
	}
}

func TestListenerClose(t *testing.T) {
	l := NewListener("localhost", 0)
	ctx, cancel := context.WithCancel(context.Background())

	// Start listener
	errCh := make(chan error, 1)
	go func() {
		errCh <- l.Listen(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Close listener
	if err := l.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}

	cancel()

	select {
	case <-errCh:
		// Expected
	case <-time.After(2 * time.Second):
		t.Error("Listener did not stop")
	}

	// Double close should be safe
	if err := l.Close(); err != nil {
		t.Errorf("Second Close() returned error: %v", err)
	}
}

func TestListenerConcurrentConnections(t *testing.T) {
	l := NewListener("localhost", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- l.Listen(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	lnAddr := l.listener.Addr().String()

	// Open multiple connections concurrently
	const numConns = 3
	var wg sync.WaitGroup
	errChan := make(chan error, numConns)

	for i := 0; i < numConns; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			conn, err := net.Dial("tcp", lnAddr)
			if err != nil {
				errChan <- fmt.Errorf("dial: %w", err)
				return
			}
			defer conn.Close()

			// Send a message
			msg := buildMitelMessage(fmt.Sprintf("CHK1  %04d", idx))
			if _, err := conn.Write(msg); err != nil {
				errChan <- fmt.Errorf("write: %w", err)
				return
			}

			// Read ACK
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			resp := make([]byte, 1)
			if _, err := conn.Read(resp); err != nil {
				errChan <- fmt.Errorf("read: %w", err)
				return
			}
			if resp[0] != ACK {
				errChan <- fmt.Errorf("expected ACK, got 0x%02x", resp[0])
				return
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("Connection error: %v", err)
	}

	// Collect events
	eventsReceived := 0
	for i := 0; i < numConns; i++ {
		select {
		case <-l.Events():
			eventsReceived++
		case <-time.After(2 * time.Second):
			t.Fatalf("Timed out waiting for events, got %d of %d", eventsReceived, numConns)
		}
	}

	if eventsReceived != numConns {
		t.Errorf("Received %d events, want %d", eventsReceived, numConns)
	}

	cancel()
	<-errCh
}

func TestListenerIPAllowlist(t *testing.T) {
	l := NewListener("localhost", 0)
	l.allowed = []string{"127.0.0.1", "::1"} // Only allow localhost

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- l.Listen(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	lnAddr := l.listener.Addr().String()

	// Allowed connection should succeed
	t.Run("allowed IP connects successfully", func(t *testing.T) {
		conn, err := net.Dial("tcp", lnAddr)
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		msg := buildMitelMessage("CHK1  2129")
		if _, err := conn.Write(msg); err != nil {
			t.Fatalf("Failed to send message: %v", err)
		}

		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		resp := make([]byte, 1)
		n, err := conn.Read(resp)
		if err != nil {
			t.Fatalf("Failed to read ACK: %v", err)
		}
		if n != 1 || resp[0] != ACK {
			t.Errorf("Expected ACK, got 0x%02x", resp[0])
		}
	})

	cancel()
	<-errCh
}

func TestNewMitelListenerFactory(t *testing.T) {
	events := make(chan pms.Event, 10)
	cfg := pms.ListenerConfig{
		ListenHost: "0.0.0.0",
		ListenPort: 0, // Will use default
		AllowedPMSIPs: []string{"192.168.1.100"},
	}

	l, err := NewMitelListener(cfg, events)
	if err != nil {
		t.Fatalf("NewMitelListener failed: %v", err)
	}

	if l.Host() != "0.0.0.0" {
		t.Errorf("Host() = %q, want %q", l.Host(), "0.0.0.0")
	}

	// Port should default to DefaultPort when 0
	if l.Port() != DefaultPort {
		t.Errorf("Port() = %d, want %d (default)", l.Port(), DefaultPort)
	}
}

func TestListenerAllowedIPs(t *testing.T) {
	tests := []struct {
		name     string
		allowed  []string
		ip       string
		expected bool
	}{
		{
			name:     "empty allowlist permits all",
			allowed:  []string{},
			ip:       "192.168.1.100",
			expected: true,
		},
		{
			name:     "nil allowlist permits all",
			allowed:  nil,
			ip:       "192.168.1.100",
			expected: true,
		},
		{
			name:     "matching IP is allowed",
			allowed:  []string{"192.168.1.100", "10.0.0.1"},
			ip:       "192.168.1.100",
			expected: true,
		},
		{
			name:     "non-matching IP is denied",
			allowed:  []string{"192.168.1.100"},
			ip:       "10.0.0.1",
			expected: false,
		},
		{
			name:     "localhost IPv4 is allowed when listed",
			allowed:  []string{"127.0.0.1"},
			ip:       "127.0.0.1",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestMitelListener("localhost", 0)
			l.allowed = tt.allowed
			if got := l.isAllowed(tt.ip); got != tt.expected {
				t.Errorf("isAllowed(%q) = %v, want %v", tt.ip, got, tt.expected)
			}
		})
	}
}