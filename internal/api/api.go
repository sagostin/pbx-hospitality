package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/config"
	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/pbx"
	"github.com/sagostin/pbx-hospitality/internal/tenant"
)

// Server holds API dependencies
type Server struct {
	tm  *tenant.Manager
	cfg *config.Config
	db  *db.DB // May be nil if DB not configured
}

// NewRouter creates the HTTP router with all endpoints
func NewRouter(tm *tenant.Manager, cfg *config.Config) http.Handler {
	return NewRouterWithDB(tm, cfg, nil)
}

// NewRouterWithDB creates the HTTP router with database support
func NewRouterWithDB(tm *tenant.Manager, cfg *config.Config, database *db.DB) http.Handler {
	s := &Server{tm: tm, cfg: cfg, db: database}
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
			r.Get("/{id}/sessions/{room}", s.getSession)

			// PMS event history (requires DB)
			r.Get("/{id}/events", s.listEvents)
		})

		// PBX webhook endpoints for receiving inbound call events
		r.Route("/pbx", func(r chi.Router) {
			r.Post("/webhook/{tenant}", s.handlePBXWebhook)
		})
	})

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"status": "ok",
	}

	if s.db != nil {
		if err := s.db.Pool().Ping(r.Context()); err != nil {
			status["database"] = "error"
		} else {
			status["database"] = "connected"
		}
	} else {
		status["database"] = "not configured"
	}

	w.Header().Set("Content-Type", "application/json")
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

	// For now, return a placeholder - would need a ListActiveSessions method
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]interface{}{})
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
