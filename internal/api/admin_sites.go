package api

import (
	"encoding/json"
	"regexp"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/db"
)

type siteResponse struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	AuthCode  string                 `json:"-"` // Never expose in API responses
	Settings  map[string]interface{} `json:"settings"`
	Enabled   bool                   `json:"enabled"`
	CreatedAt string                 `json:"created_at,omitempty"`
	UpdatedAt string                 `json:"updated_at,omitempty"`
}

type createSiteRequest struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	AuthCode string                 `json:"auth_code"`
	Settings map[string]interface{} `json:"settings"`
	Enabled  bool                   `json:"enabled"`
}

type updateSiteRequest struct {
	Name     *string                `json:"name,omitempty"`
	AuthCode *string                `json:"auth_code,omitempty"`
	Settings map[string]interface{} `json:"settings,omitempty"`
	Enabled  *bool                  `json:"enabled,omitempty"`
}

var siteIDRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

func validateSiteID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	return siteIDRegex.MatchString(id)
}

func (s *AdminServer) listSites(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	sites, err := s.db.ListSites(c.Context())
	if err != nil {
		log.Error().Err(err).Msg("Failed to list sites")
		return writeError(c, "failed to list sites", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	result := make([]siteResponse, 0, len(sites))
	for _, site := range sites {
		result = append(result, toSiteResponse(site))
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(result)
}

func (s *AdminServer) getSite(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	id := c.Params("id")
	if !validateSiteID(id) {
		return writeError(c, "invalid site ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	site, err := s.db.GetSite(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("site", id).Msg("Failed to get site")
		return writeError(c, "failed to get site", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if site == nil {
		return writeError(c, "site not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(toSiteResponse(*site))
}

func (s *AdminServer) createSite(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	var req createSiteRequest
	if err := c.BodyParser(&req); err != nil {
		return writeError(c, "invalid request body", "INVALID_BODY", fiber.StatusBadRequest)
	}

	if req.ID == "" {
		return writeError(c, "id is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if !validateSiteID(req.ID) {
		return writeError(c, "id must be alphanumeric with dashes, max 64 chars", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if req.Name == "" {
		return writeError(c, "name is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if len(req.Name) > 255 {
		return writeError(c, "name must be max 255 chars", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if req.AuthCode == "" {
		return writeError(c, "auth_code is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}
	if len(req.AuthCode) < 16 {
		return writeError(c, "auth_code must be at least 16 characters", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}

	existing, err := s.db.GetSite(c.Context(), req.ID)
	if err != nil {
		log.Error().Err(err).Str("site", req.ID).Msg("Failed to check existing site")
		return writeError(c, "failed to create site", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if existing != nil {
		return writeError(c, "site already exists", "ALREADY_EXISTS", fiber.StatusConflict)
	}

	site := &db.Site{
		ID:       req.ID,
		Name:     req.Name,
		AuthCode: req.AuthCode, // In production, this should be hashed
		Settings: siteMapToJSON(req.Settings),
		Enabled:  req.Enabled,
	}
	if err := s.db.CreateSite(c.Context(), site); err != nil {
		log.Error().Err(err).Str("site", req.ID).Msg("Failed to create site")
		return writeError(c, "failed to create site", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	created, _ := s.db.GetSite(c.Context(), req.ID)
	c.Set("Content-Type", "application/json")
	return c.Status(fiber.StatusCreated).JSON(toSiteResponse(*created))
}

func (s *AdminServer) updateSite(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	id := c.Params("id")
	if !validateSiteID(id) {
		return writeError(c, "invalid site ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	existing, err := s.db.GetSite(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("site", id).Msg("Failed to get site for update")
		return writeError(c, "failed to update site", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if existing == nil {
		return writeError(c, "site not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	var req updateSiteRequest
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
	if req.AuthCode != nil {
		if len(*req.AuthCode) < 16 {
			return writeError(c, "auth_code must be at least 16 characters", "VALIDATION_ERROR", fiber.StatusBadRequest)
		}
		existing.AuthCode = *req.AuthCode // In production, hash this
	}
	if req.Settings != nil {
		existing.Settings = siteMapToJSON(req.Settings)
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := s.db.UpdateSite(c.Context(), existing); err != nil {
		log.Error().Err(err).Str("site", id).Msg("Failed to update site")
		return writeError(c, "failed to update site", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(toSiteResponse(*existing))
}

func (s *AdminServer) deleteSite(c *fiber.Ctx) error {
	if s.db == nil {
		return writeError(c, "database not configured", "DB_NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	id := c.Params("id")
	if !validateSiteID(id) {
		return writeError(c, "invalid site ID format", "INVALID_ID", fiber.StatusBadRequest)
	}

	existing, err := s.db.GetSite(c.Context(), id)
	if err != nil {
		log.Error().Err(err).Str("site", id).Msg("Failed to get site for delete")
		return writeError(c, "failed to delete site", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}
	if existing == nil {
		return writeError(c, "site not found", "NOT_FOUND", fiber.StatusNotFound)
	}

	if err := s.db.DeleteSite(c.Context(), id); err != nil {
		log.Error().Err(err).Str("site", id).Msg("Failed to delete site")
		return writeError(c, "failed to delete site", "INTERNAL_ERROR", fiber.StatusInternalServerError)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

func toSiteResponse(s db.Site) siteResponse {
	settings := parseSiteJSONMap(s.Settings)
	return siteResponse{
		ID:        s.ID,
		Name:      s.Name,
		AuthCode:  "", // Never expose
		Settings:  settings,
		Enabled:   s.Enabled,
		CreatedAt: s.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: s.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func parseSiteJSONMap(jsonStr string) map[string]interface{} {
	if jsonStr == "" {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}

func siteMapToJSON(m map[string]interface{}) string {
	if m == nil {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}