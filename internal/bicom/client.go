package bicom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Config holds Bicom PBXware API configuration
type Config struct {
	BaseURL  string // e.g., "https://pbx.example.com"
	APIKey   string // API key from Admin Settings → API Keys
	TenantID string // Server/tenant ID for multi-tenant PBXware
}

// Client is a Bicom PBXware REST API client
type Client struct {
	cfg        Config
	httpClient *http.Client
}

// NewClient creates a new Bicom PBXware API client
func NewClient(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("BaseURL is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("APIKey is required")
	}

	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// APIResponse represents a generic Bicom API response
type APIResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Extension represents a PBXware extension
type Extension struct {
	ID          string `json:"id"`
	Extension   string `json:"extension"`
	Name        string `json:"name"`
	Email       string `json:"email,omitempty"`
	Status      string `json:"status,omitempty"`
	ServicePlan string `json:"service_plan,omitempty"`
}

// WakeUpCall represents a scheduled wake-up call
type WakeUpCall struct {
	Extension string `json:"extension"`
	Time      string `json:"time"` // Format: HH:MM
	Enabled   bool   `json:"enabled"`
	Snooze    int    `json:"snooze,omitempty"` // Snooze time in minutes
}

// doRequest performs an API request to Bicom PBXware
func (c *Client) doRequest(ctx context.Context, action string, params map[string]string) (*APIResponse, error) {
	// Build URL with action parameter
	u, err := url.Parse(c.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL: %w", err)
	}
	u.Path = "/api/"

	// Build query parameters
	q := url.Values{}
	q.Set("key", c.cfg.APIKey)
	q.Set("action", action)
	q.Set("format", "json")

	if c.cfg.TenantID != "" {
		q.Set("server", c.cfg.TenantID)
	}

	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	log.Debug().
		Str("action", action).
		Str("url", u.String()).
		Msg("Bicom API request")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &apiResp, nil
}

// doPost performs a POST API request to Bicom PBXware
func (c *Client) doPost(ctx context.Context, action string, params map[string]string) (*APIResponse, error) {
	u, err := url.Parse(c.cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL: %w", err)
	}
	u.Path = "/api/"

	// Build form data
	form := url.Values{}
	form.Set("key", c.cfg.APIKey)
	form.Set("action", action)
	form.Set("format", "json")

	if c.cfg.TenantID != "" {
		form.Set("server", c.cfg.TenantID)
	}

	for k, v := range params {
		form.Set(k, v)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	log.Debug().
		Str("action", action).
		Msg("Bicom API POST request")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &apiResp, nil
}

// =============================================================================
// Extension Management
// =============================================================================

// ListExtensions returns all extensions for the current tenant
func (c *Client) ListExtensions(ctx context.Context) ([]Extension, error) {
	resp, err := c.doRequest(ctx, "pbxware.ext.list", nil)
	if err != nil {
		return nil, err
	}

	var extensions []Extension
	if err := json.Unmarshal(resp.Data, &extensions); err != nil {
		return nil, fmt.Errorf("parsing extensions: %w", err)
	}

	return extensions, nil
}

// GetExtension returns details for a specific extension
func (c *Client) GetExtension(ctx context.Context, extensionID string) (*Extension, error) {
	resp, err := c.doRequest(ctx, "pbxware.ext.configuration", map[string]string{
		"id": extensionID,
	})
	if err != nil {
		return nil, err
	}

	var ext Extension
	if err := json.Unmarshal(resp.Data, &ext); err != nil {
		return nil, fmt.Errorf("parsing extension: %w", err)
	}

	return &ext, nil
}

// UpdateExtensionName updates the name (caller ID name) for an extension
func (c *Client) UpdateExtensionName(ctx context.Context, extensionID, name string) error {
	resp, err := c.doPost(ctx, "pbxware.ext.edit", map[string]string{
		"id":   extensionID,
		"name": name,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to update extension name: %s", resp.Message)
	}

	log.Info().
		Str("extension", extensionID).
		Str("name", name).
		Msg("Extension name updated")

	return nil
}

// UpdateServicePlan changes the service plan for an extension
func (c *Client) UpdateServicePlan(ctx context.Context, extensionID, servicePlanID string) error {
	resp, err := c.doPost(ctx, "pbxware.ext.edit", map[string]string{
		"id":           extensionID,
		"service_plan": servicePlanID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to update service plan: %s", resp.Message)
	}

	log.Info().
		Str("extension", extensionID).
		Str("service_plan", servicePlanID).
		Msg("Extension service plan updated")

	return nil
}

// =============================================================================
// Wake-Up Call Management
// =============================================================================

// ScheduleWakeUpCall schedules a wake-up call for an extension
// Uses the Enhanced Services Wake-Up Call feature (normally accessed via *411)
func (c *Client) ScheduleWakeUpCall(ctx context.Context, extensionID string, wakeTime time.Time) error {
	// Format time as HH:MM for Bicom API
	timeStr := wakeTime.Format("15:04")

	resp, err := c.doPost(ctx, "pbxware.ext.es.wakeupcall.edit", map[string]string{
		"id":      extensionID,
		"time":    timeStr,
		"enabled": "1",
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to schedule wake-up call: %s", resp.Message)
	}

	log.Info().
		Str("extension", extensionID).
		Str("time", timeStr).
		Msg("Wake-up call scheduled")

	return nil
}

// CancelWakeUpCall cancels a scheduled wake-up call for an extension
func (c *Client) CancelWakeUpCall(ctx context.Context, extensionID string) error {
	resp, err := c.doPost(ctx, "pbxware.ext.es.wakeupcall.edit", map[string]string{
		"id":      extensionID,
		"enabled": "0",
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to cancel wake-up call: %s", resp.Message)
	}

	log.Info().
		Str("extension", extensionID).
		Msg("Wake-up call cancelled")

	return nil
}

// GetWakeUpCallStatus returns the current wake-up call configuration for an extension
func (c *Client) GetWakeUpCallStatus(ctx context.Context, extensionID string) (*WakeUpCall, error) {
	resp, err := c.doRequest(ctx, "pbxware.ext.es.wakeupcall.get", map[string]string{
		"id": extensionID,
	})
	if err != nil {
		return nil, err
	}

	var wakeup WakeUpCall
	if err := json.Unmarshal(resp.Data, &wakeup); err != nil {
		return nil, fmt.Errorf("parsing wake-up call: %w", err)
	}

	return &wakeup, nil
}

// =============================================================================
// Voicemail Management
// =============================================================================

// DeleteAllVoicemails deletes all voicemail messages for an extension
func (c *Client) DeleteAllVoicemails(ctx context.Context, extensionID string) error {
	resp, err := c.doPost(ctx, "pbxware.vm.delete_all", map[string]string{
		"id": extensionID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to delete voicemails: %s", resp.Message)
	}

	log.Info().
		Str("extension", extensionID).
		Msg("All voicemails deleted")

	return nil
}

// GetVoicemailCount returns the number of voicemail messages for an extension
func (c *Client) GetVoicemailCount(ctx context.Context, extensionID string) (newMsgs, oldMsgs int, err error) {
	resp, err := c.doRequest(ctx, "pbxware.vm.count", map[string]string{
		"id": extensionID,
	})
	if err != nil {
		return 0, 0, err
	}

	var counts struct {
		New int `json:"new"`
		Old int `json:"old"`
	}
	if err := json.Unmarshal(resp.Data, &counts); err != nil {
		return 0, 0, fmt.Errorf("parsing voicemail count: %w", err)
	}

	return counts.New, counts.Old, nil
}

// VoicemailGreetingType represents the type of voicemail greeting
type VoicemailGreetingType string

const (
	// GreetingDefault uses the system default greeting
	GreetingDefault VoicemailGreetingType = "default"
	// GreetingUnavailable uses the recorded unavailable greeting
	GreetingUnavailable VoicemailGreetingType = "unavailable"
	// GreetingBusy uses the recorded busy greeting
	GreetingBusy VoicemailGreetingType = "busy"
	// GreetingNone uses no greeting (straight to beep)
	GreetingNone VoicemailGreetingType = "none"
)

// SetVoicemailGreeting sets the voicemail greeting type for an extension
// For hospitality: use GreetingDefault on checkout to reset to hotel standard
func (c *Client) SetVoicemailGreeting(ctx context.Context, extensionID string, greeting VoicemailGreetingType) error {
	resp, err := c.doPost(ctx, "pbxware.ext.es.vm.edit", map[string]string{
		"id":       extensionID,
		"greeting": string(greeting),
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to set voicemail greeting: %s", resp.Message)
	}

	log.Info().
		Str("extension", extensionID).
		Str("greeting", string(greeting)).
		Msg("Voicemail greeting updated")

	return nil
}

// ResetVoicemailGreeting resets the voicemail greeting to system default
// This should be called on guest checkout to remove any personalized greetings
func (c *Client) ResetVoicemailGreeting(ctx context.Context, extensionID string) error {
	return c.SetVoicemailGreeting(ctx, extensionID, GreetingDefault)
}

// ClearVoicemailForGuest performs all voicemail cleanup for guest checkout:
// - Deletes all messages
// - Resets greeting to default
func (c *Client) ClearVoicemailForGuest(ctx context.Context, extensionID string) error {
	// Delete all messages first
	if err := c.DeleteAllVoicemails(ctx, extensionID); err != nil {
		log.Error().Err(err).Str("extension", extensionID).Msg("Failed to delete voicemails")
		// Continue to reset greeting even if delete fails
	}

	// Reset greeting to default
	if err := c.ResetVoicemailGreeting(ctx, extensionID); err != nil {
		log.Error().Err(err).Str("extension", extensionID).Msg("Failed to reset voicemail greeting")
		return err
	}

	log.Info().
		Str("extension", extensionID).
		Msg("Voicemail cleared for guest checkout")

	return nil
}

// =============================================================================
// Enhanced Services
// =============================================================================

// SetDND enables or disables Do Not Disturb for an extension
func (c *Client) SetDND(ctx context.Context, extensionID string, enabled bool) error {
	enabledStr := "0"
	if enabled {
		enabledStr = "1"
	}

	resp, err := c.doPost(ctx, "pbxware.ext.es.dnd.edit", map[string]string{
		"id":      extensionID,
		"enabled": enabledStr,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to set DND: %s", resp.Message)
	}

	log.Info().
		Str("extension", extensionID).
		Bool("enabled", enabled).
		Msg("DND updated")

	return nil
}

// SetCallForward sets call forwarding for an extension
func (c *Client) SetCallForward(ctx context.Context, extensionID, destination string, enabled bool) error {
	enabledStr := "0"
	if enabled {
		enabledStr = "1"
	}

	resp, err := c.doPost(ctx, "pbxware.ext.es.callforward.edit", map[string]string{
		"id":          extensionID,
		"enabled":     enabledStr,
		"destination": destination,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to set call forward: %s", resp.Message)
	}

	log.Info().
		Str("extension", extensionID).
		Str("destination", destination).
		Bool("enabled", enabled).
		Msg("Call forward updated")

	return nil
}

// =============================================================================
// Service Plans
// =============================================================================

// ServicePlan represents a PBXware service plan
type ServicePlan struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListServicePlans returns all available service plans
func (c *Client) ListServicePlans(ctx context.Context) ([]ServicePlan, error) {
	resp, err := c.doRequest(ctx, "pbxware.sp.list", nil)
	if err != nil {
		return nil, err
	}

	var plans []ServicePlan
	if err := json.Unmarshal(resp.Data, &plans); err != nil {
		return nil, fmt.Errorf("parsing service plans: %w", err)
	}

	return plans, nil
}
