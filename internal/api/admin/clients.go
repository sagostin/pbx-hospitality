package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/db"
)

// AdminServer holds admin API dependencies
type AdminServer struct {
	db *db.DB
}

// NewAdminServer creates a new admin API server
func NewAdminServer(database *db.DB) *AdminServer {
	return &AdminServer{db: database}
}

// =============================================================================
// Client Handlers
// =============================================================================

type clientResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Region       string `json:"region"`
	ContactEmail string `json:"contact_email"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type createClientRequest struct {
	Name         string `json:"name"`
	Region       string `json:"region"`
	ContactEmail string `json:"contact_email"`
}

type updateClientRequest struct {
	Name         string `json:"name"`
	Region       string `json:"region"`
	ContactEmail string `json:"contact_email"`
}

type clientListResponse struct {
	Data   []clientResponse `json:"data"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// ListClients handles GET /api/v1/admin/clients
func (s *AdminServer) ListClients(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	limit := 20
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	clients, total, err := s.db.ListClients(r.Context(), limit, offset)
	if err != nil {
		log.Error().Err(err).Msg("Failed to list clients")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	response := clientListResponse{
		Data:   make([]clientResponse, 0, len(clients)),
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}
	for _, c := range clients {
		response.Data = append(response.Data, clientResponse{
			ID:           c.ID,
			Name:         c.Name,
			Region:       c.Region,
			ContactEmail: c.ContactEmail,
			CreatedAt:    c.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt:    c.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// CreateClient handles POST /api/v1/admin/clients
func (s *AdminServer) CreateClient(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	var req createClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Region == "" || req.ContactEmail == "" {
		http.Error(w, "name, region, and contact_email are required", http.StatusBadRequest)
		return
	}

	id := uuid.New().String()
	if err := s.db.CreateClient(r.Context(), id, req.Name, req.Region, req.ContactEmail); err != nil {
		log.Error().Err(err).Msg("Failed to create client")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	client, err := s.db.GetClient(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get created client")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clientResponse{
		ID:           client.ID,
		Name:         client.Name,
		Region:       client.Region,
		ContactEmail: client.ContactEmail,
		CreatedAt:    client.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    client.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// GetClient handles GET /api/v1/admin/clients/{id}
func (s *AdminServer) GetClient(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "client id required", http.StatusBadRequest)
		return
	}

	client, err := s.db.GetClient(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to get client")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if client == nil {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clientResponse{
		ID:           client.ID,
		Name:         client.Name,
		Region:       client.Region,
		ContactEmail: client.ContactEmail,
		CreatedAt:    client.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    client.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// UpdateClient handles PUT /api/v1/admin/clients/{id}
func (s *AdminServer) UpdateClient(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "client id required", http.StatusBadRequest)
		return
	}

	var req updateClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.Region == "" || req.ContactEmail == "" {
		http.Error(w, "name, region, and contact_email are required", http.StatusBadRequest)
		return
	}

	if err := s.db.UpdateClient(r.Context(), id, req.Name, req.Region, req.ContactEmail); err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to update client")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	client, err := s.db.GetClient(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to get updated client")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if client == nil {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clientResponse{
		ID:           client.ID,
		Name:         client.Name,
		Region:       client.Region,
		ContactEmail: client.ContactEmail,
		CreatedAt:    client.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    client.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// DeleteClient handles DELETE /api/v1/admin/clients/{id}
func (s *AdminServer) DeleteClient(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "client id required", http.StatusBadRequest)
		return
	}

	if err := s.db.DeleteClient(r.Context(), id); err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to delete client")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
