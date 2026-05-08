package api

import (
	"regexp"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/db"
)

type bicomSystemResponse struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	APIURL       string                 `json:"api_url"`
	APIKey       string                 `json:"-"` // Never expose
	TenantID     string                 `json:"tenant_id,omitempty"`
	ARIURL       string                 `json:"ari_url,omitempty"`
	ARIUser      string                 `json:"ari_user,omitempty"`
	ARIAppName   string                 `json:"ari_app_name,omitempty"`
	WebhookURL   string                 `json:"webhook_url,omitempty"`
	HealthStatus string                 `json:"health_status"`
	Settings     map[string]interface{} `json:"settings"`
	Enabled      bool                   `json:"enabled"`
	CreatedAt    string                 `json:"created_at,omitempty"`
	UpdatedAt    string                 `json:"updated_at,omitempty"`
}

type createBicomSystemRequest struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	APIURL     string                 `json:"api_url"`
	APIKey     string                 `json:"api_key"`
	TenantID   string                 `json:"tenant_id,omitempty"`
	ARIURL     string                 `json:"ari_url,omitempty"`
	ARIUser    string                 `json:"ari_user,omitempty"`
	ARIPass    string                 `json:"ari_pass,omitempty"`
	ARIAppName string                 `json:"ari_app_name,omitempty"`
	WebhookURL string                 `json:"webhook_url,omitempty"`
	Settings   map[string]interface{} `json:"settings"`
	Enabled    bool                   `json:"enabled"`
}

type updateBicomSystemRequest struct {
	Name       *string                `json:"name,omitempty"`
	APIURL     *string                `json:"api_url,omitempty"`
	APIKey     *string                `json:"api_key,omitempty"`
	TenantID   *string                `json:"tenant_id,omitempty"`
	ARIURL     *string                `json:"ari_url,omitempty"`
	ARIUser    *string                `json:"ari_user,omitempty"`
	ARIPass    *string                `json:"ari_pass,omitempty"`
	ARIAppName *string                `json:"ari_app_name,omitempty"`
	WebhookURL *string                `json:"webhook_url,omitempty"`
	Settings   map[string]interface{} `json:"settings,omitempty"`
	Enabled    *bool                  `json:"enabled,omitempty"`
}

var bicomSystemIDRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

func validateBicomSystemID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	return bicomSystemIDRegex.MatchString(id)
}

func (s *AdminServer) listBicomSystems(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	systems, err := s.db.ListBicomSystems(c.Context())
	if err != nil {
		log.Error().Err(err).Msg("Failed to list bicom systems")
		return writeError(c, "failed to list bicom systems", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	result := make([]bicomSystemResponse, 0, len(systems))
	for _, sys := range systems {
		result = append(result, toBicomSystemResponse(sys))
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(result)
}

func (s *AdminServer) getBicomSystem(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	id := c.Params("id")
	if !validateBicomSystemID(id) {
		return writeError(c, "invalid bicom system ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	system, err := s.db.GetBicomSystem(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("system", id).Msg("Failed to get bicom system")
		return writeError(c, "failed to get bicom system", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if system == nil {
		return writeError(c, "bicom system not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(toBicomSystemResponse(*system))
}

func (s *AdminServer) createBicomSystem(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	var req createBicomSystemRequest
	if err := c.BodyParser(&req); err != nil {
		return writeError(c, "invalid request body", "INVALID_BODY", fiber.StatusBadRequest)
	}

	if req.ID == "" {
		return writeError(c, "id is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if !validateBicomSystemID(req.ID) {
		return writeError(c, "id must be alphanumeric with dashes, max 64 chars", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if req.Name == "" {
		return writeError(c, "name is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if req.APIURL == "" {
		return writeError(c, "api_url is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if req.APIKey == "" {
		return writeError(c, "api_key is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}

	existing, err := s.db.GetBicomSystem(c.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Str("system", req.ID).Msg("Failed to check existing bicom system")
		return writeError(c, "failed to create bicom system", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if existing != nil {
		return writeError(c, "bicom system already exists", "ALREADY_EXISTS", fiber.StatusConflict)
	}

	system := &db.BicomSystem{
		ID:           req.ID,
		Name:         req.Name,
		APIURL:       req.APIURL,
		APIKey:       req.APIKey,
		TenantID:     req.TenantID,
		ARIURL:       req.ARIURL,
		ARIUser:      req.ARIUser,
		ARIPass:      req.ARIPass,
		ARIAppName:   req.ARIAppName,
		WebhookURL:   req.WebhookURL,
		HealthStatus: "unknown",
		Settings:     bicomSettingsToJSON(req.Settings),
		Enabled:      req.Enabled,
	}
	if err := s.db.CreateBicomSystem(c.Context(), system); err != nil {
		log.Error().Err(err).Str("system", req.ID).Msg("Failed to create bicom system")
		return writeError(c, "failed to create bicom system", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	created, _ := s.db.GetBicomSystem(c.Context(), req.ID)
	c.Set("Content-Type", "application/json")
	return c.Status(fiber.StatusCreated).JSON(toBicomSystemResponse(*created))
}

func (s *AdminServer) updateBicomSystem(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	id := c.Params("id")
	if !validateBicomSystemID(id) {
		return writeError(c, "invalid bicom system ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	existing, err := s.db.GetBicomSystem(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("system", id).Msg("Failed to get bicom system for update")
		return writeError(c, "failed to update bicom system", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if existing == nil {
		return writeError(c, "bicom system not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	var req updateBicomSystemRequest
	if err := c.BodyParser(&req); err != nil {
		return writeError(c, "invalid request body", "INVALID_BODY", fiber.StatusBadRequest)
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.APIURL != nil {
		existing.APIURL = *req.APIURL
	}
	if req.APIKey != nil {
		existing.APIKey = *req.APIKey
	}
	if req.TenantID != nil {
		existing.TenantID = *req.TenantID
	}
	if req.ARIURL != nil {
		existing.ARIURL = *req.ARIURL
	}
	if req.ARIUser != nil {
		existing.ARIUser = *req.ARIUser
	}
	if req.ARIPass != nil {
		existing.ARIPass = *req.ARIPass
	}
	if req.ARIAppName != nil {
		existing.ARIAppName = *req.ARIAppName
	}
	if req.WebhookURL != nil {
		existing.WebhookURL = *req.WebhookURL
	}
	if req.Settings != nil {
		existing.Settings = bicomSettingsToJSON(req.Settings)
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := s.db.UpdateBicomSystem(c.Context(), existing); err != nil {
		log.Error().Err(err).Str("system", id).Msg("Failed to update bicom system")
		return writeError(c, "failed to update bicom system", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(toBicomSystemResponse(*existing))
}

func (s *AdminServer) deleteBicomSystem(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	id := c.Params("id")
	if !validateBicomSystemID(id) {
		return writeError(c, "invalid bicom system ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	existing, err := s.db.GetBicomSystem(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("system", id).Msg("Failed to get bicom system for delete")
		return writeError(c, "failed to delete bicom system", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if existing == nil {
		return writeError(c, "bicom system not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	if err := s.db.DeleteBicomSystem(c.Context(), id); err != nil {
		log.Error().Err(err).Str("system", id).Msg("Failed to delete bicom system")
		return writeError(c, "failed to delete bicom system", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

type updateARISecretRequest struct {
	ARIPass string `json:"ari_pass"`
}

func (s *AdminServer) updateBicomSystemARISecret(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	id := c.Params("id")
	if !validateBicomSystemID(id) {
		return writeError(c, "invalid bicom system ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	var req updateARISecretRequest
	if err := c.BodyParser(&req); err != nil {
		return writeError(c, "invalid request body", "INVALID_BODY", fiber.StatusBadRequest)
	}

	if req.ARIPass == "" {
		return writeError(c, "ari_pass is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}

	existing, err := s.db.GetBicomSystem(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("system", id).Msg("Failed to get bicom system for secret update")
		return writeError(c, "failed to get bicom system", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if existing == nil {
		return writeError(c, "bicom system not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	// Set and encrypt the new ARI password
	existing.ARIPass = req.ARIPass
	if err := existing.SetARIPass(req.ARIPass); err != nil {
		log.Error().Err(err).Str("system", id).Msg("Failed to encrypt ARI password")
		return writeError(c, "failed to encrypt password", "ENCRYPTION_FAILED", fiber.StatusInternalServerError)
	}

	if err := s.db.UpdateBicomSystem(c.Context(), existing); err != nil {
		log.Error().Err(err).Str("system", id).Msg("Failed to update bicom system secret")
		return writeError(c, "failed to update secret", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	// Trigger PBX reload if manager is available
	if s.pbxManager != nil {
		if err := s.pbxManager.ReloadSystem(c.Context(), id); err != nil {
			log.Warn().Err(err).Str("system", id).Msg("Failed to reload PBX after secret update")
		}
	}

	log.Info().Str("system", id).Msg("ARI secret updated for bicom system")
	return c.JSON(map[string]string{
		"status": "updated",
		"system": id,
	})
}

type siteBicomMappingRequest struct {
	BicomSystemID   string `json:"bicom_system_id"`
	Priority        int    `json:"priority"`
	FailoverEnabled bool   `json:"failover_enabled"`
}

func (s *AdminServer) listSiteBicomMappings(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	siteID := c.Params("id")
	mappings, err := s.db.ListSiteBicomMappings(c.Context(), siteID)
	if err != nil {
		log.Error().Err(err).Str("site", siteID).Msg("Failed to list site-bicom mappings")
		return writeError(c, "failed to list mappings", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	type mappingResponse struct {
		ID              uint   `json:"id"`
		SiteID          string `json:"site_id"`
		BicomSystemID   string `json:"bicom_system_id"`
		Priority        int    `json:"priority"`
		FailoverEnabled bool   `json:"failover_enabled"`
	}

	result := make([]mappingResponse, 0, len(mappings))
	for _, m := range mappings {
		result = append(result, mappingResponse{
			ID:              m.ID,
			SiteID:          m.SiteID,
			BicomSystemID:   m.BicomSystemID,
			Priority:        m.Priority,
			FailoverEnabled: m.FailoverEnabled,
		})
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(result)
}

func (s *AdminServer) addSiteBicomMapping(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	siteID := c.Params("id")
	if !validateSiteID(siteID) {
		return writeError(c, "invalid site ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	var req siteBicomMappingRequest
	if err := c.BodyParser(&req); err != nil {
		return writeError(c, "invalid request body", "INVALID_BODY", fiber.StatusBadRequest)
	}

	if req.BicomSystemID == "" {
		return writeError(c, "bicom_system_id is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if !validateBicomSystemID(req.BicomSystemID) {
		return writeError(c, "invalid bicom_system_id format", "INVALID_ID", fiber.StatusBadRequest)
	}

	system, err := s.db.GetBicomSystem(c.Context(), req.BicomSystemID)
	if err != nil || system == nil {
		return writeError(c, "bicom system not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	mapping := &db.SiteBicomMapping{
		SiteID:          siteID,
		BicomSystemID:   req.BicomSystemID,
		Priority:        req.Priority,
		FailoverEnabled: req.FailoverEnabled,
	}
	if err := s.db.CreateSiteBicomMapping(c.Context(), mapping); err != nil {
		log.Error().Err(err).Str("site", siteID).Msg("Failed to create site-bicom mapping")
		return writeError(c, "failed to create mapping", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	c.Set("Content-Type", "application/json")
	return c.Status(fiber.StatusCreated).JSON(mapping)
}

func (s *AdminServer) removeSiteBicomMapping(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	siteID := c.Params("id")
	bicomSystemID := c.Params("bicomSystemId")

	if !validateSiteID(siteID) {
		return writeError(c, "invalid site ID format", "INVALID_ID", fiber.StatusBadRequest)
	}
	if !validateBicomSystemID(bicomSystemID) {
		return writeError(c, "invalid bicom system ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	if err := s.db.DeleteSiteBicomMapping(c.Context(), siteID, bicomSystemID); err != nil {
		log.Error().Err(err).Str("site", siteID).Str("bicom", bicomSystemID).Msg("Failed to delete site-bicom mapping")
		return writeError(c, "failed to delete mapping", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func (s *AdminServer) getSiteHealth(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	siteID := c.Params("id")
	if !validateSiteID(siteID) {
		return writeError(c, "invalid site ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	site, err := s.db.GetSite(c.Context(), siteID)
	if err != nil || site == nil {
		return writeError(c, "site not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	health, err := s.db.GetSiteHealthStatus(c.Context(), siteID)
	if err != nil {
		log.Error().Err(err).Str("site", siteID).Msg("Failed to get site health status")
		return writeError(c, "failed to get health status", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	systems, _ := s.db.GetBicomSystemsForSite(c.Context(), siteID)
	systemStatuses := make([]map[string]interface{}, 0, len(systems))
	for _, sys := range systems {
		systemStatuses = append(systemStatuses, map[string]interface{}{
			"id":            sys.ID,
			"name":          sys.Name,
			"health_status": sys.HealthStatus,
			"api_url":       sys.APIURL,
		})
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(map[string]interface{}{
		"site_id":       siteID,
		"health_status": health,
		"systems":       systemStatuses,
	})
}

func toBicomSystemResponse(s db.BicomSystem) bicomSystemResponse {
	settings := parseSiteJSONMap(s.Settings)
	return bicomSystemResponse{
		ID:           s.ID,
		Name:         s.Name,
		APIURL:       s.APIURL,
		APIKey:       "", // Never expose
		TenantID:     s.TenantID,
		ARIURL:       s.ARIURL,
		ARIUser:      s.ARIUser,
		ARIAppName:   s.ARIAppName,
		WebhookURL:   s.WebhookURL,
		HealthStatus: s.HealthStatus,
		Settings:     settings,
		Enabled:      s.Enabled,
		CreatedAt:    s.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    s.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func bicomSettingsToJSON(m map[string]interface{}) string {
	return siteMapToJSON(m)
}
