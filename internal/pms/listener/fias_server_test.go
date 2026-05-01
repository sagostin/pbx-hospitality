package listener

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagostin/pbx-hospitality/internal/pms"
	"github.com/sagostin/pbx-hospitality/internal/pms/fias"
)

func TestFiasListenerDefaults(t *testing.T) {
	l := NewListener("localhost", FiasDefaultPort)
	if l.Host() != "localhost" {
		t.Errorf("Host() = %q, want %q", l.Host(), "localhost")
	}
	if l.Port() != FiasDefaultPort {
		t.Errorf("Port() = %d, want %d", l.Port(), FiasDefaultPort)
	}
}

func TestFiasListenerNewFiasListener(t *testing.T) {
	cfg := pms.ListenerConfig{
		ListenHost:    "0.0.0.0",
		ListenPort:    6000,
		AllowedPMSIPs: []string{"192.168.1.100", "10.0.0.1"},
	}
	events := make(chan pms.Event, 100)
	l, err := NewFiasListener(cfg, events)
	if err != nil {
		t.Fatalf("NewFiasListener failed: %v", err)
	}
	if l.Host() != "0.0.0.0" {
		t.Errorf("Host() = %q, want %q", l.Host(), "0.0.0.0")
	}
	if l.Port() != 6000 {
		t.Errorf("Port() = %d, want %d", l.Port(), 6000)
	}
}

func TestFiasListenerDefaultPort(t *testing.T) {
	cfg := pms.ListenerConfig{
		ListenHost:    "localhost",
		ListenPort:    0, // should default to FiasDefaultPort
		AllowedPMSIPs: []string{},
	}
	events := make(chan pms.Event, 100)
	l, err := NewFiasListener(cfg, events)
	if err != nil {
		t.Fatalf("NewFiasListener failed: %v", err)
	}
	if l.Port() != FiasDefaultPort {
		t.Errorf("Port() = %d, want %d (default)", l.Port(), FiasDefaultPort)
	}
}

func TestFiasIsAllowed(t *testing.T) {
	tests := []struct {
		name       string
		allowedIPs []string
		remoteIP   string
		want       bool
	}{
		{
			name:       "empty allowlist allows all",
			allowedIPs: []string{},
			remoteIP:   "192.168.1.100",
			want:       true,
		},
		{
			name:       "matching IP allowed",
			allowedIPs: []string{"192.168.1.100", "10.0.0.1"},
			remoteIP:   "192.168.1.100",
			want:       true,
		},
		{
			name:       "non-matching IP denied",
			allowedIPs: []string{"192.168.1.100"},
			remoteIP:   "192.168.1.200",
			want:       false,
		},
		{
			name:       "second IP in list allowed",
			allowedIPs: []string{"192.168.1.100", "10.0.0.1"},
			remoteIP:   "10.0.0.1",
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := NewListener("localhost", 5000)
			l.allowed = tt.allowedIPs
			if got := l.isAllowed(tt.remoteIP); got != tt.want {
				t.Errorf("isAllowed(%q) = %v, want %v", tt.remoteIP, got, tt.want)
			}
		})
	}
}

// TestFiasListenerIntegration tests the full FIAS listener with a real TCP connection
func TestFiasListenerIntegration(t *testing.T) {
	// Find an available port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to find available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	cfg := pms.ListenerConfig{
		ListenHost:    "localhost",
		ListenPort:    port,
		AllowedPMSIPs: []string{},
	}
	events := make(chan pms.Event, 100)
	l, err := NewFiasListener(cfg, events)
	if err != nil {
		t.Fatalf("NewFiasListener failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start listener
	errCh := make(chan error, 1)
	go func() {
		errCh <- l.Listen(ctx)
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Connect a fake PMS client
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), time.Second)
	if err != nil {
		cancel()
		t.Fatalf("failed to connect to listener: %v", err)
	}
	defer conn.Close()

	// Send LR handshake
	lr := "LR|DA|TI|RN|GN|FL|RI|\r\n"
	if _, err := conn.Write([]byte(lr)); err != nil {
		cancel()
		t.Fatalf("failed to send LR: %v", err)
	}

	// Read LR response
	respBuf := make([]byte, 100)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := conn.Read(respBuf)
	if err != nil {
		cancel()
		t.Fatalf("failed to read LR response: %v", err)
	}

	expectedResp := "LR|RN|GN|FL|DA|\r\n"
	if string(respBuf[:n]) != expectedResp {
		t.Errorf("LR response = %q, want %q", string(respBuf[:n]), expectedResp)
	}

	// Send guest check-in event
	gi := "GI|RN1015|GNSmith, John|DA260102|TI1430|\r\n"
	if _, err := conn.Write([]byte(gi)); err != nil {
		cancel()
		t.Fatalf("failed to send GI: %v", err)
	}

	// Wait for the event
	select {
	case evt := <-events:
		if evt.Type != pms.EventCheckIn {
			t.Errorf("event type = %v, want %v", evt.Type, pms.EventCheckIn)
		}
		if evt.Room != "1015" {
			t.Errorf("room = %q, want %q", evt.Room, "1015")
		}
		if evt.GuestName != "Smith, John" {
			t.Errorf("guest name = %q, want %q", evt.GuestName, "Smith, John")
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for check-in event")
	}

	// Send message waiting ON
	mw := "MW|RN1015|FL1|\r\n"
	if _, err := conn.Write([]byte(mw)); err != nil {
		cancel()
		t.Fatalf("failed to send MW: %v", err)
	}

	select {
	case evt := <-events:
		if evt.Type != pms.EventMessageWaiting {
			t.Errorf("event type = %v, want %v", evt.Type, pms.EventMessageWaiting)
		}
		if evt.Room != "1015" {
			t.Errorf("room = %q, want %q", evt.Room, "1015")
		}
		if !evt.Status {
			t.Error("status = false, want true (MW ON)")
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for message waiting event")
	}

	// Send wake-up call
	wk := "WK|RN1015|TI0700|\r\n"
	if _, err := conn.Write([]byte(wk)); err != nil {
		cancel()
		t.Fatalf("failed to send WK: %v", err)
	}

	select {
	case evt := <-events:
		if evt.Type != pms.EventWakeUp {
			t.Errorf("event type = %v, want %v", evt.Type, pms.EventWakeUp)
		}
		if evt.Room != "1015" {
			t.Errorf("room = %q, want %q", evt.Room, "1015")
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for wake-up event")
	}

	// Send link end to close gracefully
	le := "LE|\r\n"
	if _, err := conn.Write([]byte(le)); err != nil {
		cancel()
		t.Fatalf("failed to send LE: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	cancel()

	// Wait for listener to stop
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Logf("Listen returned: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for listener to stop")
	}
}

// TestFiasListenerIPAllowlist tests that IP allowlisting works
func TestFiasListenerIPAllowlist(t *testing.T) {
	// Find an available port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to find available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	cfg := pms.ListenerConfig{
		ListenHost:    "localhost",
		ListenPort:    port,
		AllowedPMSIPs: []string{"127.0.0.1"}, // Only allow localhost
	}
	events := make(chan pms.Event, 100)
	l, err := NewFiasListener(cfg, events)
	if err != nil {
		t.Fatalf("NewFiasListener failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- l.Listen(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Connecting from allowed IP should work
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), time.Second)
	if err != nil {
		cancel()
		t.Fatalf("failed to connect from allowed IP: %v", err)
	}

	// Send LR and check response
	conn.Write([]byte("LR|DA|TI|RN|GN|FL|\r\n"))
	buf := make([]byte, 100)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, _ := conn.Read(buf)
	if n == 0 {
		t.Error("expected LR response, got none")
	}
	conn.Close()

	cancel()
	<-errCh
}

// TestFiasListenerClose tests that Close stops the listener
func TestFiasListenerClose(t *testing.T) {
	l := NewListener("localhost", 0)
	if l.Port() != FiasDefaultPort {
		t.Errorf("default port = %d, want %d", l.Port(), FiasDefaultPort)
	}

	// Calling Close on unstarted listener should be safe
	l.Close()
}

// TestFiasListenerLinkRecordHandling tests LR/LS/LA/LE handling
func TestFiasListenerLinkRecordHandling(t *testing.T) {
	// Find an available port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to find available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	cfg := pms.ListenerConfig{
		ListenHost:    "localhost",
		ListenPort:    port,
		AllowedPMSIPs: []string{},
	}
	events := make(chan pms.Event, 100)
	l, err := NewFiasListener(cfg, events)
	if err != nil {
		t.Fatalf("NewFiasListener failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- l.Listen(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), time.Second)
	if err != nil {
		cancel()
		t.Fatalf("failed to connect: %v", err)
	}

	// Send LS (link start) — should be handled silently
	conn.Write([]byte("LS|\r\n"))

	// Send LA (link alive) — should respond with LA
	conn.Write([]byte("LA|\r\n"))
	buf := make([]byte, 100)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		cancel()
		t.Fatalf("failed to read LA response: %v", err)
	}
	if string(buf[:n]) != "LA|\r\n" {
		t.Errorf("LA response = %q, want %q", string(buf[:n]), "LA|\r\n")
	}

	// Send LR with specific fields
	lr := "LR|RN|GN|FL|DA|TI|\r\n"
	conn.Write([]byte(lr))
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, _ = conn.Read(buf)
	// Should respond with our supported fields
	if n == 0 {
		t.Error("expected LR response")
	}

	conn.Close()
	cancel()
	<-errCh
}

// TestFiasListenerMultipleConnections tests multiple simultaneous connections
func TestFiasListenerMultipleConnections(t *testing.T) {
	// Find an available port
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to find available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	cfg := pms.ListenerConfig{
		ListenHost:    "localhost",
		ListenPort:    port,
		AllowedPMSIPs: []string{},
	}
	events := make(chan pms.Event, 100)
	l, err := NewFiasListener(cfg, events)
	if err != nil {
		t.Fatalf("NewFiasListener failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- l.Listen(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Create 3 connections
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(roomNum string) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), time.Second)
			if err != nil {
				t.Logf("dial error: %v", err)
				return
			}
			defer conn.Close()

			// Send LR
			conn.Write([]byte("LR|DA|TI|RN|GN|FL|\r\n"))
			buf := make([]byte, 100)
			conn.SetReadDeadline(time.Now().Add(time.Second))
			conn.Read(buf)

			// Send check-in for this room
			gi := fmt.Sprintf("GI|RN%s|GNGuest %s|DA260102|\r\n", roomNum, roomNum)
			conn.Write([]byte(gi))
		}(fmt.Sprintf("1%03d", i)) // 1001, 1002, 1003
	}

	wg.Wait()

	// Should have received 3 events
	eventCount := 0
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-events:
			eventCount++
			if eventCount >= 3 {
				goto done
			}
		case <-timeout:
			goto done
		}
	}
done:
	if eventCount < 3 {
		t.Errorf("received %d events, want at least 3", eventCount)
	}

	cancel()
	<-errCh
}
