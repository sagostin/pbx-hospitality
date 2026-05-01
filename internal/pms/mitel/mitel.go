package mitel

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

const (
	STX = 0x02 // Start of text
	ETX = 0x03 // End of text
	ENQ = 0x05 // Enquiry
	ACK = 0x06 // Acknowledge
	NAK = 0x15 // Negative acknowledge
)

// Function codes for Mitel PMS protocol
const (
	FuncCheckIn = "CHK"
	FuncMsgWait = "MW "
	FuncName    = "NAM"
	FuncRoom    = "RM "
	FuncDND     = "DND"
)

func init() {
	pms.Register("mitel", NewAdapter)
}

// Adapter implements the PMS adapter for Mitel SX-200 protocol
type Adapter struct {
	host   string
	port   int
	conn   net.Conn
	events chan pms.Event
	mu     sync.RWMutex
	cancel context.CancelFunc
	wg     sync.WaitGroup

	connected   bool
	pendingName map[string]string // room -> "" (pending), populated by next message
}

// NewAdapter creates a new Mitel protocol adapter
func NewAdapter(host string, port int, opts ...pms.AdapterOption) (pms.Adapter, error) {
	a := &Adapter{
		host:        host,
		port:        port,
		events:      make(chan pms.Event, 100),
		pendingName: make(map[string]string),
	}

	for _, opt := range opts {
		opt(a)
	}

	return a, nil
}

// Protocol returns the protocol name
func (a *Adapter) Protocol() string {
	return "mitel"
}

// Connect establishes connection to the PMS
func (a *Adapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	ctx, a.cancel = context.WithCancel(ctx)

	addr := fmt.Sprintf("%s:%d", a.host, a.port)
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connecting to Mitel PMS at %s: %w", addr, err)
	}

	a.conn = conn
	a.connected = true

	log.Info().
		Str("addr", addr).
		Msg("Connected to Mitel PMS")

	// Start reading loop
	a.wg.Add(1)
	go a.readLoop(ctx)

	return nil
}

// Events returns the event channel
func (a *Adapter) Events() <-chan pms.Event {
	return a.events
}

// SendAck sends acknowledgement
func (a *Adapter) SendAck() error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.conn == nil {
		return fmt.Errorf("not connected")
	}

	_, err := a.conn.Write([]byte{ACK})
	return err
}

// SendNak sends negative acknowledgement
func (a *Adapter) SendNak() error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.conn == nil {
		return fmt.Errorf("not connected")
	}

	_, err := a.conn.Write([]byte{NAK})
	return err
}

// Close terminates the connection
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
	}

	if a.conn != nil {
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

// readLoop reads and parses messages from the PMS
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
		a.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		// Read until STX
		b, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Timeout is normal, continue waiting
				continue
			}
			log.Error().Err(err).Msg("Error reading from Mitel PMS")
			continue
		}

		// Handle ENQ (polling)
		if b == ENQ {
			a.SendAck()
			continue
		}

		// Expect STX
		if b != STX {
			continue
		}

		// Read message body until ETX
		var msg []byte
		for {
			b, err := reader.ReadByte()
			if err != nil {
				log.Error().Err(err).Msg("Error reading message body")
				break
			}
			if b == ETX {
				break
			}
			msg = append(msg, b)
		}

		if len(msg) == 0 {
			continue
		}

		// Parse the message
		evt, err := parseMessage(msg, a.pendingName)
		if err != nil {
			log.Error().Err(err).Bytes("raw", msg).Msg("Failed to parse Mitel message")
			a.SendNak()
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

// parseMessage parses a Mitel PMS message.
// It accepts an optional pendingName map for stateful NAM guest-name tracking.
// If pendingName is non-nil, NAM events use it to store/retrieve pending name data.
// Format: <FUNC><STATUS><ROOM#> (10 chars total)
// FUNC: 3 chars, STATUS: 2 chars, ROOM: 5 chars
// A name payload may arrive as a separate message longer than 10 chars,
// with the 5-char room number at the start followed by the guest name.
func parseMessage(msg []byte, pendingName map[string]string) (pms.Event, error) {
	// Handle variable-length name payload: starts with 5-char room number
	// followed by the guest name (e.g., "2129 Smith, John    ").
	// Only used when there's a pending room waiting for a name.
	if len(msg) > 10 && pendingName != nil {
		payloadRoom := strings.TrimSpace(string(msg[0:5]))
		if _, ok := pendingName[payloadRoom]; ok {
			name := strings.TrimSpace(string(msg[5:]))
			delete(pendingName, payloadRoom)
			return pms.Event{
				Type:      pms.EventNameUpdate,
				Room:      payloadRoom,
				GuestName: name,
				Status:    true,
				Timestamp: time.Now(),
				RawData:   msg,
				Metadata: map[string]string{
					"function": FuncName,
					"status":   "1",
				},
			}, nil
		}
	}

	if len(msg) < 10 {
		return pms.Event{}, fmt.Errorf("message too short: %d bytes", len(msg))
	}

	funcCode := string(msg[0:3])
	status := strings.TrimSpace(string(msg[3:5]))
	room := strings.TrimSpace(string(msg[5:10]))

	evt := pms.Event{
		Room:      room,
		Timestamp: time.Now(),
		RawData:   msg,
		Metadata:  make(map[string]string),
	}

	evt.Metadata["function"] = funcCode
	evt.Metadata["status"] = status

	// Parse based on function code
	switch funcCode {
	case FuncCheckIn:
		if status == "1" {
			evt.Type = pms.EventCheckIn
			evt.Status = true
		} else {
			evt.Type = pms.EventCheckOut
			evt.Status = false
		}

	case FuncMsgWait:
		evt.Type = pms.EventMessageWaiting
		evt.Status = status == "1"

	case FuncName:
		evt.Type = pms.EventNameUpdate
		evt.Status = status == "1"
		// Mitel SX-200 sends NAM followed by a name payload message.
		// Register room as pending; if pendingName already has a value for this
		// room, consume it as the guest name (name payload arrived first).
		if pendingName != nil {
			if name, ok := pendingName[room]; ok && name != "" {
				evt.GuestName = name
				delete(pendingName, room)
			} else {
				// Mark as pending; next message for this room carries the name.
				pendingName[room] = ""
			}
		}

	case FuncRoom:
		evt.Type = pms.EventRoomStatus
		evt.Status = status == "1"

	case FuncDND:
		evt.Type = pms.EventDND
		evt.Status = status == "1"

	default:
		return pms.Event{}, fmt.Errorf("unknown function code: %s", funcCode)
	}

	return evt, nil
}

// ParseMessage is exported for testing
func ParseMessage(msg []byte) (pms.Event, error) {
	return parseMessage(msg, nil)
}
