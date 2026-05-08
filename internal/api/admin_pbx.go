package api

import (
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
)

type pbxStatusResponse struct {
	SystemID string `json:"system_id"`
	State    string `json:"state"`
	LastSeen string `json:"last_seen"`
}

func (s *AdminServer) listPBXStatus(c *fiber.Ctx) error {
	if s.pbxManager == nil {
		return writeError(c, "PBX manager not configured", "NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	statuses := s.pbxManager.Status()
	result := make([]pbxStatusResponse, 0, len(statuses))
	for _, st := range statuses {
		result = append(result, pbxStatusResponse{
			SystemID: st.SystemID,
			State:    st.State,
			LastSeen: st.LastSeen.Format("2006-01-02T15:04:05Z"),
		})
	}

	c.Set("Content-Type", "application/json")
	return c.JSON(result)
}

func (s *AdminServer) reloadPBXSystem(c *fiber.Ctx) error {
	if s.pbxManager == nil {
		return writeError(c, "PBX manager not configured", "NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	systemID := c.Params("id")
	if systemID == "" {
		return writeError(c, "system ID is required", "VALIDATION_ERROR", fiber.StatusBadRequest)
	}

	log.Info().Str("system", systemID).Msg("Admin triggered PBX reload")
	if err := s.pbxManager.ReloadSystem(c.Context(), systemID); err != nil {
		log.Error().Err(err).Str("system", systemID).Msg("Failed to reload PBX system")
		return writeError(c, "failed to reload system", "RELOAD_FAILED", fiber.StatusInternalServerError)
	}

	return c.JSON(map[string]string{
		"status": "reloading",
		"system": systemID,
	})
}

func (s *AdminServer) reloadAllPBX(c *fiber.Ctx) error {
	if s.pbxManager == nil {
		return writeError(c, "PBX manager not configured", "NOT_CONFIGURED", fiber.StatusServiceUnavailable)
	}

	log.Info().Msg("Admin triggered full PBX reload")
	if err := s.pbxManager.ReloadFromDB(c.Context()); err != nil {
		log.Error().Err(err).Msg("Failed to reload all PBX systems")
		return writeError(c, "failed to reload systems", "RELOAD_FAILED", fiber.StatusInternalServerError)
	}

	return c.JSON(map[string]string{
		"status": "reloading",
		"scope":  "all",
	})
}
