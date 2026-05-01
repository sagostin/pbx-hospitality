package tenant

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/config"
	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/metrics"
	"github.com/sagostin/pbx-hospitality/internal/pbx"
	_ "github.com/sagostin/pbx-hospitality/internal/pbx/bicom" // Register Bicom provider
	"github.com/sagostin/pbx-hospitality/internal/pms"
)

// Manager manages all tenant instances
type Manager struct {
	cfg      *config.Config
	database *db.DB
	tenants  map[string]*Tenant
	mu       sync.RWMutex
}

// NewManager creates a new tenant manager
func NewManager(cfg *config.Config, database *db.DB) (*Manager, error) {
	m := &Manager{
		cfg:      cfg,
		database: database,
		tenants:  make(map[string]*Tenant),
	}

	// Initialize tenants from config
	for _, tc := range cfg.Tenants {
		t, err := NewTenant(tc, database)
		if err != nil {
			return nil, err
		}
		m.tenants[tc.ID] = t
	}

	return m, nil
}

// StartAll starts all tenant services
func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for id, t := range m.tenants {
		log.Info().Str("tenant", id).Msg("Starting tenant")
		if err := t.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// StopAll stops all tenant services
func (m *Manager) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for id, t := range m.tenants {
		log.Info().Str("tenant", id).Msg("Stopping tenant")
		t.Stop()
	}
}

// Get returns a tenant by ID
func (m *Manager) Get(id string) (*Tenant, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tenants[id]
	return t, ok
}

// List returns all tenant IDs
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.tenants))
	for id := range m.tenants {
		ids = append(ids, id)
	}
	return ids
}

// Reload updates tenant configurations without full restart
// - Stops tenants that were removed from config
// - Starts new tenants added to config
// - Updates settings for existing tenants (reconnects if needed)
func (m *Manager) Reload(newCfg *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Build set of new tenant IDs
	newTenantIDs := make(map[string]config.TenantConfig)
	for _, tc := range newCfg.Tenants {
		// Skip disabled tenants
		if tc.Enabled != nil && !*tc.Enabled {
			continue
		}
		newTenantIDs[tc.ID] = tc
	}

	// Stop and remove tenants that no longer exist in config
	for id, t := range m.tenants {
		if _, exists := newTenantIDs[id]; !exists {
			log.Info().Str("tenant", id).Msg("Tenant removed from config, stopping")
			t.Stop()
			delete(m.tenants, id)
		}
	}

	// Add or update tenants
	ctx := context.Background()
	for id, tc := range newTenantIDs {
		if existing, exists := m.tenants[id]; exists {
			// Tenant exists - check if config changed
			if existing.cfg.PMS.Host != tc.PMS.Host || existing.cfg.PMS.Port != tc.PMS.Port {
				// PMS config changed - restart tenant
				log.Info().Str("tenant", id).Msg("PMS config changed, reconnecting")
				existing.Stop()
				newTenant, err := NewTenant(tc)
				if err != nil {
					log.Error().Err(err).Str("tenant", id).Msg("Failed to recreate tenant")
					continue
				}
				if err := newTenant.Start(ctx); err != nil {
					log.Error().Err(err).Str("tenant", id).Msg("Failed to restart tenant")
					continue
				}
				m.tenants[id] = newTenant
			} else {
				// Just update config fields that don't require restart
				existing.Name = tc.Name
				existing.cfg = tc
				log.Debug().Str("tenant", id).Msg("Tenant config updated")
			}
		} else {
			// New tenant - create and start
			log.Info().Str("tenant", id).Msg("New tenant in config, starting")
			newTenant, err := NewTenant(tc, m.database)
			if err != nil {
				log.Error().Err(err).Str("tenant", id).Msg("Failed to create new tenant")
				continue
			}
			if err := newTenant.Start(ctx); err != nil {
				log.Error().Err(err).Str("tenant", id).Msg("Failed to start new tenant")
				continue
			}
			m.tenants[id] = newTenant
		}
	}

	m.cfg = newCfg
	return nil
}

// Tenant represents a single hotel/property integration
type Tenant struct {
	ID          string
	Name        string
	cfg         config.TenantConfig
	database    *db.DB
	pmsAdapter  pms.Adapter
	pbxProvider pbx.Provider // PBX provider (Bicom, FreeSWITCH, etc.)
	mapper      *RoomMapper
	timezone    *time.Location // Tenant's configured timezone
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// NewTenant creates a new tenant instance
func NewTenant(cfg config.TenantConfig, database *db.DB) (*Tenant, error) {
	// Load timezone (default to UTC if not specified or invalid)
	var tz *time.Location = time.UTC
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			log.Warn().Str("tenant", cfg.ID).Str("timezone", cfg.Timezone).Msg("Invalid timezone, using UTC")
		} else {
			tz = loc
			log.Debug().Str("tenant", cfg.ID).Str("timezone", cfg.Timezone).Msg("Tenant timezone loaded")
		}
	}

	t := &Tenant{
		ID:       cfg.ID,
		Name:     cfg.Name,
		cfg:      cfg,
		database: database,
		mapper:   NewRoomMapper(cfg.RoomPrefix),
		timezone: tz,
	}
	return t, nil
}

// Start initializes and starts the tenant services
func (t *Tenant) Start(ctx context.Context) error {
	ctx, t.cancel = context.WithCancel(ctx)

	// Initialize PMS adapter
	adapter, err := pms.NewAdapter(t.cfg.PMS.Protocol, t.cfg.PMS.Host, t.cfg.PMS.Port)
	if err != nil {
		return err
	}
	t.pmsAdapter = adapter

	// Connect to PMS
	if err := t.pmsAdapter.Connect(ctx); err != nil {
		return err
	}

	// Initialize PBX provider using registry
	pbxType := t.cfg.PBX.Type
	if pbxType == "" {
		pbxType = "bicom" // Default to Bicom for backward compatibility
	}

	t.pbxProvider, err = pbx.NewProvider(pbx.ProviderConfig{
		Type: pbxType,
		// Bicom-specific
		BicomAPIURL:   t.cfg.PBX.APIURL,
		BicomAPIKey:   t.cfg.PBX.APIKey,
		BicomTenantID: t.cfg.PBX.TenantID,
		// ARI settings
		ARIURL:     t.cfg.PBX.ARIURL,
		ARIWSUrl:   t.cfg.PBX.ARIWSUrl,
		ARIUser:    t.cfg.PBX.ARIUser,
		ARIPass:    t.cfg.PBX.ARIPass,
		ARIAppName: t.cfg.PBX.AppName,
		// Zultys-specific
		APIURL:        t.cfg.PBX.APIURL,
		AuthURL:       t.cfg.PBX.AuthURL,
		Username:      t.cfg.PBX.Username,
		Password:      t.cfg.PBX.Password,
		WebhookSecret: t.cfg.PBX.WebhookSecret,
	})
	if err != nil {
		return fmt.Errorf("creating PBX provider (%s): %w", pbxType, err)
	}

	// Connect to PBX
	if err := t.pbxProvider.Connect(ctx); err != nil {
		return fmt.Errorf("connecting to PBX: %w", err)
	}

	log.Info().
		Str("tenant", t.ID).
		Str("pbx_type", pbxType).
		Msg("PBX provider initialized")

	// Start event processing loop
	t.wg.Add(1)
	go t.processEvents(ctx)

	return nil
}

// Stop terminates the tenant services
func (t *Tenant) Stop() {
	if t.cancel != nil {
		t.cancel()
	}

	if t.pmsAdapter != nil {
		t.pmsAdapter.Close()
	}

	if t.pbxProvider != nil {
		t.pbxProvider.Close()
	}

	t.wg.Wait()
}

// processEvents handles incoming PMS events
func (t *Tenant) processEvents(ctx context.Context) {
	defer t.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-t.pmsAdapter.Events():
			if !ok {
				return
			}
			t.handleEvent(ctx, evt)
		}
	}
}

// handleEvent processes a single PMS event
func (t *Tenant) handleEvent(ctx context.Context, evt pms.Event) {
	start := time.Now()
	eventType := evt.Type.String()

	// Record event received
	metrics.PMSEventsTotal.WithLabelValues(t.ID, eventType).Inc()

	log := log.With().
		Str("tenant", t.ID).
		Str("event", eventType).
		Str("room", evt.Room).
		Logger()

	// Map room to extension
	ext, err := t.mapper.GetExtension(evt.Room)
	if err != nil {
		log.Error().Err(err).Msg("Failed to map room to extension")
		metrics.PMSEventErrors.WithLabelValues(t.ID, eventType, "mapping").Inc()
		return
	}

	log = log.With().Str("extension", ext).Logger()
	log.Info().Msg("Processing PMS event")

	switch evt.Type {
	case pms.EventCheckIn:
		t.handleCheckIn(ctx, ext, evt, log)

	case pms.EventCheckOut:
		t.handleCheckOut(ctx, ext, evt, log)

	case pms.EventMessageWaiting:
		// MWI control via PBX provider
		if err := t.pbxProvider.SetMWI(ctx, ext, evt.Status); err != nil {
			log.Error().Err(err).Msg("Failed to set MWI")
		}

	case pms.EventNameUpdate:
		// Update extension name via PBX provider
		if err := t.pbxProvider.UpdateExtensionName(ctx, ext, evt.GuestName); err != nil {
			log.Error().Err(err).Msg("Failed to update extension name")
		}

	case pms.EventDND:
		// Set DND via PBX provider
		if err := t.pbxProvider.SetDND(ctx, ext, evt.Status); err != nil {
			log.Error().Err(err).Msg("Failed to set DND")
		}

	case pms.EventWakeUp:
		// Handle wake-up call scheduling from PMS
		t.handleWakeUp(ctx, ext, evt, log)

	default:
		log.Warn().Msg("Unhandled event type")
	}

	// Acknowledge the event
	if err := t.pmsAdapter.SendAck(); err != nil {
		log.Error().Err(err).Msg("Failed to send ACK")
	}

	// Record processing duration
	metrics.PMSEventDuration.WithLabelValues(t.ID, eventType).Observe(time.Since(start).Seconds())
}

// handleCheckIn handles guest check-in event
func (t *Tenant) handleCheckIn(ctx context.Context, ext string, evt pms.Event, log zerolog.Logger) {
	// Update extension name via PBX provider
	if err := t.pbxProvider.UpdateExtensionName(ctx, ext, evt.GuestName); err != nil {
		log.Error().Err(err).Msg("Failed to set extension name")
	} else {
		log.Info().Str("guest", evt.GuestName).Msg("Guest checked in, extension name updated")
	}

	// Persist guest session to database
	if t.database != nil {
		reservationID := evt.Metadata["reservation_id"]
		if _, err := t.database.CreateGuestSession(ctx, t.ID, evt.Room, ext, evt.GuestName, reservationID, map[string]interface{}{}); err != nil {
			log.Error().Err(err).Msg("Failed to persist guest session to database")
		} else {
			log.Info().Str("reservation_id", reservationID).Msg("Guest session persisted to database")
		}
	}
}

// handleCheckOut handles guest check-out event
func (t *Tenant) handleCheckOut(ctx context.Context, ext string, evt pms.Event, log zerolog.Logger) {
	// Clear extension name
	if err := t.pbxProvider.UpdateExtensionName(ctx, ext, ""); err != nil {
		log.Error().Err(err).Msg("Failed to clear extension name")
	}

	// Delete all voicemails and reset greeting
	if err := t.pbxProvider.ClearVoicemailForGuest(ctx, ext); err != nil {
		log.Error().Err(err).Msg("Failed to clear voicemail for guest")
	} else {
		log.Info().Msg("Guest checked out, voicemails and greeting cleared")
	}

	// Cancel any scheduled wake-up calls
	if err := t.pbxProvider.CancelWakeUpCall(ctx, ext); err != nil {
		log.Debug().Err(err).Msg("Failed to cancel wake-up call (may not exist)")
	}

	// Clear MWI lamp
	if err := t.pbxProvider.SetMWI(ctx, ext, false); err != nil {
		log.Error().Err(err).Msg("Failed to clear MWI")
	}

	// End guest session in database
	if t.database != nil {
		if err := t.database.EndGuestSession(ctx, t.ID, evt.Room); err != nil {
			log.Error().Err(err).Msg("Failed to end guest session in database")
		} else {
			log.Info().Msg("Guest session ended in database")
		}
	}
}

// handleWakeUp handles wake-up call scheduling from PMS
func (t *Tenant) handleWakeUp(ctx context.Context, ext string, evt pms.Event, log zerolog.Logger) {
	// Check if wake-up time is in metadata (FIAS uses TI field, format HHMM)
	wakeTimeStr, ok := evt.Metadata["TI"]
	if !ok || wakeTimeStr == "" {
		log.Warn().Msg("Wake-up call requested but no time specified")
		return
	}

	// Parse the wake-up time (format: HHMM)
	wakeTime, err := t.parseWakeUpTime(wakeTimeStr)
	if err != nil {
		log.Error().Err(err).Str("time", wakeTimeStr).Msg("Failed to parse wake-up time")
		return
	}

	// Schedule the wake-up call via PBX provider
	if err := t.pbxProvider.ScheduleWakeUpCall(ctx, ext, wakeTime); err != nil {
		log.Error().Err(err).Str("time", wakeTime.Format("15:04")).Msg("Failed to schedule wake-up call")
		metrics.PMSEventErrors.WithLabelValues(t.ID, "WakeUp", "pbx_provider").Inc()
	} else {
		log.Info().
			Str("time", wakeTime.Format("15:04")).
			Str("timezone", t.timezone.String()).
			Msg("Wake-up call scheduled")
	}
}

// parseWakeUpTime parses HHMM format time string into a time.Time in tenant timezone
// If the time has already passed today, it schedules for tomorrow
func (t *Tenant) parseWakeUpTime(timeStr string) (time.Time, error) {
	// Normalize the time string (HHMM or HH:MM)
	timeStr = strings.ReplaceAll(timeStr, ":", "")
	if len(timeStr) != 4 {
		return time.Time{}, fmt.Errorf("invalid time format: %s (expected HHMM)", timeStr)
	}

	hour := timeStr[0:2]
	minute := timeStr[2:4]

	// Parse hours and minutes
	h, err := strconv.Atoi(hour)
	if err != nil || h < 0 || h > 23 {
		return time.Time{}, fmt.Errorf("invalid hour: %s", hour)
	}
	m, err := strconv.Atoi(minute)
	if err != nil || m < 0 || m > 59 {
		return time.Time{}, fmt.Errorf("invalid minute: %s", minute)
	}

	// Get current time in tenant's timezone
	now := time.Now().In(t.timezone)

	// Create wake-up time for today in tenant timezone
	wakeTime := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, t.timezone)

	// If the time has already passed today, schedule for tomorrow
	if wakeTime.Before(now) {
		wakeTime = wakeTime.Add(24 * time.Hour)
	}

	return wakeTime, nil
}

// PBXProvider returns the tenant's PBX provider for webhook handling
func (t *Tenant) PBXProvider() pbx.Provider {
	return t.pbxProvider
}

// Status returns the tenant's current status
func (t *Tenant) Status() TenantStatus {
	return TenantStatus{
		ID:           t.ID,
		Name:         t.Name,
		PMSConnected: t.pmsAdapter != nil && t.pmsAdapter.Connected(),
		PBXConnected: t.pbxProvider != nil && t.pbxProvider.Connected(),
	}
}

// TenantStatus represents the current state of a tenant
type TenantStatus struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PMSConnected bool   `json:"pms_connected"`
	PBXConnected bool   `json:"pbx_connected"`
}
