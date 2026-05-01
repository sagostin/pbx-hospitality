package fias

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
)

// Record types for FIAS protocol
const (
	RecordLinkRecord  = "LR" // Link Record (handshake)
	RecordGuestIn     = "GI" // Guest Check-In
	RecordGuestOut    = "GO" // Guest Check-Out
	RecordMessageWait = "MW" // Message Waiting
	RecordRoomStatus  = "RS" // Room Status
	RecordWakeUp      = "WK" // Wake-Up Call
	RecordLinkStart   = "LS" // Link Start
	RecordLinkAlive   = "LA" // Link Alive (keepalive)
	RecordLinkEnd     = "LE" // Link End
)

// Field prefixes for FIAS protocol
const (
	FieldRoomNumber  = "RN" // Room Number
	FieldGuestName   = "GN" // Guest Name
	FieldDate        = "DA" // Date (YYMMDD)
	FieldTime        = "TI" // Time (HHMM)
	FieldFlag        = "FL" // Flag (0/1)
	FieldReservation = "RI" // Reservation ID
	FieldGuestNumber = "G#" // Guest Number
	FieldWorkstation = "WS" // Workstation ID
)

func init() {
	pms.Register("fias", NewAdapter)
}

// Default values for FIAS adapter
const (
	DefaultFiasPort      = 5000  // Default FIAS server port
	DefaultFiasTimeout   = 60    // seconds
	MaxLineSize          = 4096 // Maximum FIAS record line size
	MaxConcurrentConns   = 50    // Maximum concurrent PMS connections
)

// Adapter implements the PMS adapter for FIAS protocol
type Adapter struct {
	// Connection mode config
	host   string
	port   int

	// Listen mode config
	listenHost    string
	listenPort    int
	allowedPMSIPs []string // IP whitelist for connections

	// Runtime state
	conn   net.Conn
	events chan pms.Event
	mu     sync.RWMutex
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Server-side state (listen mode)
	listener    net.Listener
	connections map[string]net.Conn // host:conn for multi-tenant isolation

	connected     bool
	linkedRecords []string // Records negotiated in LR handshake
}

// ListenConfig holds listen mode configuration
type ListenConfig struct {
	Host          string   // Listen host (empty = all interfaces)
	Port          int      // Listen port (use negative to signal listen mode via PMSHost/PMSPort)
	AllowedIPs    []string // Whitelist of allowed PMS IPs
}

// NewAdapter creates a new FIAS protocol adapter.
// Supports two modes:
//   - Connect mode (default): connects TO the PMS host:port
//   - Listen mode: binds a local socket and accepts incoming PMS connections
//     Activated when host is empty or negative port is provided.
//     Use WithListenConfig() option to specify listen address and AllowedPMSIPs.
func NewAdapter(host string, port int, opts ...pms.AdapterOption) (pms.Adapter, error) {
	a := &Adapter{
		host:          host,
		port:          port,
		events:        make(chan pms.Event, 100),
		connections:   make(map[string]net.Conn),
	}

	for _, opt := range opts {
		opt(a)
	}

	return a, nil
}

// WithListenConfig configures the adapter for listen (server) mode.
// The PMS connects TO this adapter instead of the adapter connecting to PMS.
func WithListenConfig(cfg ListenConfig) pms.AdapterOption {
	return func(v interface{}) {
		if a, ok := v.(*Adapter); ok {
			a.listenHost = cfg.Host
			a.listenPort = cfg.Port
			a.allowedPMSIPs = cfg.AllowedIPs
		}
	}
}

// isListenMode returns true if the adapter should operate in listen mode.
// Listen mode is activated when:
// - host is empty string
// - port is negative (absolute value indicates the actual port)
func (a *Adapter) isListenMode() bool {
	// Empty host or negative port signals listen mode
	return a.host == "" || a.port < 0
}

// getListenPort returns the actual port number for listen mode.
func (a *Adapter) getListenPort() int {
	if a.port < 0 {
		return -a.port
	}
	if a.listenPort != 0 {
		return a.listenPort
	}
	return DefaultFiasPort
}

// Protocol returns the protocol name
func (a *Adapter) Protocol() string {
	return "fias"
}

// Connect establishes connection to the PMS or starts the server (listen mode)
func (a *Adapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	ctx, a.cancel = context.WithCancel(ctx)

	if a.isListenMode() {
		return a.listen(ctx)
	}
	return a.connect(ctx)
}

// connect establishes a TCP connection to the PMS (client mode)
func (a *Adapter) connect(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", a.host, a.port)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connecting to FIAS PMS at %s: %w", addr, err)
	}

	a.conn = conn
	a.connected = true

	log.Info().
		Str("addr", addr).
		Msg("Connected to FIAS PMS")

	// Start reading loop
	a.wg.Add(1)
	go a.readLoop(ctx, conn)

	return nil
}

// listen starts the TCP server and accepts incoming PMS connections (server mode)
func (a *Adapter) listen(ctx context.Context) error {
	port := a.getListenPort()
	addr := fmt.Sprintf("%s:%d", a.listenHost, port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	a.listener = ln

	log.Info().
		Str("host", a.listenHost).
		Int("port", port).
		Strs("allowed_ips", a.allowedPMSIPs).
		Msg("FIAS PMS Listener started (server mode)")

	// Accept loop - each PMS connects independently
	a.wg.Add(1)
	go a.acceptLoop(ctx)

	return nil
}

// acceptLoop accepts incoming PMS connections
func (a *Adapter) acceptLoop(ctx context.Context) {
	defer a.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		a.listener.(*net.TCPListener).SetDeadline(time.Now().Add(1 * time.Second))

		conn, err := a.listener.Accept()
		if err != nil {
			if a.isClosed() {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Error().Err(err).Msg("Accept error on FIAS Listener")
			continue
		}

		// Check IP allowlist
		if !a.isIPAllowed(conn.RemoteAddr().String()) {
			log.Warn().
				Str("remote", conn.RemoteAddr().String()).
				Msg("FIAS connection rejected: IP not in allowlist")
			conn.Close()
			continue
		}

		// Handle connection in goroutine
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			a.handleConnection(ctx, conn)
		}()
	}
}

// isClosed returns true if the listener is closed (thread-safe)
func (a *Adapter) isClosed() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return !a.connected && a.listener == nil
}

// isIPAllowed checks if the remote IP is in the allowlist
func (a *Adapter) isIPAllowed(remoteAddr string) bool {
	if len(a.allowedPMSIPs) == 0 {
		return true // No allowlist = allow all
	}

	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}

	for _, ip := range a.allowedPMSIPs {
		if ip == host {
			return true
		}
	}
	return false
}

// handleConnection processes a single PMS connection in listen mode
func (a *Adapter) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	log.Info().
		Str("remote", remoteAddr).
		Msg("FIAS PMS connection opened")

	// Store connection for potential per-tenant isolation
	a.mu.Lock()
	a.connections[remoteAddr] = conn
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		delete(a.connections, remoteAddr)
		a.mu.Unlock()
	}()

	reader := bufio.NewReaderSize(conn, MaxLineSize)

	// Per-connection link state (for multi-tenant)
	var connLinkedRecords []string

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(DefaultFiasTimeout * time.Second))

		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Send keepalive
				conn.Write([]byte("LA|\r\n"))
				continue
			}
			log.Debug().Err(err).Str("remote", remoteAddr).Msg("FIAS read error")
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse the record
		evt, err := parseRecord(line)
		if err != nil {
			if err == ErrLinkRecord {
				connLinkedRecords = a.handleLinkRecordForConn(line, conn, connLinkedRecords)
				continue
			}
			log.Error().Err(err).Str("raw", line).Str("remote", remoteAddr).Msg("Failed to parse FIAS record")
			continue
		}

		// Send event
		select {
		case a.events <- evt:
			log.Debug().
				Str("remote", remoteAddr).
				Str("event", evt.Type.String()).
				Str("room", evt.Room).
				Msg("FIAS event emitted")
		case <-ctx.Done():
			return
		default:
			log.Warn().Str("remote", remoteAddr).Msg("Event channel full, dropping event")
		}
	}
}

// handleLinkRecordForConn processes LR/LA/LS records for a specific connection
func (a *Adapter) handleLinkRecordForConn(line string, conn net.Conn, linkedRecords []string) []string {
	fields := strings.Split(line, "|")
	if len(fields) == 0 {
		return linkedRecords
	}

	recordType := fields[0]

	switch recordType {
	case RecordLinkRecord:
		// Store linked record types for this connection
		linkedRecords = fields[1:]
		log.Info().Strs("records", linkedRecords).Str("remote", conn.RemoteAddr().String()).Msg("FIAS link record received")

		// Respond with our supported records
		response := fmt.Sprintf("LR|%s|%s|%s|%s|\r\n",
			FieldRoomNumber, FieldGuestName, FieldFlag, FieldDate)
		conn.Write([]byte(response))

	case RecordLinkStart:
		log.Info().Str("remote", conn.RemoteAddr().String()).Msg("FIAS link started")

	case RecordLinkAlive:
		// Respond to keepalive
		conn.Write([]byte("LA|\r\n"))

	case RecordLinkEnd:
		log.Info().Str("remote", conn.RemoteAddr().String()).Msg("FIAS link ended by remote")
	}

	return linkedRecords
}

// Events returns the event channel
func (a *Adapter) Events() <-chan pms.Event {
	return a.events
}

// SendAck sends acknowledgement (FIAS uses different mechanism)
func (a *Adapter) SendAck() error {
	// FIAS doesn't use explicit ACK, responses are record-based
	return nil
}

// SendNak sends negative acknowledgement
func (a *Adapter) SendNak() error {
	// FIAS doesn't use explicit NAK
	return nil
}

// Close terminates the connection or stops the server
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
	}

	// Close single connection (client mode)
	if a.conn != nil {
		a.conn.Write([]byte("LE|\r\n"))
		a.conn.Close()
		a.connected = false
	}

	// Close all connections (server mode)
	for addr, conn := range a.connections {
		conn.Close()
		delete(a.connections, addr)
	}

	// Close listener (server mode)
	if a.listener != nil {
		a.listener.Close()
		a.listener = nil
	}

	a.connected = false
	a.wg.Wait()
	close(a.events)

	return nil
}

// Connected returns connection status
func (a *Adapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

// readLoop reads and parses records from the PMS (client mode)
func (a *Adapter) readLoop(ctx context.Context, conn net.Conn) {
	defer a.wg.Done()

	reader := bufio.NewReaderSize(conn, MaxLineSize)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Set read deadline
		conn.SetReadDeadline(time.Now().Add(DefaultFiasTimeout * time.Second))

		// Read a line (FIAS records are line-delimited)
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Send keepalive
				conn.Write([]byte("LA|\r\n"))
				continue
			}
			log.Error().Err(err).Msg("Error reading from FIAS PMS")
			continue
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse the record
		evt, err := parseRecord(line)
		if err != nil {
			if err == ErrLinkRecord {
				// Handle special records
				a.handleLinkRecord(line, conn)
				continue
			}
			log.Error().Err(err).Str("raw", line).Msg("Failed to parse FIAS record")
			continue
		}

		// Send event
		select {
		case a.events <- evt:
		case <-ctx.Done():
			return
		default:
			log.Warn().Msg("Event channel full, dropping event")
		}
	}
}

// handleLinkRecord processes LR/LA/LS records (client mode)
func (a *Adapter) handleLinkRecord(line string, conn net.Conn) {
	fields := strings.Split(line, "|")
	if len(fields) == 0 {
		return
	}

	recordType := fields[0]

	switch recordType {
	case RecordLinkRecord:
		// Store linked record types
		a.mu.Lock()
		a.linkedRecords = fields[1:]
		a.mu.Unlock()
		log.Info().Strs("records", a.linkedRecords).Msg("FIAS link record received")

		// Respond with our supported records
		response := fmt.Sprintf("LR|%s|%s|%s|%s|\r\n",
			FieldRoomNumber, FieldGuestName, FieldFlag, FieldDate)
		conn.Write([]byte(response))

	case RecordLinkStart:
		log.Info().Msg("FIAS link started")

	case RecordLinkAlive:
		// Respond to keepalive
		conn.Write([]byte("LA|\r\n"))

	case RecordLinkEnd:
		log.Info().Msg("FIAS link ended by remote")
		a.Close()
	}
}

// ErrLinkRecord is returned when a link record (LR/LS/LA/LE) is parsed.
// These records are handled specially by the protocol and not emitted as events.
var ErrLinkRecord = fmt.Errorf("link record")

// parseRecord parses a FIAS record
// Format: <RECORD_TYPE>|<FIELD>=<VALUE>|<FIELD>=<VALUE>|...|
func parseRecord(line string) (pms.Event, error) {
	fields := strings.Split(line, "|")
	if len(fields) < 2 {
		return pms.Event{}, fmt.Errorf("invalid record format")
	}

	recordType := fields[0]

	// Handle link/keepalive records separately
	switch recordType {
	case RecordLinkRecord, RecordLinkStart, RecordLinkAlive, RecordLinkEnd:
		return pms.Event{}, ErrLinkRecord
	}

	// Parse fields into map
	fieldMap := make(map[string]string)
	for _, f := range fields[1:] {
		if len(f) >= 2 {
			key := f[:2]
			value := ""
			if len(f) > 2 {
				value = f[2:]
			}
			fieldMap[key] = value
		}
	}

	evt := pms.Event{
		Room:      fieldMap[FieldRoomNumber],
		GuestName: fieldMap[FieldGuestName],
		Timestamp: time.Now(),
		RawData:   []byte(line),
		Metadata:  fieldMap,
	}

	// Determine event type from record type
	switch recordType {
	case RecordGuestIn:
		evt.Type = pms.EventCheckIn
		evt.Status = true

	case RecordGuestOut:
		evt.Type = pms.EventCheckOut
		evt.Status = false

	case RecordMessageWait:
		evt.Type = pms.EventMessageWaiting
		evt.Status = fieldMap[FieldFlag] == "1"

	case RecordRoomStatus:
		evt.Type = pms.EventRoomStatus
		evt.Status = fieldMap[FieldFlag] == "1"

	case RecordWakeUp:
		evt.Type = pms.EventWakeUp
		evt.Status = true

	default:
		return pms.Event{}, fmt.Errorf("unknown record type: %s", recordType)
	}

	return evt, nil
}

// ParseRecord is exported for testing
func ParseRecord(line string) (pms.Event, error) {
	return parseRecord(line)
}
