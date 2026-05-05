package api

import (
	"encoding/json"
	"io"
	"regexp"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/config"
	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/tenant"
)

type AdminServer struct {
	*Server
}

func adminKeyMiddleware(adminKey string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if adminKey == "" {
			return writeError(c, "admin API not configured", "ADMIN_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
		}
		key := c.Get("X-Admin-Key")
		if key == "" || key != adminKey {
			return writeError(c, "invalid or missing X-Admin-Key header", "UNAUTHORIZED", fiber.StatusUnauthorized)
		}
		return c.Next()
	}
}

func writeError(c *fiber.Ctx, message, code string, status int) error {
	c.Set("Content-Type", "application/json")
	return c.Status(status).JSON(map[string]string{"error": message, "code": code})
}

type tenantResponse struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	PMSConfig map[string]interface{} `json:"pms_config"`
	PBXConfig map[string]interface{} `json:"pbx_config"`
	Settings  map[string]interface{} `json:"settings"`
	Enabled   bool                   `json:"enabled"`
	CreatedAt string                 `json:"created_at,omitempty"`
	UpdatedAt string                 `json:"updated_at,omitempty"`
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

var tenantIDRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

func validateTenantID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	return tenantIDRegex.MatchString(id)
}

func (s *AdminServer) listTenants(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenants, err := s.db.ListTenants(c.Context())
	if err != nil {
		log.Error().Err(err).Msg("Failed to list tenants")
		return writeError(c, "failed to list tenants", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	result := make([]tenantResponse, 0, len(tenants))
	for _, t := range tenants {
		result = append(result, toTenantResponse(t))
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(result)
}

func (s *AdminServer) getTenant(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	id := c.Params("id")
	if !validateTenantID(id) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	t, err := s.db.GetTenant(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to get tenant")
		return writeError(c, "failed to get tenant", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if t == nil {
		return writeError(c, "tenant not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(toTenantResponse(*t))
}

func (s *AdminServer) createTenant(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	var req createTenantRequest
	if err := c.BodyParser(&req); err != nil {
		return writeError(c, "invalid request body", "INVALID_BODY", fiber.StatusBadRequest)
	}

	if req.ID == "" {
		return writeError(c, "id is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if !validateTenantID(req.ID) {
		return writeError(c, "id must be alphanumeric with dashes, max 64 chars", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if req.Name == "" {
		return writeError(c, "name is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if len(req.Name) > 255 {
		return writeError(c, "name must be max 255 chars", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if err := validatePMSConfig(req.PMSConfig); err != nil {
		return writeError(c, err.Error(), "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if err := validatePBXConfig(req.PBXConfig); err != nil {
		return writeError(c, err.Error(), "VALIDATION_ERROR", fiber.StatusBadRequest)
	}

	existing, err := s.db.GetTenant(c.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Str("tenant", req.ID).Msg("Failed to check existing tenant")
		return writeError(c, "failed to create tenant", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if existing != nil {
		return writeError(c, "tenant already exists", "ALREADY_EXISTS", fiber.StatusConflict)
	}

	t := &db.Tenant{
		ID:        req.ID,
		Name:      req.Name,
		PMSConfig: req.PMSConfig,
		PBXConfig: req.PBXConfig,
		Settings:  req.Settings,
		Enabled:   req.Enabled,
	}
	if err := s.db.CreateTenant(c.Context(), t); err != nil {
		log.Error().Err(err).Str("tenant", req.ID).Msg("Failed to create tenant")
		return writeError(c, "failed to create tenant", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	if err := s.tm.ReloadFromDB(); err != nil {
		log.Warn().Err(err).Str("tenant", req.ID).Msg("Failed to reload tenant manager after create")
	}

	created, _ := s.db.GetTenant(c.Context(), req.ID)
	c.Set("Content-Type", "application/json")
	return c.Status(fiber.StatusCreated).JSON(toTenantResponse(*created))
}

func (s *AdminServer) updateTenant(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	id := c.Params("id")
	if !validateTenantID(id) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	existing, err := s.db.GetTenant(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to get tenant for update")
		return writeError(c, "failed to update tenant", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if existing == nil {
		return writeError(c, "tenant not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	var req updateTenantRequest
	if err := c.BodyParser(&req); err != nil {
		return writeError(c, "invalid request body", "INVALID_BODY", fiber.StatusBadRequest)
	}

	if req.Name != nil {
		if *req.Name == "" {
			return writeError(c, "name cannot be empty", "VALIDATION_ERROR", fiber.StatusBadRequest)
		}
		if len(*req.Name) > 255 {
			return writeError(c, "name must be max 255 chars", "VALIDATION_ERROR", fiber.StatusBadRequest)
		}
		existing.Name = *req.Name
	}
	if req.PMSConfig != nil {
		if err := validatePMSConfig(req.PMSConfig); err != nil {
			return writeError(c, err.Error(), "VALIDATION_ERROR", fiber.StatusBadRequest)
		}
		existing.PMSConfig = req.PMSConfig
	}
	if req.PBXConfig != nil {
		if err := validatePBXConfig(req.PBXConfig); err != nil {
			return writeError(c, err.Error(), "VALIDATION_ERROR", fiber.StatusBadRequest)
		}
		existing.PBXConfig = req.PBXConfig
	}
	if req.Settings != nil {
		existing.Settings = req.Settings
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := s.db.UpdateTenant(c.Context(), existing); err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to update tenant")
		return writeError(c, "failed to update tenant", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	if err := s.tm.ReloadFromDB(); err != nil {
		log.Warn().Err(err).Str("tenant", id).Msg("Failed to reload tenant manager after update")
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(toTenantResponse(*existing))
}

func (s *AdminServer) deleteTenant(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	id := c.Params("id")
	if !validateTenantID(id) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	existing, err := s.db.GetTenant(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to get tenant for delete")
		return writeError(c, "failed to delete tenant", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if existing == nil {
		return writeError(c, "tenant not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	if err := s.db.DeleteTenant(c.Context(), id); err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to delete tenant")
		return writeError(c, "failed to delete tenant", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	s.tm.InvalidateCache(id)

	return c.SendStatus(fiber.StatusNoContent)
}

type importRequest struct {
	Tenants []createTenantRequest `json:"tenants"`
}

func (s *AdminServer) importTenants(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return writeError(c, "failed to read request body", "INVALID_BODY", fiber.StatusBadRequest)
	}

	var yamlReq struct {
		Tenants []createTenantRequest `json:"tenants" yaml:"tenants"`
	}
	if err := json.Unmarshal(body, &yamlReq); err != nil {
		return writeError(c, "request must be JSON or YAML with a 'tenants' array", "INVALID_FORMAT", fiber.StatusBadRequest)
	}

	if len(yamlReq.Tenants) == 0 {
		return writeError(c, "no tenants provided", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}

	created := 0
	errors := []string{}
	for _, req := range yamlReq.Tenants {
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

		existing, _ := s.db.GetTenant(c.Context(), req.ID)
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
		if err := s.db.CreateTenant(c.Context(), t); err != nil {
			errors = append(errors, "tenant '"+req.ID+"': failed to create: "+err.Error())
			continue
		}
		created++
	}

	if err := s.tm.ReloadFromDB(); err != nil {
		log.Warn().Err(err).Msg("Failed to reload tenant manager after import")
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(map[string]interface{}{
		"created": created,
		"errors":  errors,
	})
}

func validatePMSConfig(cfg map[string]interface{}) error {
	if cfg == nil {
		return nil
	}
	protocol, ok := cfg["protocol"].(string)
	if !ok || protocol == "" {
		return nil
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
		return nil
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