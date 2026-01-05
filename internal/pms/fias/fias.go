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

	"github.com/topsoffice/bicom-hospitality/internal/pms"
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

// Adapter implements the PMS adapter for FIAS protocol
type Adapter struct {
	host   string
	port   int
	conn   net.Conn
	events chan pms.Event
	mu     sync.RWMutex
	cancel context.CancelFunc
	wg     sync.WaitGroup

	connected     bool
	linkedRecords []string // Records negotiated in LR handshake
}

// NewAdapter creates a new FIAS protocol adapter
func NewAdapter(host string, port int, opts ...pms.AdapterOption) (pms.Adapter, error) {
	a := &Adapter{
		host:   host,
		port:   port,
		events: make(chan pms.Event, 100),
	}

	for _, opt := range opts {
		opt(a)
	}

	return a, nil
}

// Protocol returns the protocol name
func (a *Adapter) Protocol() string {
	return "fias"
}

// Connect establishes connection to the PMS
func (a *Adapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	ctx, a.cancel = context.WithCancel(ctx)

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
	go a.readLoop(ctx)

	return nil
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

// Close terminates the connection
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
	}

	if a.conn != nil {
		// Send LE (Link End) before closing
		a.conn.Write([]byte("LE|\r\n"))
		a.conn.Close()
		a.connected = false
	}

	close(a.events)
	a.wg.Wait()

	return nil
}

// Connected returns connection status
func (a *Adapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

// readLoop reads and parses records from the PMS
func (a *Adapter) readLoop(ctx context.Context) {
	defer a.wg.Done()

	reader := bufio.NewReader(a.conn)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Set read deadline
		a.conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		// Read a line (FIAS records are line-delimited)
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Send keepalive
				a.conn.Write([]byte("LA|\r\n"))
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
			if err == errLinkRecord || err == errKeepalive {
				// Handle special records
				a.handleLinkRecord(line)
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

// handleLinkRecord processes LR/LA/LS records
func (a *Adapter) handleLinkRecord(line string) {
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
		a.conn.Write([]byte(response))

	case RecordLinkStart:
		log.Info().Msg("FIAS link started")

	case RecordLinkAlive:
		// Respond to keepalive
		a.conn.Write([]byte("LA|\r\n"))

	case RecordLinkEnd:
		log.Info().Msg("FIAS link ended by remote")
		a.Close()
	}
}

var errLinkRecord = fmt.Errorf("link record")
var errKeepalive = fmt.Errorf("keepalive")

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
		return pms.Event{}, errLinkRecord
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
