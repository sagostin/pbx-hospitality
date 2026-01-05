package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// PMS connection status (1 = connected, 0 = disconnected)
	PMSConnectionStatus = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "hospitality",
			Subsystem: "pms",
			Name:      "connection_status",
			Help:      "PMS connection status (1=connected, 0=disconnected)",
		},
		[]string{"tenant", "protocol"},
	)

	// ARI connection status
	ARIConnectionStatus = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "hospitality",
			Subsystem: "ari",
			Name:      "connection_status",
			Help:      "ARI connection status (1=connected, 0=disconnected)",
		},
		[]string{"tenant"},
	)

	// PMS events received counter
	PMSEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "pms",
			Name:      "events_total",
			Help:      "Total PMS events received",
		},
		[]string{"tenant", "type"},
	)

	// PMS event processing errors
	PMSEventErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "pms",
			Name:      "event_errors_total",
			Help:      "Total PMS event processing errors",
		},
		[]string{"tenant", "type", "error"},
	)

	// PMS event processing duration
	PMSEventDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "hospitality",
			Subsystem: "pms",
			Name:      "event_duration_seconds",
			Help:      "PMS event processing duration",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"tenant", "type"},
	)

	// Bicom API request counter
	BicomAPIRequests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "bicom",
			Name:      "requests_total",
			Help:      "Total Bicom API requests",
		},
		[]string{"tenant", "action"},
	)

	// Bicom API errors
	BicomAPIErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "bicom",
			Name:      "errors_total",
			Help:      "Total Bicom API errors",
		},
		[]string{"tenant", "action"},
	)

	// Active guest sessions gauge
	ActiveSessions = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "hospitality",
			Subsystem: "guest",
			Name:      "active_sessions",
			Help:      "Number of active guest sessions",
		},
		[]string{"tenant"},
	)

	// Check-in/out counters
	CheckInsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "guest",
			Name:      "checkins_total",
			Help:      "Total guest check-ins",
		},
		[]string{"tenant"},
	)

	CheckOutsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "guest",
			Name:      "checkouts_total",
			Help:      "Total guest check-outs",
		},
		[]string{"tenant"},
	)
)
