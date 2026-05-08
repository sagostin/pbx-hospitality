package api

import (
	"encoding/json"
	"io"
	"regexp"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/pbx"
)

type AdminServer struct {
	*Server
	pbxManager *pbx.Manager
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
	SiteID    *string                `json:"site_id,omitempty"`
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
	SiteID    *string                `json:"site_id,omitempty"`
	Name      string                 `json:"name"`
	PMSConfig map[string]interface{} `json:"pms_config"`
	PBXConfig map[string]interface{} `json:"pbx_config"`
	Settings  map[string]interface{} `json:"settings"`
	Enabled   bool                   `json:"enabled"`
}

type updateTenantRequest struct {
	SiteID    *string                `json:"site_id,omitempty"`
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
		SiteID:    req.SiteID,
		Name:      req.Name,
		PMSConfig: mapToJSON(req.PMSConfig),
		PBXConfig: mapToJSON(req.PBXConfig),
		Settings:  mapToJSON(req.Settings),
		Enabled:   req.Enabled,
	}
	if err := s.db.CreateTenant(c.Context(), t); err != nil {
		log.Error().Err(err).Str("tenant", req.ID).Msg("Failed to create tenant")
		return writeError(c, "failed to create tenant", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	if err := s.tm.ReloadFromDB(c.Context()); err != nil {
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
	if req.SiteID != nil {
		if *req.SiteID == "" {
			existing.SiteID = nil // Clear site association
		} else {
			existing.SiteID = req.SiteID
		}
	}
	if req.PMSConfig != nil {
		if err := validatePMSConfig(req.PMSConfig); err != nil {
			return writeError(c, err.Error(), "VALIDATION_ERROR", fiber.StatusBadRequest)
		}
		existing.PMSConfig = mapToJSON(req.PMSConfig)
	}
	if req.PBXConfig != nil {
		if err := validatePBXConfig(req.PBXConfig); err != nil {
			return writeError(c, err.Error(), "VALIDATION_ERROR", fiber.StatusBadRequest)
		}
		existing.PBXConfig = mapToJSON(req.PBXConfig)
	}
	if req.Settings != nil {
		existing.Settings = mapToJSON(req.Settings)
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := s.db.UpdateTenant(c.Context(), existing); err != nil {
		log.Error().Err(err).Str("tenant", id).Msg("Failed to update tenant")
		return writeError(c, "failed to update tenant", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	if err := s.tm.ReloadFromDB(c.Context()); err != nil {
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

func (s *AdminServer) listTenantRooms(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenantID := c.Params("id")
	if !validateTenantID(tenantID) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	mappings, err := s.db.ListRoomMappings(c.Context(), tenantID)
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to list room mappings")
		return writeError(c, "failed to list rooms", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(mappings)
}

func (s *AdminServer) getTenantRoom(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenantID := c.Params("id")
	roomNumber := c.Params("room")
	if !validateTenantID(tenantID) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}
	if roomNumber == "" {
		return writeError(c, "room number is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}

	mapping, err := s.db.GetRoomMapping(c.Context(), tenantID, roomNumber)
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to get room mapping")
		return writeError(c, "failed to get room", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if mapping == nil {
		return writeError(c, "room mapping not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(mapping)
}

func (s *AdminServer) deleteTenantRoom(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenantID := c.Params("id")
	roomNumber := c.Params("room")
	if !validateTenantID(tenantID) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}
	if roomNumber == "" {
		return writeError(c, "room number is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}

	if err := s.db.DeleteRoomMapping(c.Context(), tenantID, roomNumber); err != nil {
		if err.Error() == "room mapping not found" {
			return writeError(c, "room mapping not found", "NOT_FOUND", fiber.StatusNotFound)
		}
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to delete room mapping")
		return writeError(c, "failed to delete room", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func (s *AdminServer) listTenantSessions(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenantID := c.Params("id")
	if !validateTenantID(tenantID) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	allSessions := c.QueryBool("all", false)
	if allSessions {
		sessions, err := s.db.ListAllGuestSessions(c.Context(), tenantID)
		if err != nil {
			log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to list guest sessions")
			return writeError(c, "failed to list sessions", "INTERNAL_ERROR", fiber.StatusInternalServerError)
		}
		c.Set("Content-Type", "application/json")
		return c.JSON(sessions)
	}

	sessions, err := s.db.ListActiveSessions(c.Context(), tenantID)
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to list active sessions")
		return writeError(c, "failed to list sessions", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(sessions)
}

func (s *AdminServer) getTenantSession(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenantID := c.Params("id")
	roomNumber := c.Params("room")
	if !validateTenantID(tenantID) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}
	if roomNumber == "" {
		return writeError(c, "room number is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}

	session, err := s.db.GetGuestSessionByRoom(c.Context(), tenantID, roomNumber)
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to get guest session")
		return writeError(c, "failed to get session", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if session == nil {
		return writeError(c, "session not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(session)
}

func (s *AdminServer) deleteTenantSession(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenantID := c.Params("id")
	roomNumber := c.Params("room")
	if !validateTenantID(tenantID) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}
	if roomNumber == "" {
		return writeError(c, "room number is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}

	if err := s.db.DeleteGuestSession(c.Context(), tenantID, roomNumber); err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to delete guest session")
		return writeError(c, "failed to delete session", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func (s *AdminServer) listTenantEvents(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenantID := c.Params("id")
	if !validateTenantID(tenantID) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if parsed := parsePositiveInt(l); parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	offset := parsePositiveInt(c.Query("offset"))

	var processed *bool
	if c.Query("processed") != "" {
		p := c.QueryBool("processed", false)
		processed = &p
	}

	events, err := s.db.ListPMSEvents(c.Context(), tenantID, processed, limit, offset)
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to list PMS events")
		return writeError(c, "failed to list events", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(events)
}

func (s *AdminServer) deleteTenantEvent(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenantID := c.Params("id")
	eventID := c.Params("eventID")
	if !validateTenantID(tenantID) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	id := parsePositiveInt(eventID)
	if id == 0 {
		return writeError(c, "invalid event ID", "INVALID_ID", fiber.StatusBadRequest)
	}

	event, err := s.db.GetPMSEvent(c.Context(), tenantID, int64(id))
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to get PMS event")
		return writeError(c, "failed to get event", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if event == nil {
		return writeError(c, "event not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	if err := s.db.DeletePMSEvent(c.Context(), int64(id)); err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to delete PMS event")
		return writeError(c, "failed to delete event", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func (s *AdminServer) retryTenantEvent(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenantID := c.Params("id")
	eventID := c.Params("eventID")
	if !validateTenantID(tenantID) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	id := parsePositiveInt(eventID)
	if id == 0 {
		return writeError(c, "invalid event ID", "INVALID_ID", fiber.StatusBadRequest)
	}

	event, err := s.db.GetPMSEvent(c.Context(), tenantID, int64(id))
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to get PMS event")
		return writeError(c, "failed to get event", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if event == nil {
		return writeError(c, "event not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	if err := s.db.ResetPMSEvent(c.Context(), int64(id)); err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to reset PMS event")
		return writeError(c, "failed to reset event", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(map[string]interface{}{
		"status":   "reset",
		"event_id": id,
		"tenant":   tenantID,
	})
}

func (s *AdminServer) getTenantHealth(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	tenantID := c.Params("id")
	if !validateTenantID(tenantID) {
		return writeError(c, "invalid tenant ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	t, err := s.db.GetTenant(c.Context(), tenantID)
	if err != nil {
		log.Error().Err(err).Str("tenant", tenantID).Msg("Failed to get tenant")
		return writeError(c, "failed to get tenant", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if t == nil {
		return writeError(c, "tenant not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	var pmsConnected, pbxConnected bool
	pmsCfg := parseJSONMap(t.PMSConfig)
	if pmsCfg != nil && len(pmsCfg) > 0 {
		pmsConnected = true
	}
	pbxCfg := parseJSONMap(t.PBXConfig)
	if pbxCfg != nil && len(pbxCfg) > 0 {
		pbxConnected = true
	}

	rooms, _ := s.db.ListRoomMappings(c.Context(), tenantID)
	sessions, _ := s.db.ListActiveSessions(c.Context(), tenantID)

	c.Set("Content-Type", "application/json")
	return c.JSON(map[string]interface{}{
		"tenant_id":       tenantID,
		"name":            t.Name,
		"pms_connected":   pmsConnected,
		"pbx_connected":   pbxConnected,
		"enabled":         t.Enabled,
		"room_count":      len(rooms),
		"active_sessions": len(sessions),
	})
}

func parsePositiveInt(s string) int {
	if s == "" {
		return 0
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

type importRequest struct {
	Tenants []createTenantRequest `json:"tenants"`
}

func (s *AdminServer) importTenants(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	body, err := io.ReadAll(c.Request().BodyStream())
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
			PMSConfig: mapToJSON(req.PMSConfig),
			PBXConfig: mapToJSON(req.PBXConfig),
			Settings:  mapToJSON(req.Settings),
			Enabled:   req.Enabled,
		}
		if err := s.db.CreateTenant(c.Context(), t); err != nil {
			errors = append(errors, "tenant '"+req.ID+"': failed to create: "+err.Error())
			continue
		}
		created++
	}

	if err := s.tm.ReloadFromDB(c.Context()); err != nil {
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
	pmsCfg := parseJSONMap(t.PMSConfig)
	pbxCfg := parseJSONMap(t.PBXConfig)
	settings := parseJSONMap(t.Settings)
	return tenantResponse{
		ID:        t.ID,
		SiteID:    t.SiteID,
		Name:      t.Name,
		PMSConfig: pmsCfg,
		PBXConfig: pbxCfg,
		Settings:  settings,
		Enabled:   t.Enabled,
		CreatedAt: t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: t.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func parseJSONMap(jsonStr string) map[string]interface{} {
	if jsonStr == "" {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}

func mapToJSON(m map[string]interface{}) string {
	if m == nil {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}
