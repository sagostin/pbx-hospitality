package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Valid PMSType values
var validPMSTypes = map[string]bool{
	"tigertms": true,
	"mitel":    true,
	"fias":     true,
}

type systemResponse struct {
	ID              string                 `json:"id"`
	ClientID        string                 `json:"client_id"`
	Name            string                 `json:"name"`
	PMSType         string                 `json:"pms_type"`
	Host            string                 `json:"host,omitempty"`
	Port            int                    `json:"port,omitempty"`
	SerialPort      string                 `json:"serial_port,omitempty"`
	BaudRate        int                    `json:"baud_rate,omitempty"`
	CredentialsJSON map[string]interface{} `json:"credentials_json,omitempty"`
	CreatedAt       string                 `json:"created_at"`
	UpdatedAt       string                 `json:"updated_at"`
}

type systemListItemResponse struct {
	ID        string `json:"id"`
	ClientID  string `json:"client_id"`
	Name      string `json:"name"`
	PMSType   string `json:"pms_type"`
	Host      string `json:"host,omitempty"`
	Port      int    `json:"port,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type createSystemRequest struct {
	Name            string                 `json:"name"`
	PMSType         string                 `json:"pms_type"`
	Host            string                 `json:"host,omitempty"`
	Port            int                    `json:"port,omitempty"`
	SerialPort      string                 `json:"serial_port,omitempty"`
	BaudRate        int                    `json:"baud_rate,omitempty"`
	CredentialsJSON map[string]interface{} `json:"credentials_json,omitempty"`
}

type updateSystemRequest struct {
	Name            string                 `json:"name"`
	PMSType         string                 `json:"pms_type"`
	Host            string                 `json:"host,omitempty"`
	Port            int                    `json:"port,omitempty"`
	SerialPort      string                 `json:"serial_port,omitempty"`
	BaudRate        int                    `json:"baud_rate,omitempty"`
	CredentialsJSON map[string]interface{} `json:"credentials_json,omitempty"`
}

// ListSystems handles GET /api/v1/admin/clients/{client_id}/systems
func (s *AdminServer) ListSystems(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	clientID := chi.URLParam(r, "client_id")
	if clientID == "" {
		http.Error(w, "client_id required", http.StatusBadRequest)
		return
	}

	systems, err := s.db.ListSystemsByClient(r.Context(), clientID)
	if err != nil {
		log.Error().Err(err).Str("client_id", clientID).Msg("Failed to list systems")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	response := make([]systemListItemResponse, 0, len(systems))
	for _, sys := range systems {
		response = append(response, systemListItemResponse{
			ID:        sys.ID,
			ClientID:  sys.ClientID,
			Name:      sys.Name,
			PMSType:   sys.PMSType,
			Host:      sys.Host,
			Port:      sys.Port,
			CreatedAt: sys.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt: sys.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// CreateSystem handles POST /api/v1/admin/clients/{client_id}/systems
func (s *AdminServer) CreateSystem(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	clientID := chi.URLParam(r, "client_id")
	if clientID == "" {
		http.Error(w, "client_id required", http.StatusBadRequest)
		return
	}

	// Verify client exists
	client, err := s.db.GetClient(r.Context(), clientID)
	if err != nil {
		log.Error().Err(err).Str("client_id", clientID).Msg("Failed to get client")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if client == nil {
		http.Error(w, "client not found", http.StatusNotFound)
		return
	}

	var req createSystemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.PMSType == "" {
		http.Error(w, "name and pms_type are required", http.StatusBadRequest)
		return
	}

	if !validPMSTypes[req.PMSType] {
		http.Error(w, "invalid pms_type; must be one of: tigertms, mitel, fias", http.StatusBadRequest)
		return
	}

	id := uuid.New().String()
	if err := s.db.CreateSystem(r.Context(), id, clientID, req.Name, req.PMSType, req.Host, req.Port, req.SerialPort, req.BaudRate, req.CredentialsJSON); err != nil {
		log.Error().Err(err).Msg("Failed to create system")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	system, err := s.db.GetSystem(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get created system")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(systemResponse{
		ID:              system.ID,
		ClientID:        system.ClientID,
		Name:            system.Name,
		PMSType:         system.PMSType,
		Host:            system.Host,
		Port:            system.Port,
		SerialPort:      system.SerialPort,
		BaudRate:        system.BaudRate,
		CredentialsJSON: system.CredentialsJSON,
		CreatedAt:       system.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:       system.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// GetSystem handles GET /api/v1/admin/systems/{id}
func (s *AdminServer) GetSystem(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "system id required", http.StatusBadRequest)
		return
	}

	system, err := s.db.GetSystem(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to get system")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if system == nil {
		http.Error(w, "system not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(systemResponse{
		ID:              system.ID,
		ClientID:        system.ClientID,
		Name:            system.Name,
		PMSType:         system.PMSType,
		Host:            system.Host,
		Port:            system.Port,
		SerialPort:      system.SerialPort,
		BaudRate:        system.BaudRate,
		CredentialsJSON: system.CredentialsJSON,
		CreatedAt:       system.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:       system.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// UpdateSystem handles PUT /api/v1/admin/systems/{id}
func (s *AdminServer) UpdateSystem(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "system id required", http.StatusBadRequest)
		return
	}

	var req updateSystemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.PMSType == "" {
		http.Error(w, "name and pms_type are required", http.StatusBadRequest)
		return
	}

	if !validPMSTypes[req.PMSType] {
		http.Error(w, "invalid pms_type; must be one of: tigertms, mitel, fias", http.StatusBadRequest)
		return
	}

	if err := s.db.UpdateSystem(r.Context(), id, req.Name, req.PMSType, req.Host, req.Port, req.SerialPort, req.BaudRate, req.CredentialsJSON); err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to update system")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	system, err := s.db.GetSystem(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to get updated system")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if system == nil {
		http.Error(w, "system not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(systemResponse{
		ID:              system.ID,
		ClientID:        system.ClientID,
		Name:            system.Name,
		PMSType:         system.PMSType,
		Host:            system.Host,
		Port:            system.Port,
		SerialPort:      system.SerialPort,
		BaudRate:        system.BaudRate,
		CredentialsJSON: system.CredentialsJSON,
		CreatedAt:       system.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:       system.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// DeleteSystem handles DELETE /api/v1/admin/systems/{id}
func (s *AdminServer) DeleteSystem(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "system id required", http.StatusBadRequest)
		return
	}

	if err := s.db.DeleteSystem(r.Context(), id); err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to delete system")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
