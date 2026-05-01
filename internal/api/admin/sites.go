package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/crypto"
)

// Valid PBXType values
var validPBXTypes = map[string]bool{
	"zultys": true,
	"bicom":  true,
}

// siteResponse is used for API responses (secrets redacted)
type siteResponse struct {
	ID             string `json:"id"`
	SystemID       string `json:"system_id"`
	Name           string `json:"name"`
	PBXType        string `json:"pbx_type"`
	ARIURL         string `json:"ari_url,omitempty"`
	ARIWSURL       string `json:"ari_ws_url,omitempty"`
	ARIUser        string `json:"ari_user,omitempty"`
	APIURL         string `json:"api_url,omitempty"`
	APIKey         string `json:"api_key,omitempty"`         // redacted
	WebhookSecret  string `json:"webhook_secret,omitempty"` // redacted
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// siteListItemResponse is used for list responses (secrets redacted)
type siteListItemResponse struct {
	ID        string `json:"id"`
	SystemID  string `json:"system_id"`
	Name      string `json:"name"`
	PBXType   string `json:"pbx_type"`
	ARIURL    string `json:"ari_url,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type createSiteRequest struct {
	Name           string `json:"name"`
	PBXType       string `json:"pbx_type"`
	ARIURL        string `json:"ari_url,omitempty"`
	ARIWSURL      string `json:"ari_ws_url,omitempty"`
	ARIUser       string `json:"ari_user,omitempty"`
	APIURL        string `json:"api_url,omitempty"`
	APIKey        string `json:"api_key,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

type updateSiteRequest struct {
	Name           string `json:"name"`
	PBXType       string `json:"pbx_type"`
	ARIURL        string `json:"ari_url,omitempty"`
	ARIWSURL      string `json:"ari_ws_url,omitempty"`
	ARIUser       string `json:"ari_user,omitempty"`
	APIURL        string `json:"api_url,omitempty"`
	APIKey        string `json:"api_key,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
}

// redactedSecret is the placeholder returned for secrets in API responses
const redactedSecret = "***REDACTED***"

// ListSites handles GET /api/v1/admin/systems/{system_id}/sites
func (s *AdminServer) ListSites(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	systemID := chi.URLParam(r, "system_id")
	if systemID == "" {
		http.Error(w, "system_id required", http.StatusBadRequest)
		return
	}

	sites, err := s.db.ListSitesBySystem(r.Context(), systemID)
	if err != nil {
		log.Error().Err(err).Str("system_id", systemID).Msg("Failed to list sites")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	response := make([]siteListItemResponse, 0, len(sites))
	for _, site := range sites {
		response = append(response, siteListItemResponse{
			ID:        site.ID,
			SystemID:  site.SystemID,
			Name:      site.Name,
			PBXType:   site.PBXType,
			ARIURL:    site.ARIURL,
			CreatedAt: site.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt: site.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// CreateSite handles POST /api/v1/admin/systems/{system_id}/sites
func (s *AdminServer) CreateSite(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	systemID := chi.URLParam(r, "system_id")
	if systemID == "" {
		http.Error(w, "system_id required", http.StatusBadRequest)
		return
	}

	// Verify system exists
	system, err := s.db.GetSystem(r.Context(), systemID)
	if err != nil {
		log.Error().Err(err).Str("system_id", systemID).Msg("Failed to get system")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if system == nil {
		http.Error(w, "system not found", http.StatusNotFound)
		return
	}

	var req createSiteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.PBXType == "" {
		http.Error(w, "name and pbx_type are required", http.StatusBadRequest)
		return
	}

	if !validPBXTypes[req.PBXType] {
		http.Error(w, "invalid pbx_type; must be one of: zultys, bicom", http.StatusBadRequest)
		return
	}

	id := uuid.New().String()
	if err := s.db.CreateSite(r.Context(), id, systemID, req.Name, req.PBXType, req.ARIURL, req.ARIWSURL, req.ARIUser, req.APIURL); err != nil {
		log.Error().Err(err).Msg("Failed to create site")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Store encrypted secrets
	secretsDB := crypto.NewSecretsDB(s.db.Pool())
	if req.APIKey != "" {
		if err := secretsDB.StoreSecret(r.Context(), id, "api_key", req.APIKey); err != nil {
			log.Error().Err(err).Str("site_id", id).Msg("Failed to store api_key")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	if req.WebhookSecret != "" {
		if err := secretsDB.StoreSecret(r.Context(), id, "webhook_secret", req.WebhookSecret); err != nil {
			log.Error().Err(err).Str("site_id", id).Msg("Failed to store webhook_secret")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(siteResponse{
		ID:            id,
		SystemID:      systemID,
		Name:          req.Name,
		PBXType:       req.PBXType,
		ARIURL:        req.ARIURL,
		ARIWSURL:      req.ARIWSURL,
		ARIUser:       req.ARIUser,
		APIURL:        req.APIURL,
		APIKey:        redactedSecret,
		WebhookSecret: redactedSecret,
		CreatedAt:     "",
		UpdatedAt:     "",
	})
}

// GetSite handles GET /api/v1/admin/sites/{id}
// Returns secrets as "***REDACTED***"
func (s *AdminServer) GetSite(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "site id required", http.StatusBadRequest)
		return
	}

	site, err := s.db.GetSite(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to get site")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if site == nil {
		http.Error(w, "site not found", http.StatusNotFound)
		return
	}

	// Check if secrets exist (they will be redacted)
	secretsDB := crypto.NewSecretsDB(s.db.Pool())
	hasAPIKey := false
	hasWebhookSecret := false

	if _, err := secretsDB.GetSecret(r.Context(), id, "api_key"); err == nil {
		hasAPIKey = true
	}
	if _, err := secretsDB.GetSecret(r.Context(), id, "webhook_secret"); err == nil {
		hasWebhookSecret = true
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(siteResponse{
		ID:             site.ID,
		SystemID:       site.SystemID,
		Name:           site.Name,
		PBXType:        site.PBXType,
		ARIURL:         site.ARIURL,
		ARIWSURL:       site.ARIWSURL,
		ARIUser:        site.ARIUser,
		APIURL:         site.APIURL,
		APIKey:         redactedSecret,
		WebhookSecret:  redactedSecret,
		CreatedAt:      site.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:      site.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	})

	_ = hasAPIKey
	_ = hasWebhookSecret
}

// UpdateSite handles PUT /api/v1/admin/sites/{id}
// Updates site fields and re-encrypts secrets if provided
func (s *AdminServer) UpdateSite(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "site id required", http.StatusBadRequest)
		return
	}

	// Verify site exists
	site, err := s.db.GetSite(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to get site")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if site == nil {
		http.Error(w, "site not found", http.StatusNotFound)
		return
	}

	var req updateSiteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" || req.PBXType == "" {
		http.Error(w, "name and pbx_type are required", http.StatusBadRequest)
		return
	}

	if !validPBXTypes[req.PBXType] {
		http.Error(w, "invalid pbx_type; must be one of: zultys, bicom", http.StatusBadRequest)
		return
	}

	// Update site fields (secrets handled separately)
	if err := s.db.UpdateSite(r.Context(), id, req.Name, req.PBXType, req.ARIURL, req.ARIWSURL, req.ARIUser, req.APIURL); err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to update site")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Update secrets if provided
	secretsDB := crypto.NewSecretsDB(s.db.Pool())
	if req.APIKey != "" {
		if err := secretsDB.StoreSecret(r.Context(), id, "api_key", req.APIKey); err != nil {
			log.Error().Err(err).Str("site_id", id).Msg("Failed to store api_key")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	if req.WebhookSecret != "" {
		if err := secretsDB.StoreSecret(r.Context(), id, "webhook_secret", req.WebhookSecret); err != nil {
			log.Error().Err(err).Str("site_id", id).Msg("Failed to store webhook_secret")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(siteResponse{
		ID:             id,
		SystemID:       site.SystemID,
		Name:           req.Name,
		PBXType:        req.PBXType,
		ARIURL:         req.ARIURL,
		ARIWSURL:       req.ARIWSURL,
		ARIUser:        req.ARIUser,
		APIURL:         req.APIURL,
		APIKey:         redactedSecret,
		WebhookSecret:  redactedSecret,
		CreatedAt:      site.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:      "",
	})
}

// DeleteSite handles DELETE /api/v1/admin/sites/{id}
// Cascade deletes extensions and secrets
func (s *AdminServer) DeleteSite(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "site id required", http.StatusBadRequest)
		return
	}

	// Delete secrets first
	secretsDB := crypto.NewSecretsDB(s.db.Pool())
	_ = secretsDB.DeleteSecret(r.Context(), id, "api_key")
	_ = secretsDB.DeleteSecret(r.Context(), id, "webhook_secret")

	// Delete site (cascades to extensions)
	if err := s.db.DeleteSite(r.Context(), id); err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to delete site")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ReloadSite handles POST /api/v1/admin/sites/{id}/reload
// Triggers site reload (SIGHUP equivalent)
func (s *AdminServer) ReloadSite(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "site id required", http.StatusBadRequest)
		return
	}

	site, err := s.db.GetSite(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("id", id).Msg("Failed to get site")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if site == nil {
		http.Error(w, "site not found", http.StatusNotFound)
		return
	}

	// TODO: Implement actual site reload via tenant manager
	// For now, this is a placeholder that confirms the site exists
	log.Info().Str("site_id", id).Msg("Site reload requested")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "reload_triggered",
		"site_id": id,
	})
}
