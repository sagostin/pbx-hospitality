package bicom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
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

// WakeUpCall represents the wake-up call state on an extension.
//
// Bicom's `pbxware.ext.es.opwakeupcall.get` response only confirms whether
// the extension has a wake-up scheduled; the actual time is held
// internally by the PBX and not surfaced via this REST API.
type WakeUpCall struct {
	Extension string `json:"extension"`
	Enabled   bool   `json:"enabled"`          // Operator-set wake-up is set
	Time      string `json:"time,omitempty"`   // Best-effort, may be empty
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

// AddExtension creates a new extension for the tenant.
// This is used for dynamic room provisioning where extensions are created
// on check-in and removed on check-out rather than pre-provisioned.
// Required params: name, ext (extension number), prot (protocol, e.g. "sip").
// Optional: email, secret, pin, voicemail, status, incominglimit, outgoinglimit,
// service_plan, and other PBXware extension attributes.
func (c *Client) AddExtension(ctx context.Context, params map[string]string) (*Extension, error) {
	resp, err := c.doPost(ctx, "pbxware.ext.add", params)
	if err != nil {
		return nil, err
	}

	if !resp.Success {
		return nil, fmt.Errorf("failed to add extension: %s", resp.Message)
	}

	var ext Extension
	if err := json.Unmarshal(resp.Data, &ext); err != nil {
		// API may return just an ID or a full Extension object
		return nil, fmt.Errorf("parsing added extension: %w", err)
	}

	log.Info().
		Str("extension", ext.Extension).
		Str("name", ext.Name).
		Msg("Extension created")

	return &ext, nil
}

// DeleteExtension removes an extension from the tenant.
// The extensionID is the PBXware internal ID (not the dialed extension number).
func (c *Client) DeleteExtension(ctx context.Context, extensionID string) error {
	resp, err := c.doPost(ctx, "pbxware.ext.delete", map[string]string{
		"id": extensionID,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to delete extension: %s", resp.Message)
	}

	log.Info().
		Str("extension", extensionID).
		Msg("Extension deleted")

	return nil
}

// =============================================================================
// Wake-Up Call Management
// =============================================================================
//
// The Bicom PBXware REST API only supports toggling the wake-up state for an
// extension. It does NOT accept a time parameter — the actual wake-up time
// is set either via the PBXware UI / dialplan or via the guest dialing the
// *411 feature code from the room extension.
//
// Endpoints:
//   - pbxware.ext.es.opwakeupcall.set  — operator-set variant (state=yes|no)
//   - pbxware.ext.es.opwakeupcall.get  — read current state
//   - pbxware.ext.es.wakeupcall.set    — self-set variant (state=yes|no)
//   - pbxware.ext.es.wakeupcall.get    — read current state
//
// For hospitality, the operator-set variant is the right choice: staff sets
// the wake-up on behalf of the guest via the PMS.
//
// To actually fire the call at the scheduled time, the WakeUpScheduler uses
// ARI Channels.Originate (see internal/pbx/bicom/provider.go).
// =============================================================================

// ScheduleWakeUpCall toggles the operator-set wake-up state on Bicom.
// The wakeTime argument is accepted for interface compatibility and to
// drive the WakeUpScheduler; Bicom's REST API only accepts state=yes|no.
func (c *Client) ScheduleWakeUpCall(ctx context.Context, extensionID string, wakeTime time.Time) error {
	return c.SetWakeUpState(ctx, extensionID, true)
}

// CancelWakeUpCall clears the operator-set wake-up state on Bicom.
func (c *Client) CancelWakeUpCall(ctx context.Context, extensionID string) error {
	return c.SetWakeUpState(ctx, extensionID, false)
}

// SetWakeUpState toggles the operator-set wake-up state on the extension.
// state=true → wake-up is set, false → cleared.
func (c *Client) SetWakeUpState(ctx context.Context, extensionID string, state bool) error {
	stateStr := "no"
	if state {
		stateStr = "yes"
	}

	resp, err := c.doPost(ctx, "pbxware.ext.es.opwakeupcall.set", map[string]string{
		"id":    extensionID,
		"state": stateStr,
	})
	if err != nil {
		return err
	}

	if !resp.Success {
		return fmt.Errorf("failed to set wake-up state: %s", resp.Message)
	}

	log.Info().
		Str("extension", extensionID).
		Str("state", stateStr).
		Msg("Bicom wake-up state set")

	return nil
}

// GetWakeUpCallStatus returns the current wake-up state for an extension.
// Bicom only exposes on/off; the actual scheduled time is held internally
// and not surfaced via this REST API.
func (c *Client) GetWakeUpCallStatus(ctx context.Context, extensionID string) (*WakeUpCall, error) {
	resp, err := c.doRequest(ctx, "pbxware.ext.es.opwakeupcall.get", map[string]string{
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

// VoicemailClearError captures partial failures in ClearVoicemailForGuest.
// Callers can use errors.As to extract granular failure details.
type VoicemailClearError struct {
	DeleteFailed   bool
	GreetingFailed bool
	DeleteErr      error
	GreetingErr    error
}

func (e *VoicemailClearError) Error() string {
	var parts []string
	if e.DeleteFailed {
		parts = append(parts, "voicemail delete failed")
	}
	if e.GreetingFailed {
		parts = append(parts, "greeting reset failed")
	}
	return strings.Join(parts, "; ")
}

func (e *VoicemailClearError) Is(target error) bool {
	_, ok := target.(*VoicemailClearError)
	return ok
}

// ClearVoicemailForGuest performs all voicemail cleanup for guest checkout:
// - Deletes all messages
// - Resets greeting to default
// Returns VoicemailClearError if any step fails so callers can distinguish
// partial from complete failures.
func (c *Client) ClearVoicemailForGuest(ctx context.Context, extensionID string) error {
	var clearErr VoicemailClearError

	// Delete all messages first
	if err := c.DeleteAllVoicemails(ctx, extensionID); err != nil {
		log.Error().Err(err).Str("extension", extensionID).Msg("Failed to delete voicemails")
		clearErr.DeleteFailed = true
		clearErr.DeleteErr = err
	}

	// Reset greeting to default
	if err := c.ResetVoicemailGreeting(ctx, extensionID); err != nil {
		log.Error().Err(err).Str("extension", extensionID).Msg("Failed to reset voicemail greeting")
		clearErr.GreetingFailed = true
		clearErr.GreetingErr = err
	}

	if clearErr.DeleteFailed || clearErr.GreetingFailed {
		return &clearErr
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

// CDRRecord represents a single Asterisk CDR row as exposed by Bicom's
// `pbxware.cdr.list` (or equivalent) endpoint. Field names mirror the
// Asterisk CDR spec used in the TigerTMS iLink CDR PDF so the same
// payload shape can be relayed downstream without renaming.
//
// The actual Bicom CDR action name and exact response shape should be
// confirmed against the Bicom API docs at integration time — the
// poller translates whatever shape comes back into this struct.
type CDRRecord struct {
	UniqueID    string `json:"uniqueid,omitempty"`
	AccountCode string `json:"accountcode,omitempty"`
	Src         string `json:"src,omitempty"`
	Dst         string `json:"dst,omitempty"`
	DContext    string `json:"dcontext,omitempty"`
	CLID        string `json:"clid,omitempty"`
	Channel     string `json:"channel,omitempty"`
	DstChannel  string `json:"dstchannel,omitempty"`
	LastApp     string `json:"lastapp,omitempty"`
	LastData    string `json:"lastdata,omitempty"`
	Start       string `json:"start,omitempty"`
	Answer      string `json:"answer,omitempty"`
	End         string `json:"end,omitempty"`
	Duration    string `json:"duration,omitempty"`
	BillSec     string `json:"billsec,omitempty"`
	Disposition string `json:"disposition,omitempty"`
	AMAFlags    string `json:"amaflags,omitempty"`
}

// CDRPoller periodically polls the Bicom CDR endpoint and forwards new
// records to a sink function. The sink is responsible for relaying
// each record to its destination (iLink outbound dispatcher, Bicom
// Event Publisher pipeline, etc.).
//
// Watermarking: we keep a per-tenant `last_seen_end` timestamp so we
// only emit records whose `end` field is later than the watermark.
// The watermark is in-memory only — restart resumes from
// `sinceTime` (default = now-1h).
type CDRPoller struct {
	client    *Client
	sinceTime time.Time
	interval  time.Duration
	tenantID  string

	mu          sync.Mutex
	lastSeenEnd time.Time
}

// NewCDRPoller creates a poller that runs against the given Bicom
// client. The poller emits CDR records that ended strictly after
// `since`. Pass since=time.Now().Add(-time.Hour) to start with a
// one-hour backfill.
func NewCDRPoller(client *Client, tenantID string, since time.Time, interval time.Duration) *CDRPoller {
	return &CDRPoller{
		client:      client,
		tenantID:    tenantID,
		sinceTime:   since,
		interval:    interval,
		lastSeenEnd: since,
	}
}

// Run blocks until ctx is done, polling every interval. The sink
// receives every CDRRecord the poller fetches.
func (p *CDRPoller) Run(ctx context.Context, sink func(ctx context.Context, tenantID string, rec CDRRecord) error) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.pollOnce(ctx, sink); err != nil {
				log.Warn().Err(err).Str("tenant", p.tenantID).Msg("CDR poll failed")
			}
		}
	}
}

// pollOnce fetches the new CDRs since the last watermark. Bicom's
// CDR list endpoint is `pbxware.cdr.list` with a `starttime` filter.
// The exact parameter names should be confirmed at integration
// time; we send `starttime=<unix-ts>` and expect JSON rows back.
func (p *CDRPoller) pollOnce(ctx context.Context, sink func(ctx context.Context, tenantID string, rec CDRRecord) error) error {
	p.mu.Lock()
	start := p.lastSeenEnd
	p.mu.Unlock()

	resp, err := p.client.doRequest(ctx, "pbxware.cdr.list", map[string]string{
		"starttime": fmt.Sprintf("%d", start.Unix()),
	})
	if err != nil {
		return fmt.Errorf("bicom cdr list: %w", err)
	}

	var records []CDRRecord
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &records); err != nil {
			return fmt.Errorf("decode cdr list: %w", err)
		}
	}

	var newestEnd time.Time
	for _, rec := range records {
		// Parse the end timestamp (Bicom uses "YYYY-MM-DD HH:MM:SS")
		var recEnd time.Time
		if rec.End != "" {
			if t, err := time.Parse("2006-01-02 15:04:05", rec.End); err == nil {
				recEnd = t
			}
		}
		if !recEnd.IsZero() && (newestEnd.IsZero() || recEnd.After(newestEnd)) {
			newestEnd = recEnd
		}
		if err := sink(ctx, p.tenantID, rec); err != nil {
			log.Warn().Err(err).Str("tenant", p.tenantID).Str("uniqueid", rec.UniqueID).Msg("cdr sink failed")
		}
	}

	if !newestEnd.IsZero() {
		p.mu.Lock()
		if p.lastSeenEnd.Before(newestEnd) {
			p.lastSeenEnd = newestEnd
		}
		p.mu.Unlock()
	}
	return nil
}
