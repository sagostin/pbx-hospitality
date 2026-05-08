package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	fiberws "github.com/gofiber/websocket/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/config"
	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/pbx"
	"github.com/sagostin/pbx-hospitality/internal/pms"
	"github.com/sagostin/pbx-hospitality/internal/pms/tigertms"
	"github.com/sagostin/pbx-hospitality/internal/tenant"
	"github.com/sagostin/pbx-hospitality/internal/websocket"
)

type Server struct {
	tm               *tenant.Manager
	pbxMgr           *pbx.Manager
	cfg              *config.Config
	db               *db.DB
	tigertmsHandlers map[*fiber.App]string
	logSink          *websocket.LogSink
}

func NewRouter(tm *tenant.Manager, pbxMgr *pbx.Manager, cfg *config.Config) http.Handler {
	return NewRouterWithDB(tm, pbxMgr, cfg, nil)
}

func NewRouterWithDB(tm *tenant.Manager, pbxMgr *pbx.Manager, cfg *config.Config, database *db.DB) http.Handler {
	s := &Server{
		tm:               tm,
		pbxMgr:           pbxMgr,
		cfg:              cfg,
		db:               database,
		tigertmsHandlers: make(map[*fiber.App]string),
	}
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})

	app.Use(recover.New())
	app.Use(requestid.New())
	app.Use(logger.New())

	app.Get("/health", s.health)

	app.Get("/metrics", adaptor.HTTPHandler(promhttp.Handler()))

	if cfg.Logging.WebSocketLogs.Enabled {
		s.logSink = websocket.NewLogSink()
		app.Use("/ws/logs", fiberws.New(s.logSink.HandleWS))
		log.Info().Str("path", cfg.Logging.WebSocketLogs.Path).Msg("WebSocket log sink enabled")
	}

	admin := &AdminServer{Server: s, pbxManager: pbxMgr}
	adminGroup := app.Group("/admin/tenants")
	adminGroup.Use(adminKeyMiddleware(cfg.Server.AdminAPIKey))
	adminGroup.Get("/", admin.listTenants)
	adminGroup.Get("/:id", admin.getTenant)
	adminGroup.Post("/", admin.createTenant)
	adminGroup.Put("/:id", admin.updateTenant)
	adminGroup.Delete("/:id", admin.deleteTenant)
	adminGroup.Post("/import", admin.importTenants)
	adminGroup.Get("/:id/rooms", admin.listTenantRooms)
	adminGroup.Get("/:id/rooms/:room", admin.getTenantRoom)
	adminGroup.Delete("/:id/rooms/:room", admin.deleteTenantRoom)
	adminGroup.Get("/:id/sessions", admin.listTenantSessions)
	adminGroup.Get("/:id/sessions/:room", admin.getTenantSession)
	adminGroup.Delete("/:id/sessions/:room", admin.deleteTenantSession)
	adminGroup.Get("/:id/events", admin.listTenantEvents)
	adminGroup.Delete("/:id/events/:eventID", admin.deleteTenantEvent)
	adminGroup.Post("/:id/events/:eventID/retry", admin.retryTenantEvent)
	adminGroup.Get("/:id/health", admin.getTenantHealth)

	adminSitesGroup := app.Group("/admin/sites")
	adminSitesGroup.Use(adminKeyMiddleware(cfg.Server.AdminAPIKey))
	adminSitesGroup.Get("/", admin.listSites)
	adminSitesGroup.Get("/:id", admin.getSite)
	adminSitesGroup.Post("/", admin.createSite)
	adminSitesGroup.Put("/:id", admin.updateSite)
	adminSitesGroup.Delete("/:id", admin.deleteSite)
	adminSitesGroup.Get("/:id/bicom", admin.listSiteBicomMappings)
	adminSitesGroup.Post("/:id/bicom", admin.addSiteBicomMapping)
	adminSitesGroup.Delete("/:id/bicom/:bicomSystemId", admin.removeSiteBicomMapping)
	adminSitesGroup.Get("/:id/health", admin.getSiteHealth)
	adminSitesGroup.Get("/:id/bicom-systems", admin.listSiteBicomSystems)

	adminBicomGroup := app.Group("/admin/bicom-systems")
	adminBicomGroup.Use(adminKeyMiddleware(cfg.Server.AdminAPIKey))
	adminBicomGroup.Get("/", admin.listBicomSystems)
	adminBicomGroup.Get("/:id", admin.getBicomSystem)
	adminBicomGroup.Post("/", admin.createBicomSystem)
	adminBicomGroup.Put("/:id", admin.updateBicomSystem)
	adminBicomGroup.Delete("/:id", admin.deleteBicomSystem)
	adminBicomGroup.Put("/:id/ari-secret", admin.updateBicomSystemARISecret)

	adminPBXGroup := app.Group("/admin/pbx")
	adminPBXGroup.Use(adminKeyMiddleware(cfg.Server.AdminAPIKey))
	adminPBXGroup.Get("/status", admin.listPBXStatus)
	adminPBXGroup.Post("/reload", admin.reloadAllPBX)
	adminPBXGroup.Post("/:id/reload", admin.reloadPBXSystem)

	apiV1 := app.Group("/api/v1")
	tenants := apiV1.Group("/tenants")
	tenants.Get("/", s.listTenants)
	tenants.Get("/:id", s.getTenant)
	tenants.Get("/:id/status", s.getTenantStatus)
	tenants.Get("/:id/rooms", s.listRooms)
	tenants.Post("/:id/rooms", s.createRoomMapping)
	tenants.Get("/:id/sessions", s.listActiveSessions)
	tenants.Post("/:id/sessions", s.createSession)
	tenants.Get("/:id/sessions/:room", s.getSession)
	tenants.Delete("/:id/sessions/:room", s.endSession)
	tenants.Get("/:id/events", s.listEvents)

	apiV1.Post("/pbx/webhook/:tenant", s.handlePBXWebhook)

	if database != nil {
		tenants, err := database.ListTenants(nil)
		if err == nil {
			for _, t := range tenants {
				if !t.Enabled {
					continue
				}
				pmsCfg := parseJSONMap(t.PMSConfig)
				protocol, _ := pmsCfg["protocol"].(string)
				if protocol == "tigertms" {
					authToken, _ := pmsCfg["auth_token"].(string)
					pathPrefix, _ := pmsCfg["path_prefix"].(string)
					adapter, err := pms.NewAdapter("tigertms", "", 0, tigertms.WithAuthToken(authToken))
					if err != nil {
						log.Error().Err(err).Str("tenant", t.ID).Msg("Failed to create TigerTMS adapter for API router")
						continue
					}
					tigerAdapter, ok := adapter.(*tigertms.Adapter)
					if !ok {
						log.Error().Str("tenant", t.ID).Msg("TigerTMS adapter is wrong type")
						continue
					}
					handler := tigertms.NewHandler(tigerAdapter)
					fiberApp := fiber.New()
					handler.Routes(fiberApp)
					s.tigertmsHandlers[fiberApp] = t.ID

					log.Info().Str("tenant", t.ID).Str("path_prefix", pathPrefix).Msg("TigerTMS HTTP handler registered")
				}
			}
		}
	}

	app.Post("/tigertms/:tenant/API/*", s.handleTigerTMS)

	return adaptor.FiberApp(app)
}

func (s *Server) health(c *fiber.Ctx) error {
	c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Set("Pragma", "no-cache")
	c.Set("Expires", "0")
	c.Set("Content-Type", "application/json")

	status := map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	if s.db != nil {
		sqlDB, err := s.db.DB.DB()
		if err != nil || sqlDB.PingContext(c.Context()) != nil {
			status["database"] = "error"
			status["status"] = "degraded"
		} else {
			status["database"] = "connected"
		}
	} else {
		status["database"] = "not configured"
	}

	tenants := s.tm.List()
	if len(tenants) > 0 {
		tenantStatuses := make(map[string]interface{})
		overallHealthy := true

		for _, id := range tenants {
			if t, ok := s.tm.Get(id); ok {
				ts := t.Status()
				tenantStatus := map[string]interface{}{
					"name":            ts.Name,
					"pms_connected":   ts.PMSConnected,
					"pbx_connected":   ts.PBXConnected,
					"cloud_connected": ts.PBXConnected,
					"queue_depth":     0,
					"reconnect_count": ts.ReconnectCount,
				}
				tenantStatuses[id] = tenantStatus

				if !ts.PMSConnected || !ts.PBXConnected {
					overallHealthy = false
				}
			}
		}

		status["tenants"] = tenantStatuses

		if !overallHealthy && status["status"] == "ok" {
			status["status"] = "degraded"
		}
	}

	return c.JSON(status)
}

func (s *Server) listTenants(c *fiber.Ctx) error {
	ids := s.tm.List()
	tenants := make([]tenant.TenantStatus, 0, len(ids))

	for _, id := range ids {
		if t, ok := s.tm.Get(id); ok {
			tenants = append(tenants, t.Status())
		}
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(tenants)
}

func (s *Server) getTenant(c *fiber.Ctx) error {
	id := c.Params("id")
	t, ok := s.tm.Get(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(map[string]interface{}{
		"id":   t.ID,
		"name": t.Name,
	})
}

func (s *Server) getTenantStatus(c *fiber.Ctx) error {
	id := c.Params("id")
	t, ok := s.tm.Get(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(t.Status())
}

func (s *Server) listRooms(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, ok := s.tm.Get(id); !ok {
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	if s.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("database not configured")
	}

	rooms, err := s.db.ListRoomMappings(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to list room mappings")
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(rooms)
}

type createRoomRequest struct {
	RoomNumber   string `json:"room_number"`
	RoomEnd      string `json:"room_end,omitempty"` // Range end (optional)
	Extension    string `json:"extension"`
	ExtensionEnd string `json:"extension_end,omitempty"` // Range extension end (optional)
	MatchPattern string `json:"match_pattern,omitempty"` // Regex pattern (optional)
}

func (s *Server) createRoomMapping(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, ok := s.tm.Get(id); !ok {
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	if s.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("database not configured")
	}

	var req createRoomRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("invalid request body")
	}

	// Validate based on mapping type
	hasRange := req.RoomEnd != "" || req.ExtensionEnd != ""
	hasPattern := req.MatchPattern != ""

	if hasPattern && (hasRange || req.RoomNumber != "") {
		return c.Status(fiber.StatusBadRequest).SendString("match_pattern cannot be combined with room_number or range")
	}

	if hasPattern {
		// Pattern mapping
		if req.Extension == "" {
			return c.Status(fiber.StatusBadRequest).SendString("extension required for pattern mapping")
		}
		rm := &db.RoomMapping{
			TenantID:     id,
			MatchPattern: req.MatchPattern,
			Extension:    req.Extension,
		}
		if err := s.db.UpsertRoomMappingEntry(c.Context(), rm); err != nil {
			log.Error().Err(err).Str("tenant", id).Msg("Failed to create pattern mapping")
			return c.Status(fiber.StatusInternalServerError).SendString("internal error")
		}
		c.Set("Content-Type", "application/json")
		return c.Status(fiber.StatusCreated).JSON(map[string]interface{}{
			"status":        "created",
			"match_pattern": req.MatchPattern,
			"extension":     req.Extension,
		})
	}

	if hasRange {
		// Range mapping
		if req.RoomNumber == "" || req.RoomEnd == "" || req.Extension == "" || req.ExtensionEnd == "" {
			return c.Status(fiber.StatusBadRequest).SendString("room_number, room_end, extension, and extension_end required for range mapping")
		}
		rm := &db.RoomMapping{
			TenantID:     id,
			RoomNumber:   req.RoomNumber,
			RoomEnd:      req.RoomEnd,
			Extension:    req.Extension,
			ExtensionEnd: req.ExtensionEnd,
		}
		if err := s.db.UpsertRoomMappingEntry(c.Context(), rm); err != nil {
			log.Error().Err(err).Str("tenant", id).Msg("Failed to create range mapping")
			return c.Status(fiber.StatusInternalServerError).SendString("internal error")
		}
		c.Set("Content-Type", "application/json")
		return c.Status(fiber.StatusCreated).JSON(map[string]interface{}{
			"status":        "created",
			"room_number":   req.RoomNumber,
			"room_end":      req.RoomEnd,
			"extension":     req.Extension,
			"extension_end": req.ExtensionEnd,
		})
	}

	// Individual mapping (backward compatible)
	if req.RoomNumber == "" || req.Extension == "" {
		return c.Status(fiber.StatusBadRequest).SendString("room_number and extension required")
	}
	if err := s.db.UpsertRoomMapping(c.Context(), id, req.RoomNumber, req.Extension); err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to create room mapping")
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}

	c.Set("Content-Type", "application/json")
	return c.Status(fiber.StatusCreated).JSON(map[string]string{
		"status":      "created",
		"room_number": req.RoomNumber,
		"extension":   req.Extension,
	})
}

func (s *Server) listActiveSessions(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, ok := s.tm.Get(id); !ok {
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	if s.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("database not configured")
	}

	sessions, err := s.db.ListActiveSessions(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to list active sessions")
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(sessions)
}

func (s *Server) getSession(c *fiber.Ctx) error {
	id := c.Params("id")
	room := c.Params("room")

	if _, ok := s.tm.Get(id); !ok {
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	if s.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("database not configured")
	}

	session, err := s.db.GetActiveSession(c.Context(), id, room)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Str("room", room).Msg("Failed to get session")
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}

	if session == nil {
		return c.Status(fiber.StatusNotFound).SendString("no active session")
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(session)
}

type createSessionRequest struct {
	RoomNumber    string                 `json:"room_number"`
	Extension     string                 `json:"extension"`
	GuestName     string                 `json:"guest_name"`
	ReservationID string                 `json:"reservation_id"`
	Metadata      map[string]interface{} `json:"metadata"`
}

func (s *Server) createSession(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, ok := s.tm.Get(id); !ok {
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	if s.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("database not configured")
	}

	var req createSessionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("invalid request body")
	}

	if req.RoomNumber == "" || req.GuestName == "" {
		return c.Status(fiber.StatusBadRequest).SendString("room_number and guest_name required")
	}

	sessionID, err := s.db.CreateGuestSession(c.Context(), id, req.RoomNumber, req.Extension, req.GuestName, req.ReservationID, req.Metadata)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to create session")
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}

	c.Set("Content-Type", "application/json")
	return c.Status(fiber.StatusCreated).JSON(map[string]interface{}{
		"id":          sessionID,
		"room_number": req.RoomNumber,
		"guest_name":  req.GuestName,
	})
}

func (s *Server) endSession(c *fiber.Ctx) error {
	id := c.Params("id")
	room := c.Params("room")

	if _, ok := s.tm.Get(id); !ok {
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	if s.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("database not configured")
	}

	if err := s.db.EndGuestSession(c.Context(), id, room); err != nil {
		log.Error().Err(err).Str("tenant", id).Str("room", room).Msg("Failed to end session")
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(map[string]string{
		"status": "ended",
		"room":   room,
	})
}

func (s *Server) listEvents(c *fiber.Ctx) error {
	id := c.Params("id")
	if _, ok := s.tm.Get(id); !ok {
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	if s.db == nil {
		return c.Status(fiber.StatusServiceUnavailable).SendString("database not configured")
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	events, err := s.db.GetRecentEvents(c.Context(), id, limit)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to list events")
		return c.Status(fiber.StatusInternalServerError).SendString("internal error")
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(events)
}

func (s *Server) handlePBXWebhook(c *fiber.Ctx) error {
	tenantID := c.Params("tenant")

	t, ok := s.tm.Get(tenantID)
	if !ok {
		log.Warn().Str("tenant", tenantID).Msg("PBX webhook for unknown tenant")
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	provider := t.PBXProvider()
	if provider == nil {
		log.Error().Str("tenant", tenantID).Msg("Tenant has no PBX provider")
		return c.Status(fiber.StatusServiceUnavailable).SendString("PBX not configured")
	}

	webhookProvider, ok := provider.(pbx.WebhookProvider)
	if !ok {
		log.Warn().Str("tenant", tenantID).Msg("PBX webhook received but provider doesn't support webhooks")
		return c.Status(fiber.StatusBadRequest).SendString("webhook not supported for this PBX type")
	}

	freshReq, err := adaptor.ConvertRequest(c, false)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("failed to process request")
	}
	if err := webhookProvider.HandleWebhook(freshReq); err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to process PBX webhook")
		return c.Status(fiber.StatusBadRequest).SendString("webhook processing failed")
	}

	c.Status(fiber.StatusOK)
	return c.SendString(`{"status":"ok"}`)
}

func (s *Server) handleTigerTMS(c *fiber.Ctx) error {
	tenantID := c.Params("tenant")

	var targetApp *fiber.App
	for app, tid := range s.tigertmsHandlers {
		if tid == tenantID {
			targetApp = app
			break
		}
	}
	if targetApp == nil {
		log.Warn().Str("tenant", tenantID).Msg("TigerTMS request for unknown tenant")
		return c.Status(fiber.StatusNotFound).SendString("tenant not found")
	}

	adaptor.CopyContextToFiberContext(c.Context(), c.Context())
	targetApp.Handler()(c.Context())
	return nil
}
