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
	GuestCheckInsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "guest",
			Name:      "checkins_total",
			Help:      "Total guest check-ins",
		},
		[]string{"tenant"},
	)

	GuestCheckOutsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "guest",
			Name:      "checkouts_total",
			Help:      "Total guest check-outs",
		},
		[]string{"tenant"},
	)
	// =============================================================================
	// Site Connector metrics (hospitality_connector_*)
	// These metrics track the on-premise site connector agent health
	// =============================================================================

	// ConnectorStatus indicates overall connector health (1=healthy, 0=unhealthy)
	ConnectorStatus = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "hospitality",
			Subsystem: "connector",
			Name:      "status",
			Help:      "Site connector health status (1=healthy, 0=unhealthy)",
		},
		[]string{"connector_id"},
	)

	// ConnectorCloudConnected indicates cloud WebSocket connection status
	ConnectorCloudConnected = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "hospitality",
			Subsystem: "connector",
			Name:      "cloud_connected",
			Help:      "Cloud WebSocket connection status (1=connected, 0=disconnected)",
		},
		[]string{"connector_id"},
	)

	// ConnectorQueueDepth tracks pending events in the local queue
	ConnectorQueueDepth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "hospitality",
			Subsystem: "connector",
			Name:      "queue_depth",
			Help:      "Number of pending events in the connector queue",
		},
		[]string{"connector_id"},
	)

	// ConnectorEventsTotal counts all connector events by type
	ConnectorEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "connector",
			Name:      "events_total",
			Help:      "Total connector events by type (checkin, checkout, etc.)",
		},
		[]string{"connector_id", "event_type"},
	)

	// ConnectorReconnectTotal counts reconnection attempts
	ConnectorReconnectTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "connector",
			Name:      "reconnect_total",
			Help:      "Total reconnection attempts",
		},
		[]string{"connector_id", "target"},
	)

	// =============================================================================
	// WebSocket Bridge Metrics
	// =============================================================================

	// WebSocket connection status (1 = connected, 0 = disconnected)
	WebSocketConnectionStatus = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "hospitality",
			Subsystem: "websocket",
			Name:      "connection_status",
			Help:      "WebSocket connection status (1=connected, 0=disconnected)",
		},
		[]string{"tenant"},
	)

	// WebSocket last connected timestamp
	WebSocketLastConnected = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "hospitality",
			Subsystem: "websocket",
			Name:      "last_connected_timestamp",
			Help:      "Unix timestamp of last successful WebSocket connection",
		},
		[]string{"tenant"},
	)

	// WebSocket reconnect delay (seconds)
	WebSocketReconnectDelay = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "hospitality",
			Subsystem: "websocket",
			Name:      "reconnect_delay_seconds",
			Help:      "Current WebSocket reconnection delay in seconds",
		},
		[]string{"tenant"},
	)

	// WebSocket events sent counter
	WebSocketEventsSent = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "websocket",
			Name:      "events_sent_total",
			Help:      "Total WebSocket events sent to cloud platform",
		},
		[]string{"tenant", "event_type"},
	)

	// WebSocket events received counter (from cloud)
	WebSocketEventsReceived = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "websocket",
			Name:      "events_received_total",
			Help:      "Total WebSocket events received from cloud platform",
		},
		[]string{"tenant"},
	)

	// WebSocket send errors
	WebSocketSendErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "websocket",
			Name:      "send_errors_total",
			Help:      "Total WebSocket send errors",
		},
		[]string{"tenant"},
	)

	// WebSocket reconnection counter
	WebSocketReconnections = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "hospitality",
			Subsystem: "websocket",
			Name:      "reconnections_total",
			Help:      "Total WebSocket reconnection attempts",
		},
		[]string{"tenant"},
	)
)
