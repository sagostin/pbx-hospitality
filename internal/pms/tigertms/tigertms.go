// Package tigertms implements a PMS adapter for TigerTMS iLink REST API.
// Unlike socket-based adapters (Mitel, FIAS), TigerTMS pushes events to HTTP endpoints.
package tigertms

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/pms"
)

func init() {
	pms.Register("tigertms", NewAdapter)
}

// Adapter implements the PMS adapter for TigerTMS iLink REST API
type Adapter struct {
	host      string
	port      int
	authToken string
	events    chan pms.Event
	mu        sync.RWMutex
	cancel    context.CancelFunc
	connected bool
}

// NewAdapter creates a new TigerTMS protocol adapter
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

// WithAuthToken sets the authentication token
func WithAuthToken(token string) pms.AdapterOption {
	return func(a interface{}) {
		if adapter, ok := a.(*Adapter); ok {
			adapter.authToken = token
		}
	}
}

// Protocol returns the protocol name
func (a *Adapter) Protocol() string {
	return "tigertms"
}

// Connect marks the adapter as ready (no outbound connection needed)
// TigerTMS pushes to us, so we're "connected" once we're listening
func (a *Adapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	_, a.cancel = context.WithCancel(ctx)
	a.connected = true

	log.Info().
		Str("protocol", "tigertms").
		Msg("TigerTMS adapter ready to receive HTTP events")

	return nil
}

// Events returns the event channel
func (a *Adapter) Events() <-chan pms.Event {
	return a.events
}

// SendAck is a no-op for HTTP (acks are done via response)
func (a *Adapter) SendAck() error {
	return nil
}

// SendNak is a no-op for HTTP (errors are done via response)
func (a *Adapter) SendNak() error {
	return nil
}

// Close terminates the adapter
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
	}

	a.connected = false
	close(a.events)

	return nil
}

// Connected returns connection status
func (a *Adapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

// ===========================================================================
// HTTP Handlers for TigerTMS endpoints
// ===========================================================================

// Handler provides HTTP handlers for TigerTMS API endpoints
type Handler struct {
	adapter *Adapter
}

// NewHandler creates a new TigerTMS HTTP handler
func NewHandler(adapter *Adapter) *Handler {
	return &Handler{adapter: adapter}
}

// Routes returns the chi router for TigerTMS endpoints
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	// Authentication middleware
	r.Use(h.authMiddleware)

	// TigerTMS API endpoints
	r.Post("/API/setguest", h.handleSetGuest)
	r.Post("/API/setcos", h.handleSetCOS)
	r.Post("/API/setmw", h.handleSetMW)
	r.Post("/API/setsipdata", h.handleSetSIPData)
	r.Post("/API/setddi", h.handleSetDDI)
	r.Post("/API/setdnd", h.handleSetDND)
	r.Post("/API/setwakeup", h.handleSetWakeup)
	// CDR billing endpoint (outbound from PBX to TigerTMS)
	r.Post("/API/CDR", h.handleCDR)

	return r
}

// authMiddleware validates the authorization header
func (h *Handler) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.adapter.authToken != "" {
			token := r.Header.Get("Authorization")
			if token == "" {
				token = r.URL.Query().Get("token")
			}
			expected := "Bearer " + h.adapter.authToken
			if token != expected && token != h.adapter.authToken {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid authorization")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// handleSetGuest handles guest check-in/check-out
// POST /API/setguest?room=2129&checkin=true&guest=Smith%2C+John
func (h *Handler) handleSetGuest(w http.ResponseWriter, r *http.Request) {
	room := r.FormValue("room")
	if room == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ROOM", "room parameter required")
		return
	}

	checkin := r.FormValue("checkin") == "true" || r.FormValue("checkin") == "1"
	guestName := r.FormValue("guest")

	evt := pms.Event{
		Room:      room,
		GuestName: guestName,
		Timestamp: time.Now(),
		Metadata:  map[string]string{"source": "tigertms"},
	}

	if checkin {
		evt.Type = pms.EventCheckIn
		evt.Status = true
	} else {
		evt.Type = pms.EventCheckOut
		evt.Status = false
	}

	h.sendEvent(evt)

	log.Info().
		Str("room", room).
		Bool("checkin", checkin).
		Str("guest", guestName).
		Msg("TigerTMS guest event")

	writeSuccess(w, "Guest event processed")
}

// handleSetCOS handles Class of Service changes
// POST /API/setcos?room=2129&cos=2
func (h *Handler) handleSetCOS(w http.ResponseWriter, r *http.Request) {
	room := r.FormValue("room")
	if room == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ROOM", "room parameter required")
		return
	}

	cos := r.FormValue("cos")

	evt := pms.Event{
		Type:      pms.EventRoomStatus, // COS is a type of room status
		Room:      room,
		Status:    true,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source":           "tigertms",
			"class_of_service": cos,
		},
	}

	h.sendEvent(evt)

	log.Info().
		Str("room", room).
		Str("cos", cos).
		Msg("TigerTMS COS event")

	writeSuccess(w, "Class of service updated")
}

// handleSetMW handles Message Waiting indicator
// POST /API/setmw?room=2129&mw=true
func (h *Handler) handleSetMW(w http.ResponseWriter, r *http.Request) {
	room := r.FormValue("room")
	if room == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ROOM", "room parameter required")
		return
	}

	mw := r.FormValue("mw") == "true" || r.FormValue("mw") == "1"

	evt := pms.Event{
		Type:      pms.EventMessageWaiting,
		Room:      room,
		Status:    mw,
		Timestamp: time.Now(),
		Metadata:  map[string]string{"source": "tigertms"},
	}

	h.sendEvent(evt)

	log.Info().
		Str("room", room).
		Bool("mw", mw).
		Msg("TigerTMS MWI event")

	writeSuccess(w, "Message waiting updated")
}

// handleSetSIPData handles SIP extension data updates
// POST /API/setsipdata?room=2129&name=Smith%2C+John&callerid=2129
func (h *Handler) handleSetSIPData(w http.ResponseWriter, r *http.Request) {
	room := r.FormValue("room")
	if room == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ROOM", "room parameter required")
		return
	}

	name := r.FormValue("name")
	callerID := r.FormValue("callerid")

	evt := pms.Event{
		Type:      pms.EventNameUpdate,
		Room:      room,
		GuestName: name,
		Status:    true,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source":    "tigertms",
			"caller_id": callerID,
		},
	}

	h.sendEvent(evt)

	log.Info().
		Str("room", room).
		Str("name", name).
		Str("callerid", callerID).
		Msg("TigerTMS SIP data event")

	writeSuccess(w, "SIP data updated")
}

// handleSetDDI handles DDI/DID assignment
// POST /API/setddi?room=2129&ddi=+14165551234
func (h *Handler) handleSetDDI(w http.ResponseWriter, r *http.Request) {
	room := r.FormValue("room")
	if room == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ROOM", "room parameter required")
		return
	}

	ddi := r.FormValue("ddi")

	evt := pms.Event{
		Type:      pms.EventRoomStatus,
		Room:      room,
		Status:    ddi != "",
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source": "tigertms",
			"ddi":    ddi,
		},
	}

	h.sendEvent(evt)

	log.Info().
		Str("room", room).
		Str("ddi", ddi).
		Msg("TigerTMS DDI event")

	writeSuccess(w, "DDI updated")
}

// handleSetDND handles Do Not Disturb
// POST /API/setdnd?room=2129&dnd=true
func (h *Handler) handleSetDND(w http.ResponseWriter, r *http.Request) {
	room := r.FormValue("room")
	if room == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ROOM", "room parameter required")
		return
	}

	dnd := r.FormValue("dnd") == "true" || r.FormValue("dnd") == "1"

	evt := pms.Event{
		Type:      pms.EventDND,
		Room:      room,
		Status:    dnd,
		Timestamp: time.Now(),
		Metadata:  map[string]string{"source": "tigertms"},
	}

	h.sendEvent(evt)

	log.Info().
		Str("room", room).
		Bool("dnd", dnd).
		Msg("TigerTMS DND event")

	writeSuccess(w, "DND updated")
}

// handleSetWakeup handles wake-up call scheduling
// POST /API/setwakeup?room=2129&time=07:00&enabled=true
func (h *Handler) handleSetWakeup(w http.ResponseWriter, r *http.Request) {
	room := r.FormValue("room")
	if room == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ROOM", "room parameter required")
		return
	}

	wakeupTime := r.FormValue("time")
	enabled := r.FormValue("enabled") == "true" || r.FormValue("enabled") == "1"

	evt := pms.Event{
		Type:      pms.EventWakeUp,
		Room:      room,
		Status:    enabled,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source":      "tigertms",
			"wakeup_time": wakeupTime,
		},
	}

	h.sendEvent(evt)

	log.Info().
		Str("room", room).
		Str("time", wakeupTime).
		Bool("enabled", enabled).
		Msg("TigerTMS wakeup event")

	writeSuccess(w, "Wakeup call scheduled")
}

// CDRData represents Call Detail Record data from the PBX
type CDRData struct {
	Src        string `json:"src"`
	Dst        string `json:"dst"`
	Start      string `json:"start"`
	Duration   int    `json:"duration"`
	Billsec    int    `json:"billsec"`
	Disposition string `json:"disposition"`
}

// handleCDR handles Call Detail Record billing data from PBX
// POST /API/CDR
// This endpoint receives CDR data from the PBX and forwards it to TigerTMS
// for billing integration with the hotel PMS.
func (h *Handler) handleCDR(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/json" {
		writeError(w, http.StatusBadRequest, "INVALID_CONTENT_TYPE", "application/json required")
		return
	}

	var cdr CDRData
	if err := json.NewDecoder(r.Body).Decode(&cdr); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "failed to parse CDR data")
		return
	}

	log.Info().
		Str("src", cdr.Src).
		Str("dst", cdr.Dst).
		Str("start", cdr.Start).
		Int("duration", cdr.Duration).
		Int("billsec", cdr.Billsec).
		Str("disposition", cdr.Disposition).
		Msg("TigerTMS CDR received")

	// Emit a CDR event for downstream processing (e.g., posting to external billing)
	evt := pms.Event{
		Type:      pms.EventRoomStatus, // CDR is a billing event
		Room:      cdr.Src,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source":      "tigertms",
			"cdr_dst":     cdr.Dst,
			"cdr_start":   cdr.Start,
			"cdr_duration": fmt.Sprintf("%d", cdr.Duration),
			"cdr_billsec": fmt.Sprintf("%d", cdr.Billsec),
			"cdr_disposition": cdr.Disposition,
		},
	}

	h.sendEvent(evt)

	writeSuccess(w, "CDR processed")
}

// sendEvent sends an event to the adapter's event channel
func (h *Handler) sendEvent(evt pms.Event) {
	select {
	case h.adapter.events <- evt:
	default:
		log.Warn().Msg("TigerTMS event channel full, dropping event")
	}
}

// Response helpers

type apiResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
}

func writeSuccess(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	resp := apiResponse{Success: true, Message: message}
	if err := encodeJSON(w, resp); err != nil {
		log.Error().Err(err).Msg("Failed to encode response")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := apiResponse{Success: false, Error: message, Code: code}
	if err := encodeJSON(w, resp); err != nil {
		log.Error().Err(err).Msg("Failed to encode error response")
	}
}

func encodeJSON(w http.ResponseWriter, v interface{}) error {
	encoder := json.NewEncoder(w)
	return encoder.Encode(v)
}
