package listener

import "github.com/sagostin/pbx-hospitality/internal/pms"

// newTestFiasListener creates a test listener that passes the port through
// unchanged (including 0, which asks the OS to assign a free ephemeral port).
// Production code uses the constructors in fias_server.go / mitel_server.go,
// which DO remap port=0 to the protocol's default; tests don't want that
// because defaults are often privileged (e.g., Mitel = 23).
func newTestFiasListener(host string, port int) *FiasListener {
	events := make(chan pms.Event, 100)
	return &FiasListener{
		host:   host,
		port:   port,
		events: events,
	}
}

func newTestMitelListener(host string, port int) *MitelListener {
	events := make(chan pms.Event, 100)
	return &MitelListener{
		host:   host,
		port:   port,
		events: events,
	}
}
