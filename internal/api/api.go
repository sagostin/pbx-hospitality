package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/config"
	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/pbx"
	"github.com/sagostin/pbx-hospitality/internal/pms"
	"github.com/sagostin/pbx-hospitality/internal/pms/tigertms"
	"github.com/sagostin/pbx-hospitality/internal/tenant"

	"github.com/sagostin/pbx-hospitality/internal/api/admin"
)

// Server holds API dependencies
type Server struct {
	tm               *tenant.Manager
	cfg              *config.Config
	db               *db.DB              // May be nil if DB not configured
	tigertmsHandlers map[string]http.Handler // tenant ID -> Tigertms HTTP handler
	admin            *admin.AdminServer
}

// NewRouter creates the HTTP router with all endpoints
func NewRouter(tm *tenant.Manager, cfg *config.Config) http.Handler {
	return NewRouterWithDB(tm, cfg, nil)
}

// NewRouterWithDB creates the HTTP router with database support
func NewRouterWithDB(tm *tenant.Manager, cfg *config.Config, database *db.DB) http.Handler {
	s := &Server{
		tm:               tm,
		cfg:              cfg,
		db:               database,
		tigertmsHandlers: make(map[string]http.Handler),
		admin:            admin.NewAdminServer(database),
	}
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Health check
	r.Get("/health", s.health)

	// Prometheus metrics
	r.Handle("/metrics", promhttp.Handler())

	// API routes
	r.Route("/api/v1", func(r chi.Router) {
		// Tenant endpoints
		r.Route("/tenants", func(r chi.Router) {
			r.Get("/", s.listTenants)
			r.Get("/{id}", s.getTenant)
			r.Get("/{id}/status", s.getTenantStatus)

			// Room mappings (requires DB)
			r.Get("/{id}/rooms", s.listRooms)
			r.Post("/{id}/rooms", s.createRoomMapping)

			// Guest sessions (requires DB)
			r.Get("/{id}/sessions", s.listActiveSessions)
			r.Post("/{id}/sessions", s.createSession)
			r.Get("/{id}/sessions/{room}", s.getSession)
			r.Delete("/{id}/sessions/{room}", s.endSession)

			// PMS event history (requires DB)
			r.Get("/{id}/events", s.listEvents)
		})

		// PBX webhook endpoints for receiving inbound call events
		r.Route("/pbx", func(r chi.Router) {
			r.Post("/webhook/{tenant}", s.handlePBXWebhook)
		})

		// Admin API endpoints for managing clients, systems, and sites
		r.Route("/admin", func(r chi.Router) {
			// Clients
			r.Route("/clients", func(r chi.Router) {
				r.Get("/", s.admin.ListClients)
				r.Post("/", s.admin.CreateClient)
				r.Get("/{id}", s.admin.GetClient)
				r.Put("/{id}", s.admin.UpdateClient)
				r.Delete("/{id}", s.admin.DeleteClient)

				// Systems (nested under clients)
				r.Route("/{client_id}/systems", func(r chi.Router) {
					r.Get("/", s.admin.ListSystems)
					r.Post("/", s.admin.CreateSystem)
				})
			})

			// Systems (top-level for GET/PUT/DELETE by ID)
			r.Route("/systems", func(r chi.Router) {
				r.Get("/{id}", s.admin.GetSystem)
				r.Put("/{id}", s.admin.UpdateSystem)
				r.Delete("/{id}", s.admin.DeleteSystem)

				// Sites (nested under systems)
				r.Route("/{system_id}/sites", func(r chi.Router) {
					r.Get("/", s.admin.ListSites)
					r.Post("/", s.admin.CreateSite)
				})
			})

			// Sites (top-level for GET/PUT/DELETE by ID)
			r.Route("/sites", func(r chi.Router) {
				r.Get("/{id}", s.admin.GetSite)
				r.Put("/{id}", s.admin.UpdateSite)
				r.Delete("/{id}", s.admin.DeleteSite)
				r.Post("/{id}/reload", s.admin.ReloadSite)
			})
		})
	})

	// Register TigerTMS HTTP handlers for tenants with tigertms PMS protocol
	for _, tc := range cfg.Tenants {
		if tc.PMS.Protocol == "tigertms" {
			// Create a TigerTMS adapter (host/port are not used for HTTP server, pass 0)
			adapter, err := pms.NewAdapter("tigertms", "", 0, tigertms.WithAuthToken(tc.PMS.AuthToken))
			if err != nil {
				log.Error().Err(err).Str("tenant", tc.ID).Msg("Failed to create TigerTMS adapter for API router")
				continue
			}
			tigerAdapter, ok := adapter.(*tigertms.Adapter)
			if !ok {
				log.Error().Str("tenant", tc.ID).Msg("TigerTMS adapter is wrong type")
				continue
			}
			handler := tigertms.NewHandler(tigerAdapter)
			// Store the chi.Router (which implements http.Handler), not the Handler wrapper
			s.tigertmsHandlers[tc.ID] = handler.Routes()

			log.Info().Str("tenant", tc.ID).Str("path_prefix", tc.PMS.PathPrefix).Msg("TigerTMS HTTP handler registered")
		}
	}

	// TigerTMS HTTP endpoints: /tigertms/{tenant}/API/*
	// Each tenant gets its own subrouter rooted at /tigertms/{tenant}
	r.Route("/tigertms/{tenant}", func(r chi.Router) {
		// All TigerTMS API endpoints (including CDR) are handled by the tigertms.Handler
		r.Post("/API/*", s.handleTigerTMS)
	})

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "application/json")

	status := map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	// Database status
	if s.db != nil {
		if err := s.db.Pool().Ping(r.Context()); err != nil {
			status["database"] = "error"
			status["status"] = "degraded"
		} else {
			status["database"] = "connected"
		}
	} else {
		status["database"] = "not configured"
	}

	// Per-tenant connector status
	tenants := s.tm.List()
	if len(tenants) > 0 {
		tenantStatuses := make(map[string]interface{})
		overallHealthy := true

		for _, id := range tenants {
			if t, ok := s.tm.Get(id); ok {
				ts := t.Status()
				tenantStatus := map[string]interface{}{
					"name":              ts.Name,
					"pms_connected":     ts.PMSConnected,
					"pbx_connected":     ts.PBXConnected,
					"cloud_connected":   ts.PBXConnected, // PBX is the cloud connection
					"queue_depth":        0,              // Would need event queue tracking
					"reconnect_count":   ts.ReconnectCount,
				}
				tenantStatuses[id] = tenantStatus

				// Mark degraded if any connection is down
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

	json.NewEncoder(w).Encode(status)
}

func (s *Server) listTenants(w http.ResponseWriter, r *http.Request) {
	ids := s.tm.List()
	tenants := make([]tenant.TenantStatus, 0, len(ids))

	for _, id := range ids {
		if t, ok := s.tm.Get(id); ok {
			tenants = append(tenants, t.Status())
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tenants)
}

func (s *Server) getTenant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, ok := s.tm.Get(id)
	if !ok {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":   t.ID,
		"name": t.Name,
	})
}

func (s *Server) getTenantStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, ok := s.tm.Get(id)
	if !ok {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(t.Status())
}

// =============================================================================
// Room Mapping Endpoints
// =============================================================================

func (s *Server) listRooms(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.tm.Get(id); !ok {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	rooms, err := s.db.ListRoomMappings(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to list room mappings")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rooms)
}

type createRoomRequest struct {
	RoomNumber string `json:"room_number"`
	Extension  string `json:"extension"`
}

func (s *Server) createRoomMapping(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.tm.Get(id); !ok {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	var req createRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.RoomNumber == "" || req.Extension == "" {
		http.Error(w, "room_number and extension required", http.StatusBadRequest)
		return
	}

	if err := s.db.UpsertRoomMapping(r.Context(), id, req.RoomNumber, req.Extension); err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to create room mapping")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":      "created",
		"room_number": req.RoomNumber,
		"extension":   req.Extension,
	})
}

// =============================================================================
// Guest Session Endpoints
// =============================================================================

func (s *Server) listActiveSessions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.tm.Get(id); !ok {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	sessions, err := s.db.ListActiveSessions(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to list active sessions")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	room := chi.URLParam(r, "room")

	if _, ok := s.tm.Get(id); !ok {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	session, err := s.db.GetActiveSession(r.Context(), id, room)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Str("room", room).Msg("Failed to get session")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if session == nil {
		http.Error(w, "no active session", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

type createSessionRequest struct {
	RoomNumber    string                 `json:"room_number"`
	Extension    string                 `json:"extension"`
	GuestName    string                 `json:"guest_name"`
	ReservationID string                `json:"reservation_id"`
	Metadata     map[string]interface{} `json:"metadata"`
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.tm.Get(id); !ok {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.RoomNumber == "" || req.GuestName == "" {
		http.Error(w, "room_number and guest_name required", http.StatusBadRequest)
		return
	}

	sessionID, err := s.db.CreateGuestSession(r.Context(), id, req.RoomNumber, req.Extension, req.GuestName, req.ReservationID, req.Metadata)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to create session")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":          sessionID,
		"room_number": req.RoomNumber,
		"guest_name":  req.GuestName,
	})
}

func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	room := chi.URLParam(r, "room")

	if _, ok := s.tm.Get(id); !ok {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	if err := s.db.EndGuestSession(r.Context(), id, room); err != nil {
		log.Error().Err(err).Str("tenant", id).Str("room", room).Msg("Failed to end session")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ended",
		"room":   room,
	})
}

// =============================================================================
// PMS Event Endpoints
// =============================================================================

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := s.tm.Get(id); !ok {
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	// Parse limit from query params (default 50)
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	events, err := s.db.GetRecentEvents(r.Context(), id, limit)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to list events")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

// =============================================================================
// PBX Webhook Endpoints
// =============================================================================

// handlePBXWebhook processes incoming webhooks from PBX systems
func (s *Server) handlePBXWebhook(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenant")

	t, ok := s.tm.Get(tenantID)
	if !ok {
		log.Warn().Str("tenant", tenantID).Msg("PBX webhook for unknown tenant")
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	// Get the PBX provider and check if it supports webhooks
	provider := t.PBXProvider()
	if provider == nil {
		log.Error().Str("tenant", tenantID).Msg("Tenant has no PBX provider")
		http.Error(w, "PBX not configured", http.StatusServiceUnavailable)
		return
	}

	webhookProvider, ok := provider.(pbx.WebhookProvider)
	if !ok {
		log.Warn().Str("tenant", tenantID).Msg("PBX webhook received but provider doesn't support webhooks")
		http.Error(w, "webhook not supported for this PBX type", http.StatusBadRequest)
		return
	}

	// Handle the webhook
	if err := webhookProvider.HandleWebhook(r); err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to process PBX webhook")
		http.Error(w, "webhook processing failed", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// =============================================================================
// TigerTMS HTTP Endpoint Handlers
// =============================================================================

// handleTigerTMS routes TigerTMS API requests to the appropriate tenant handler
func (s *Server) handleTigerTMS(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenant")

	handler, ok := s.tigertmsHandlers[tenantID]
	if !ok {
		log.Warn().Str("tenant", tenantID).Msg("TigerTMS request for unknown tenant")
		http.Error(w, "tenant not found", http.StatusNotFound)
		return
	}

	handler.ServeHTTP(w, r)
}
