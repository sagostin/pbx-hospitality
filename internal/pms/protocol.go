package pms

import (
	"context"
	"time"
)

// EventType represents the type of PMS event
type EventType int

const (
	EventCheckIn EventType = iota
	EventCheckOut
	EventMessageWaiting
	EventNameUpdate
	EventRoomStatus
	EventDND
	EventWakeUp
)

// String returns a human-readable event type name
func (e EventType) String() string {
	switch e {
	case EventCheckIn:
		return "check_in"
	case EventCheckOut:
		return "check_out"
	case EventMessageWaiting:
		return "message_waiting"
	case EventNameUpdate:
		return "name_update"
	case EventRoomStatus:
		return "room_status"
	case EventDND:
		return "dnd"
	case EventWakeUp:
		return "wake_up"
	default:
		return "unknown"
	}
}

// Event represents a parsed PMS event
type Event struct {
	Type      EventType
	Room      string
	GuestName string
	Status    bool // true=on/active, false=off/inactive
	Timestamp time.Time
	RawData   []byte
	Metadata  map[string]string // Protocol-specific fields
}

// Adapter is the interface for all PMS protocol implementations
type Adapter interface {
	// Protocol returns the protocol name (e.g., "mitel", "fias")
	Protocol() string

	// Connect establishes connection to the PMS
	Connect(ctx context.Context) error

	// Events returns a channel of parsed PMS events
	Events() <-chan Event

	// SendAck sends acknowledgement for an event
	SendAck() error

	// SendNak sends negative acknowledgement
	SendNak() error

	// Close terminates the connection
	Close() error

	// Connected returns true if the adapter is connected
	Connected() bool
}

// AdapterFactory creates a PMS adapter from configuration
type AdapterFactory func(host string, port int, opts ...AdapterOption) (Adapter, error)

// AdapterOption is a functional option for adapter configuration
type AdapterOption func(interface{})

// Registry holds available protocol adapters
var Registry = make(map[string]AdapterFactory)

// Register adds a protocol adapter factory to the registry
func Register(protocol string, factory AdapterFactory) {
	Registry[protocol] = factory
}

// NewAdapter creates a PMS adapter based on protocol name
func NewAdapter(protocol, host string, port int, opts ...AdapterOption) (Adapter, error) {
	factory, ok := Registry[protocol]
	if !ok {
		return nil, &ProtocolError{Protocol: protocol, Message: "unsupported protocol"}
	}
	return factory(host, port, opts...)
}

// ProtocolError represents a protocol-related error
type ProtocolError struct {
	Protocol string
	Message  string
}

func (e *ProtocolError) Error() string {
	return e.Protocol + ": " + e.Message
}
