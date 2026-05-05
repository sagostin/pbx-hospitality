package listener

import "github.com/sagostin/pbx-hospitality/internal/pms"

func newTestFiasListener(host string, port int) *FiasListener {
	events := make(chan pms.Event, 100)
	l := &FiasListener{
		host:   host,
		port:   port,
		events: events,
	}
	if l.port == 0 {
		l.port = FiasDefaultPort
	}
	return l
}

func newTestMitelListener(host string, port int) *MitelListener {
	events := make(chan pms.Event, 100)
	l := &MitelListener{
		host:   host,
		port:   port,
		events: events,
	}
	if l.port == 0 {
		l.port = DefaultPort
	}
	return l
}
