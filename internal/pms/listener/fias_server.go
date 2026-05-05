package listener

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/pms"
	"github.com/sagostin/pbx-hospitality/internal/pms/fias"
)

const (
	// FiasDefaultPort is the default TCP listen port for FIAS PMS connections
	FiasDefaultPort = 5000

	// FiasConnectionTimeout is how long to wait for activity before closing idle connection
	FiasConnectionTimeout = 60 * time.Second

	// FiasMaxLineSize is the maximum size of a single FIAS record line
	FiasMaxLineSize = 4096
)

func init() {
	pms.RegisterListener("fias", NewFiasListener)
}

// Listener implements a TCP server that listens for incoming FIAS PMS connections.
// Each connection is handled independently, parsing pipe-delimited records and
// converting them to PMS events for downstream processing.
type Listener struct {
	host    string
	port    int
	events  chan pms.Event
	allowed []string // allowed PMS IPs; if empty, all IPs are allowed
	listener net.Listener
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.RWMutex
	closed  bool
}

// NewListener creates a new FIAS PMS Listener TCP server.
func NewListener(host string, port int) *Listener {
	return &Listener{
		host:   host,
		port:   port,
		events: make(chan pms.Event, 100),
	}
}

// NewFiasListener is the factory function registered with the PMS listener registry.
func NewFiasListener(cfg pms.ListenerConfig, events chan pms.Event) (pms.Listener, error) {
	l := &Listener{
		host:    cfg.ListenHost,
		port:    cfg.ListenPort,
		events:  events,
		allowed: cfg.AllowedPMSIPs,
	}
	if l.port == 0 {
		l.port = FiasDefaultPort
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
		Msg("FIAS PMS Listener started")

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
			log.Error().Err(err).Msg("Accept error on FIAS PMS Listener")
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

// handleConnection processes a single FIAS PMS TCP connection.
// It reads line-delimited records, handles link handshake, and emits events.
func (l *Listener) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	remoteIP, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		remoteIP = remoteAddr
	}

	// Check IP allowlist
	if !l.isAllowed(remoteIP) {
		log.Warn().Str("remote", remoteAddr).Msg("FIAS PMS connection rejected: IP not allowed")
		conn.Close()
		return
	}

	log.Info().
		Str("remote", remoteAddr).
		Msg("FIAS PMS connection opened")

	// Set connection-level deadlines
	conn.SetReadDeadline(time.Now().Add(FiasConnectionTimeout))
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

	reader := bufio.NewReaderSize(conn, FiasMaxLineSize)

	// Per-connection link state
	var linkedRecords []string

	for {
		// Check context before each read
		select {
		case <-ctx.Done():
			log.Debug().Str("remote", remoteAddr).Msg("FIAS connection closed: context cancelled")
			return
		default:
		}

		// Extend deadline on each successful operation
		conn.SetReadDeadline(time.Now().Add(FiasConnectionTimeout))

		// Read a line (FIAS records are line-delimited)
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Send keepalive
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				conn.Write([]byte("LA|\r\n"))
				conn.SetReadDeadline(time.Now().Add(FiasConnectionTimeout))
				continue
			}
			log.Debug().Err(err).Str("remote", remoteAddr).Msg("FIAS connection closed: read error")
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse the record
		evt, err := fias.ParseRecord(line)
		if err != nil {
			if err == fias.ErrLinkRecord {
				// Handle link records (LR/LS/LA/LE) — not emitted as events
				linkedRecords = l.handleLinkRecord(line, conn, linkedRecords)
				continue
			}
			log.Error().
				Err(err).
				Str("raw", line).
				Str("remote", remoteAddr).
				Msg("Failed to parse FIAS record")
			continue
		}

		// Extend deadline after successful parse
		conn.SetReadDeadline(time.Now().Add(FiasConnectionTimeout))

		// Send event to channel
		select {
		case l.events <- evt:
			log.Debug().
				Str("remote", remoteAddr).
				Str("event", evt.Type.String()).
				Str("room", evt.Room).
				Msg("FIAS PMS event emitted")
		case <-ctx.Done():
			return
		default:
			log.Warn().
				Str("remote", remoteAddr).
				Msg("FIAS event channel full, dropping event")
		}
	}
}

// handleLinkRecord processes LR/LS/LA/LE records for a connection.
// Returns the updated list of linked records for this connection.
func (l *Listener) handleLinkRecord(line string, conn net.Conn, linkedRecords []string) []string {
	fields := strings.Split(line, "|")
	if len(fields) == 0 {
		return linkedRecords
	}

	recordType := fields[0]

	switch recordType {
	case fias.RecordLinkRecord:
		// PMS sends LR with the record types it supports
		// Respond with the record types we support
		linkedRecords = fields[1:]
		log.Info().
			Strs("remote", []string{conn.RemoteAddr().String()}).
			Strs("records", linkedRecords).
			Msg("FIAS link record received")

		response := fmt.Sprintf("LR|%s|%s|%s|%s|\r\n",
			fias.FieldRoomNumber, fias.FieldGuestName, fias.FieldFlag, fias.FieldDate)
		conn.Write([]byte(response))

	case fias.RecordLinkStart:
		log.Info().
			Str("remote", conn.RemoteAddr().String()).
			Msg("FIAS link started")

	case fias.RecordLinkAlive:
		// Respond to keepalive
		conn.Write([]byte("LA|\r\n"))

	case fias.RecordLinkEnd:
		log.Info().
			Str("remote", conn.RemoteAddr().String()).
			Msg("FIAS link ended by remote")
	}

	return linkedRecords
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
		Msg("FIAS PMS Listener stopped")

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
