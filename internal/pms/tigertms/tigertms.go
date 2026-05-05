package tigertms

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/pms"
)

func init() {
	pms.Register("tigertms", NewAdapter)
}

type Adapter struct {
	host       string
	port       int
	authToken  string
	events     chan pms.Event
	mu         sync.RWMutex
	cancel     context.CancelFunc
	connected  bool
}

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

func WithAuthToken(token string) pms.AdapterOption {
	return func(a interface{}) {
		if adapter, ok := a.(*Adapter); ok {
			adapter.authToken = token
		}
	}
}

func (a *Adapter) Protocol() string {
	return "tigertms"
}

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

func (a *Adapter) Events() <-chan pms.Event {
	return a.events
}

func (a *Adapter) SendAck() error {
	return nil
}

func (a *Adapter) SendNak() error {
	return nil
}

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

func (a *Adapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

type Handler struct {
	adapter *Adapter
}

func NewHandler(adapter *Adapter) *Handler {
	return &Handler{adapter: adapter}
}

func (h *Handler) Routes(app *fiber.App) {
	app.Post("/API/setguest", h.handleSetGuest)
	app.Post("/API/setcos", h.handleSetCOS)
	app.Post("/API/setmw", h.handleSetMW)
	app.Post("/API/setsipdata", h.handleSetSIPData)
	app.Post("/API/setddi", h.handleSetDDI)
	app.Post("/API/setdnd", h.handleSetDND)
	app.Post("/API/setwakeup", h.handleSetWakeup)
	app.Post("/API/CDR", h.handleCDR)
}

func (h *Handler) authMiddleware(c *fiber.Ctx) error {
	if h.adapter.authToken != "" {
		token := c.Get("Authorization")
		if token == "" {
			token = c.Query("token")
		}
		expected := "Bearer " + h.adapter.authToken
		if token != expected && token != h.adapter.authToken {
			return writeError(c, fiber.StatusUnauthorized, "UNAUTHORIZED", "Invalid authorization")
		}
	}
	return c.Next()
}

func (h *Handler) handleSetGuest(c *fiber.Ctx) error {
	room := c.FormValue("room")
	if room == "" {
		return writeError(c, fiber.StatusBadRequest, "MISSING_ROOM", "room parameter required")
	}

	checkin := c.FormValue("checkin") == "true" || c.FormValue("checkin") == "1"
	guestName := c.FormValue("guest")

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

	return writeSuccess(c, "Guest event processed")
}

func (h *Handler) handleSetCOS(c *fiber.Ctx) error {
	room := c.FormValue("room")
	if room == "" {
		return writeError(c, fiber.StatusBadRequest, "MISSING_ROOM", "room parameter required")
	}

	cos := c.FormValue("cos")

	evt := pms.Event{
		Type:      pms.EventRoomStatus,
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

	return writeSuccess(c, "Class of service updated")
}

func (h *Handler) handleSetMW(c *fiber.Ctx) error {
	room := c.FormValue("room")
	if room == "" {
		return writeError(c, fiber.StatusBadRequest, "MISSING_ROOM", "room parameter required")
	}

	mw := c.FormValue("mw") == "true" || c.FormValue("mw") == "1"

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

	return writeSuccess(c, "Message waiting updated")
}

func (h *Handler) handleSetSIPData(c *fiber.Ctx) error {
	room := c.FormValue("room")
	if room == "" {
		return writeError(c, fiber.StatusBadRequest, "MISSING_ROOM", "room parameter required")
	}

	name := c.FormValue("name")
	callerID := c.FormValue("callerid")

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

	return writeSuccess(c, "SIP data updated")
}

func (h *Handler) handleSetDDI(c *fiber.Ctx) error {
	room := c.FormValue("room")
	if room == "" {
		return writeError(c, fiber.StatusBadRequest, "MISSING_ROOM", "room parameter required")
	}

	ddi := c.FormValue("ddi")

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

	return writeSuccess(c, "DDI updated")
}

func (h *Handler) handleSetDND(c *fiber.Ctx) error {
	room := c.FormValue("room")
	if room == "" {
		return writeError(c, fiber.StatusBadRequest, "MISSING_ROOM", "room parameter required")
	}

	dnd := c.FormValue("dnd") == "true" || c.FormValue("dnd") == "1"

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

	return writeSuccess(c, "DND updated")
}

func (h *Handler) handleSetWakeup(c *fiber.Ctx) error {
	room := c.FormValue("room")
	if room == "" {
		return writeError(c, fiber.StatusBadRequest, "MISSING_ROOM", "room parameter required")
	}

	wakeupTime := c.FormValue("time")
	enabled := c.FormValue("enabled") == "true" || c.FormValue("enabled") == "1"

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

	return writeSuccess(c, "Wakeup call scheduled")
}

type CDRData struct {
	Src        string `json:"src"`
	Dst        string `json:"dst"`
	Start      string `json:"start"`
	Duration   int    `json:"duration"`
	Billsec    int    `json:"billsec"`
	Disposition string `json:"disposition"`
}

func (h *Handler) handleCDR(c *fiber.Ctx) error {
	if c.Get("Content-Type") != "application/json" {
		return writeError(c, fiber.StatusBadRequest, "INVALID_CONTENT_TYPE", "application/json required")
	}

	var cdr CDRData
	if err := c.BodyParser(&cdr); err != nil {
		return writeError(c, fiber.StatusBadRequest, "INVALID_JSON", "failed to parse CDR data")
	}

	log.Info().
		Str("src", cdr.Src).
		Str("dst", cdr.Dst).
		Str("start", cdr.Start).
		Int("duration", cdr.Duration).
		Int("billsec", cdr.Billsec).
		Str("disposition", cdr.Disposition).
		Msg("TigerTMS CDR received")

	evt := pms.Event{
		Type:      pms.EventRoomStatus,
		Room:      cdr.Src,
		Timestamp: time.Now(),
		Metadata: map[string]string{
			"source":         "tigertms",
			"cdr_dst":        cdr.Dst,
			"cdr_start":      cdr.Start,
			"cdr_duration":   fmt.Sprintf("%d", cdr.Duration),
			"cdr_billsec":    fmt.Sprintf("%d", cdr.Billsec),
			"cdr_disposition": cdr.Disposition,
		},
	}

	h.sendEvent(evt)

	return writeSuccess(c, "CDR processed")
}

func (h *Handler) sendEvent(evt pms.Event) {
	select {
	case h.adapter.events <- evt:
	default:
		log.Warn().Msg("TigerTMS event channel full, dropping event")
	}
}

type apiResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
}

func writeSuccess(c *fiber.Ctx, message string) error {
	c.Set("Content-Type", "application/json")
	return c.Status(fiber.StatusOK).JSON(apiResponse{Success: true, Message: message})
}

func writeError(c *fiber.Ctx, status int, code, message string) error {
	c.Set("Content-Type", "application/json")
	return c.Status(status).JSON(apiResponse{Success: false, Error: message, Code: code})
}