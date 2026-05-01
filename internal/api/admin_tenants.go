package api

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/config"
	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/tenant"
)

// AdminServer wraps the API Server for admin endpoints
type AdminServer struct {
	*Server
}

// adminKeyMiddleware validates the X-Admin-Key header
func adminKeyMiddleware(adminKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if adminKey == "" {
				// Admin API disabled if no key configured
				writeError(w, "admin API not configured", "ADMIN_NOT_CONFIGURED", http.StatusServiceUnavailable)
				return
			}
			key := r.Header.Get("X-Admin-Key")
			if key == "" || key != adminKey {
				writeError(w, "invalid or missing X-Admin-Key header", "UNAUTHORIZED", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeError(w http.ResponseWriter, message, code string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message, "code": code})
}

// =============================================================================
// Tenant CRUD Handlers
// =============================================================================

type tenantResponse struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	PMSConfig map[string]interface{} `json:"pms_config"`
	PBXConfig map[string]interface{} `json:"pbx_config"`
	Settings  map[string]interface{} `json:"settings"`
	Enabled   bool                   `json:"enabled"`
	CreatedAt string                `json:"created_at,omitempty"`
	UpdatedAt string                `json:"updated_at,omitempty"`
}

type createTenantRequest struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	PMSConfig map[string]interface{} `json:"pms_config"`
	PBXConfig map[string]interface{} `json:"pbx_config"`
	Settings  map[string]interface{} `json:"settings"`
	Enabled   bool                   `json:"enabled"`
}

type updateTenantRequest struct {
	Name      *string                `json:"name,omitempty"`
	PMSConfig map[string]interface{} `json:"pms_config,omitempty"`
	PBXConfig map[string]interface{} `json:"pbx_config,omitempty"`
	Settings  map[string]interface{} `json:"settings,omitempty"`
	Enabled   *bool                  `json:"enabled,omitempty"`
}

// validateTenantID validates tenant ID format: alphanumeric with dashes, max 64 chars
var tenantIDRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

func validateTenantID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	return tenantIDRegex.MatchString(id)
}

func (s *AdminServer) listTenants(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, "database not configured", "DB_NOT_CONFIGURED", http.StatusServiceUnavailable)
		return
	}

	tenants, err := s.db.ListTenants(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("Failed to list tenants")
		writeError(w, "failed to list tenants", "INTERNAL_ERROR", http.StatusInternalServerError)
		return
	}

	result := make([]tenantResponse, 0, len(tenants))
	for _, t := range tenants {
		result = append(result, toTenantResponse(t))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *AdminServer) getTenant(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, "database not configured", "DB_NOT_CONFIGURED", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if !validateTenantID(id) {
		writeError(w, "invalid tenant ID format", "INVALID_ID", http.StatusBadRequest)
		return
	}

	t, err := s.db.GetTenant(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to get tenant")
		writeError(w, "failed to get tenant", "INTERNAL_ERROR", http.StatusInternalServerError)
		return
	}
	if t == nil {
		writeError(w, "tenant not found", "NOT_FOUND", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toTenantResponse(*t))
}

func (s *AdminServer) createTenant(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, "database not configured", "DB_NOT_CONFIGURED", http.StatusServiceUnavailable)
		return
	}

	var req createTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", "INVALID_BODY", http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.ID == "" {
		writeError(w, "id is required", "VALIDATION_ERROR", http.StatusBadRequest)
		return
	}
	if !validateTenantID(req.ID) {
		writeError(w, "id must be alphanumeric with dashes, max 64 chars", "VALIDATION_ERROR", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		writeError(w, "name is required", "VALIDATION_ERROR", http.StatusBadRequest)
		return
	}
	if len(req.Name) > 255 {
		writeError(w, "name must be max 255 chars", "VALIDATION_ERROR", http.StatusBadRequest)
		return
	}
	if err := validatePMSConfig(req.PMSConfig); err != nil {
		writeError(w, err.Error(), "VALIDATION_ERROR", http.StatusBadRequest)
		return
	}
	if err := validatePBXConfig(req.PBXConfig); err != nil {
		writeError(w, err.Error(), "VALIDATION_ERROR", http.StatusBadRequest)
		return
	}

	// Check if already exists
	existing, err := s.db.GetTenant(r.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Str("tenant", req.ID).Msg("Failed to check existing tenant")
		writeError(w, "failed to create tenant", "INTERNAL_ERROR", http.StatusInternalServerError)
		return
	}
	if existing != nil {
		writeError(w, "tenant already exists", "ALREADY_EXISTS", http.StatusConflict)
		return
	}

	t := &db.Tenant{
		ID:        req.ID,
		Name:      req.Name,
		PMSConfig: req.PMSConfig,
		PBXConfig: req.PBXConfig,
		Settings:  req.Settings,
		Enabled:   req.Enabled,
	}
	if err := s.db.CreateTenant(r.Context(), t); err != nil {
		log.Error().Err(err).Str("tenant", req.ID).Msg("Failed to create tenant")
		writeError(w, "failed to create tenant", "INTERNAL_ERROR", http.StatusInternalServerError)
		return
	}

	// Reload tenant manager cache to pick up the new tenant
	if err := s.tm.ReloadFromDB(); err != nil {
		log.Warn().Err(err).Str("tenant", req.ID).Msg("Failed to reload tenant manager after create")
	}

	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	created, _ := s.db.GetTenant(r.Context(), req.ID)
	if created != nil {
		json.NewEncoder(w).Encode(toTenantResponse(*created))
	}
}

func (s *AdminServer) updateTenant(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, "database not configured", "DB_NOT_CONFIGURED", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if !validateTenantID(id) {
		writeError(w, "invalid tenant ID format", "INVALID_ID", http.StatusBadRequest)
		return
	}

	existing, err := s.db.GetTenant(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to get tenant for update")
		writeError(w, "failed to update tenant", "INTERNAL_ERROR", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		writeError(w, "tenant not found", "NOT_FOUND", http.StatusNotFound)
		return
	}

	var req updateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", "INVALID_BODY", http.StatusBadRequest)
		return
	}

	// Apply partial updates
	if req.Name != nil {
		if *req.Name == "" {
			writeError(w, "name cannot be empty", "VALIDATION_ERROR", http.StatusBadRequest)
			return
		}
		if len(*req.Name) > 255 {
			writeError(w, "name must be max 255 chars", "VALIDATION_ERROR", http.StatusBadRequest)
			return
		}
		existing.Name = *req.Name
	}
	if req.PMSConfig != nil {
		if err := validatePMSConfig(req.PMSConfig); err != nil {
			writeError(w, err.Error(), "VALIDATION_ERROR", http.StatusBadRequest)
			return
		}
		existing.PMSConfig = req.PMSConfig
	}
	if req.PBXConfig != nil {
		if err := validatePBXConfig(req.PBXConfig); err != nil {
			writeError(w, err.Error(), "VALIDATION_ERROR", http.StatusBadRequest)
			return
		}
		existing.PBXConfig = req.PBXConfig
	}
	if req.Settings != nil {
		existing.Settings = req.Settings
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := s.db.UpdateTenant(r.Context(), existing); err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to update tenant")
		writeError(w, "failed to update tenant", "INTERNAL_ERROR", http.StatusInternalServerError)
		return
	}

	// Reload tenant manager to apply changes
	if err := s.tm.ReloadFromDB(); err != nil {
		log.Warn().Err(err).Str("tenant", id).Msg("Failed to reload tenant manager after update")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toTenantResponse(*existing))
}

func (s *AdminServer) deleteTenant(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, "database not configured", "DB_NOT_CONFIGURED", http.StatusServiceUnavailable)
		return
	}

	id := chi.URLParam(r, "id")
	if !validateTenantID(id) {
		writeError(w, "invalid tenant ID format", "INVALID_ID", http.StatusBadRequest)
		return
	}

	existing, err := s.db.GetTenant(r.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to get tenant for delete")
		writeError(w, "failed to delete tenant", "INTERNAL_ERROR", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		writeError(w, "tenant not found", "NOT_FOUND", http.StatusNotFound)
		return
	}

	if err := s.db.DeleteTenant(r.Context(), id); err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to delete tenant")
		writeError(w, "failed to delete tenant", "INTERNAL_ERROR", http.StatusInternalServerError)
		return
	}

	// Invalidate cache so tenant manager drops in-memory state
	s.tm.InvalidateCache(id)

	w.WriteHeader(http.StatusNoContent)
}

// =============================================================================
// Import Handler
// =============================================================================

type importRequest struct {
	Tenants []createTenantRequest `json:"tenants"`
}

func (s *AdminServer) importTenants(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, "database not configured", "DB_NOT_CONFIGURED", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, "failed to read request body", "INVALID_BODY", http.StatusBadRequest)
		return
	}

	// Try YAML first
	var yamlReq struct {
		Tenants []createTenantRequest `json:"tenants" yaml:"tenants"`
	}
	if err := json.Unmarshal(body, &yamlReq); err != nil {
		// Not JSON, try YAML
		writeError(w, "request must be JSON or YAML with a 'tenants' array", "INVALID_FORMAT", http.StatusBadRequest)
		return
	}

	if len(yamlReq.Tenants) == 0 {
		writeError(w, "no tenants provided", "VALIDATION_ERROR", http.StatusBadRequest)
		return
	}

	created := 0
	errors := []string{}
	for _, req := range yamlReq.Tenants {
		// Validate
		if req.ID == "" || !validateTenantID(req.ID) {
			errors = append(errors, "tenant with empty/invalid ID: "+req.ID)
			continue
		}
		if req.Name == "" {
			errors = append(errors, "tenant '"+req.ID+"': name is required")
			continue
		}
		if err := validatePMSConfig(req.PMSConfig); err != nil {
			errors = append(errors, "tenant '"+req.ID+"': "+err.Error())
			continue
		}
		if err := validatePBXConfig(req.PBXConfig); err != nil {
			errors = append(errors, "tenant '"+req.ID+"': "+err.Error())
			continue
		}

		// Check if exists
		existing, _ := s.db.GetTenant(r.Context(), req.ID)
		if existing != nil {
			errors = append(errors, "tenant '"+req.ID+"': already exists")
			continue
		}

		t := &db.Tenant{
			ID:        req.ID,
			Name:      req.Name,
			PMSConfig: req.PMSConfig,
			PBXConfig: req.PBXConfig,
			Settings:  req.Settings,
			Enabled:   req.Enabled,
		}
		if err := s.db.CreateTenant(r.Context(), t); err != nil {
			errors = append(errors, "tenant '"+req.ID+"': failed to create: "+err.Error())
			continue
		}
		created++
	}

	// Reload manager
	if err := s.tm.ReloadFromDB(); err != nil {
		log.Warn().Err(err).Msg("Failed to reload tenant manager after import")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"created": created,
		"errors":  errors,
	})
}

// =============================================================================
// Validation Helpers
// =============================================================================

func validatePMSConfig(cfg map[string]interface{}) error {
	if cfg == nil {
		return nil
	}
	protocol, ok := cfg["protocol"].(string)
	if !ok || protocol == "" {
		return nil // PMS config is optional
	}
	validProtocols := map[string]bool{"mitel": true, "fias": true, "tigertms": true}
	if !validProtocols[protocol] {
		return &validationError{Field: "pms_config.protocol", Message: "must be one of mitel, fias, tigertms"}
	}
	return nil
}

func validatePBXConfig(cfg map[string]interface{}) error {
	if cfg == nil {
		return nil
	}
	pbxType, ok := cfg["type"].(string)
	if !ok || pbxType == "" {
		return nil // PBX config is optional
	}
	validTypes := map[string]bool{"bicom": true, "zultys": true, "freeswitch": true}
	if !validTypes[pbxType] {
		return &validationError{Field: "pbx_config.type", Message: "must be one of bicom, zultys, freeswitch"}
	}
	return nil
}

type validationError struct {
	Field   string
	Message string
}

func (e *validationError) Error() string {
	return e.Field + " " + e.Message
}

func toTenantResponse(t db.Tenant) tenantResponse {
	return tenantResponse{
		ID:        t.ID,
		Name:      t.Name,
		PMSConfig: t.PMSConfig,
		PBXConfig: t.PBXConfig,
		Settings:  t.Settings,
		Enabled:   t.Enabled,
		CreatedAt: t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: t.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}
