package listener

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/pms"
	"github.com/sagostin/pbx-hospitality/internal/pms/mitel"
)

const (
	// DefaultPort is the default TCP listen port for Mitel PMS connections
	DefaultPort = 23

	// ACKTimeout is the maximum time to send ACK response (< 100ms SLA)
	ACKTimeout = 100 * time.Millisecond

	// ConnectionTimeout is how long to wait for activity before closing idle connection
	ConnectionTimeout = 5 * time.Minute

	// MaxMessageSize is the maximum size of a single PMS message
	MaxMessageSize = 256
)

func init() {
	pms.RegisterListener("mitel", NewMitelListener)
}

// Listener implements a TCP server that listens for incoming Mitel PMS connections.
// Each connection is handled independently, parsing STX/ETX framed messages and
// converting them to PMS events for downstream processing.
type Listener struct {
	host    string
	port    int
	events  chan pms.Event
	allowed []string // allowed PMS IPs; if empty, all IPs are allowed
	listener net.Listener
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu     sync.RWMutex
	closed  bool
}

// NewListener creates a new PMS Listener TCP server.
func NewListener(host string, port int) *Listener {
	return &Listener{
		host:   host,
		port:   port,
		events: make(chan pms.Event, 100),
	}
}

// NewMitelListener is the factory function registered with the PMS listener registry.
func NewMitelListener(cfg pms.ListenerConfig, events chan pms.Event) (pms.Listener, error) {
	l := &Listener{
		host:    cfg.ListenHost,
		port:    cfg.ListenPort,
		events:  events,
		allowed: cfg.AllowedPMSIPs,
	}
	if l.port == 0 {
		l.port = DefaultPort
	}
	return l, nil
}

// Events returns the channel of parsed PMS events from all connections.
func (l *Listener) Events() <-chan pms.Event {
	return l.events
}

// isAllowed checks if the remote IP is allowed to connect.
func (l *Listener) isAllowed(remoteIP string) bool {
	if len(l.allowed) == 0 {
		return true
	}
	for _, ip := range l.allowed {
		if ip == remoteIP {
			return true
		}
	}
	return false
}

// Listen starts the TCP server and accepts incoming connections.
// It blocks until the context is cancelled or an error occurs.
func (l *Listener) Listen(ctx context.Context) error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return fmt.Errorf("listener is closed")
	}

	addr := fmt.Sprintf("%s:%d", l.host, l.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		l.mu.Unlock()
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	l.listener = ln
	l.closed = false
	l.mu.Unlock()

	ctx, l.cancel = context.WithCancel(ctx)

	log.Info().
		Str("host", l.host).
		Int("port", l.port).
		Msg("PMS Listener started")

	// Accept loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Set accept deadline to allow periodic context checks
		l.listener.(*net.TCPListener).SetDeadline(time.Now().Add(1 * time.Second))

		conn, err := l.listener.Accept()
		if err != nil {
			if l.closed {
				return nil
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Deadline hit, check context and continue
				continue
			}
			log.Error().Err(err).Msg("Accept error on PMS Listener")
			continue
		}

		// Handle each connection in a goroutine
		l.wg.Add(1)
		go func() {
			defer l.wg.Done()
			l.handleConnection(ctx, conn)
		}()
	}
}

// handleConnection processes a single PMS TCP connection.
// It reads STX/ETX framed messages, sends ACK/NAK responses, and emits events.
func (l *Listener) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	remoteIP, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		remoteIP = remoteAddr
	}

	// Check IP allowlist
	if !l.isAllowed(remoteIP) {
		log.Warn().Str("remote", remoteAddr).Msg("PMS connection rejected: IP not allowed")
		conn.Close()
		return
	}

	log.Info().
		Str("remote", remoteAddr).
		Msg("PMS connection opened")

	// Set connection-level deadlines
	conn.SetReadDeadline(time.Now().Add(ConnectionTimeout))
	conn.SetWriteDeadline(time.Now().Add(ACKTimeout))

	reader := bufio.NewReaderSize(conn, MaxMessageSize)

	for {
		// Check context before each read
		select {
		case <-ctx.Done():
			log.Debug().Str("remote", remoteAddr).Msg("Connection closed: context cancelled")
			return
		default:
		}

		// Extend deadline on each successful operation
		conn.SetReadDeadline(time.Now().Add(ConnectionTimeout))

		// Read until STX or ENQ
		b, err := reader.ReadByte()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				log.Debug().Str("remote", remoteAddr).Msg("Connection closed: idle timeout")
			} else {
				log.Debug().Err(err).Str("remote", remoteAddr).Msg("Connection closed: read error")
			}
			return
		}

		// Handle ENQ (polling) - respond with ACK immediately
		if b == mitel.ENQ {
			conn.SetWriteDeadline(time.Now().Add(ACKTimeout))
			if _, err := conn.Write([]byte{mitel.ACK}); err != nil {
				log.Warn().Err(err).Str("remote", remoteAddr).Msg("Failed to send ACK for ENQ")
				return
			}
			// Extend deadline after successful ACK
			conn.SetReadDeadline(time.Now().Add(ConnectionTimeout))
			continue
		}

		// Expect STX (start of message)
		if b != mitel.STX {
			// Not STX, skip and continue looking
			continue
		}

		// Read message body until ETX
		var msg []byte
		for {
			b, err := reader.ReadByte()
			if err != nil {
				log.Warn().Err(err).Str("remote", remoteAddr).Msg("Error reading message body")
				return
			}
			if b == mitel.ETX {
				break
			}
			if len(msg) >= MaxMessageSize {
				log.Warn().
					Str("remote", remoteAddr).
					Int("size", len(msg)).
					Msg("Message exceeds max size, aborting")
				return
			}
			msg = append(msg, b)
		}

		if len(msg) == 0 {
			// Empty message, send NAK
			l.sendResponse(conn, mitel.NAK, remoteAddr)
			continue
		}

		// Parse the message using the mitel package parser
		evt, err := mitel.ParseMessage(msg)
		if err != nil {
			log.Error().
				Err(err).
				Str("remote", remoteAddr).
				Bytes("raw", msg).
				Msg("Failed to parse Mitel message")
			l.sendResponse(conn, mitel.NAK, remoteAddr)
			continue
		}

		// Send ACK before processing (meets < 100ms SLA)
		if !l.sendResponse(conn, mitel.ACK, remoteAddr) {
			return
		}

		// Extend read deadline after successful ACK
		conn.SetReadDeadline(time.Now().Add(ConnectionTimeout))

		// Send event to channel
		select {
		case l.events <- evt:
			log.Debug().
				Str("remote", remoteAddr).
				Str("event", evt.Type.String()).
				Str("room", evt.Room).
				Msg("PMS event emitted")
		case <-ctx.Done():
			return
		default:
			log.Warn().
				Str("remote", remoteAddr).
				Msg("Event channel full, dropping event")
		}
	}
}

// sendResponse sends an ACK or NAK response to the PMS.
// Returns false if the write fails (connection should be closed).
func (l *Listener) sendResponse(conn net.Conn, response byte, remoteAddr string) bool {
	conn.SetWriteDeadline(time.Now().Add(ACKTimeout))
	if _, err := conn.Write([]byte{response}); err != nil {
		log.Warn().
			Err(err).
			Str("remote", remoteAddr).
			Msg("Failed to send response")
		return false
	}
	return true
}

// Close stops the listener and closes all active connections.
func (l *Listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}

	l.closed = true

	if l.cancel != nil {
		l.cancel()
	}

	if l.listener != nil {
		l.listener.Close()
	}

	// Wait for connection handlers
	l.wg.Wait()

	close(l.events)

	log.Info().
		Str("host", l.host).
		Int("port", l.port).
		Msg("PMS Listener stopped")

	return nil
}

// Host returns the listen address.
func (l *Listener) Host() string {
	return l.host
}

// Port returns the listen port.
func (l *Listener) Port() int {
	return l.port
}

// ParseMessage is a convenience wrapper for testing.
// Parses a raw message payload (between STX and ETX).
func ParseMessage(msg []byte) (pms.Event, error) {
	// Validate and strip whitespace
	msgStr := strings.TrimSpace(string(msg))
	return mitel.ParseMessage([]byte(msgStr))
}