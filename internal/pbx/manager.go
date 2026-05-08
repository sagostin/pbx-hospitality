package pbx

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/db"
)

type ConnectedPBX struct {
	ID       string
	Provider Provider
	State    ConnectionState
	LastSeen time.Time
}

type ConnectionState int

const (
	StateDisconnected ConnectionState = iota
	StateConnecting
	StateConnected
	StateReconnecting
)

func (s ConnectionState) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateReconnecting:
		return "reconnecting"
	default:
		return "unknown"
	}
}

type Manager struct {
	db      *db.DB
	systems map[string]*ConnectedPBX
	mu      sync.RWMutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewManager(db *db.DB) *Manager {
	return &Manager{
		db:      db,
		systems: make(map[string]*ConnectedPBX),
	}
}

func (m *Manager) LoadFromDB(ctx context.Context) error {
	if m.db == nil {
		log.Warn().Msg("PBXManager: no database configured")
		return nil
	}

	systems, err := m.db.ListBicomSystems(ctx)
	if err != nil {
		return fmt.Errorf("listing bicom systems: %w", err)
	}

	for _, sys := range systems {
		if !sys.Enabled {
			continue
		}
		if err := m.Connect(ctx, sys.ID); err != nil {
			log.Error().Err(err).Str("system", sys.ID).Msg("Failed to connect bicom system")
		}
	}

	return nil
}

func (m *Manager) Connect(ctx context.Context, systemID string) error {
	if m.db == nil {
		return fmt.Errorf("no database configured")
	}

	system, err := m.db.GetBicomSystem(ctx, systemID)
	if err != nil {
		return fmt.Errorf("getting bicom system: %w", err)
	}
	if system == nil {
		return fmt.Errorf("bicom system not found: %s", systemID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.systems[systemID]; ok && existing.State == StateConnected {
		return nil
	}

	cfg := ProviderConfig{
		Type:          "bicom",
		BicomAPIURL:   system.APIURL,
		BicomAPIKey:   system.APIKey,
		BicomTenantID: system.TenantID,
		ARIURL:        system.ARIURL,
		ARIUser:       system.ARIUser,
		ARIPass:       system.ARIPass,
		ARIAppName:    system.ARIAppName,
		WebhookSecret: "",
	}

	provider, err := NewProvider(cfg)
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	connected := &ConnectedPBX{
		ID:       systemID,
		Provider: provider,
		State:    StateConnecting,
		LastSeen: time.Now(),
	}
	m.systems[systemID] = connected

	go func() {
		if err := provider.Connect(ctx); err != nil {
			log.Error().Err(err).Str("system", systemID).Msg("PBX connection failed")
			m.mu.Lock()
			if cp, ok := m.systems[systemID]; ok {
				cp.State = StateDisconnected
			}
			m.mu.Unlock()
			return
		}

		m.mu.Lock()
		if cp, ok := m.systems[systemID]; ok {
			cp.State = StateConnected
			cp.LastSeen = time.Now()
		}
		m.mu.Unlock()

		log.Info().Str("system", systemID).Msg("PBX connected")
	}()

	return nil
}

func (m *Manager) Disconnect(systemID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cp, ok := m.systems[systemID]
	if !ok {
		return nil
	}

	cp.State = StateDisconnected
	cp.Provider.Close()
	delete(m.systems, systemID)
	return nil
}

func (m *Manager) GetProvider(systemID string) (Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cp, ok := m.systems[systemID]
	if !ok || cp.State != StateConnected {
		return nil, false
	}
	return cp.Provider, true
}

func (m *Manager) ReloadSystem(ctx context.Context, systemID string) error {
	log.Info().Str("system", systemID).Msg("Reloading PBX system")
	if err := m.Disconnect(systemID); err != nil {
		log.Warn().Err(err).Str("system", systemID).Msg("Error disconnecting system")
	}
	return m.Connect(ctx, systemID)
}

func (m *Manager) ReloadFromDB(ctx context.Context) error {
	if m.db == nil {
		return fmt.Errorf("no database configured")
	}

	systems, err := m.db.ListBicomSystems(ctx)
	if err != nil {
		return fmt.Errorf("listing bicom systems: %w", err)
	}

	newIDs := make(map[string]bool)
	for _, sys := range systems {
		if sys.Enabled {
			newIDs[sys.ID] = true
		}
	}

	m.mu.Lock()
	for id := range m.systems {
		if !newIDs[id] {
			log.Info().Str("system", id).Msg("PBX system removed from DB, disconnecting")
			if cp, ok := m.systems[id]; ok {
				cp.State = StateDisconnected
				cp.Provider.Close()
				delete(m.systems, id)
			}
		}
	}

	for _, sys := range systems {
		if !sys.Enabled {
			continue
		}
		if _, exists := m.systems[sys.ID]; !exists {
			m.mu.Unlock()
			if err := m.Connect(ctx, sys.ID); err != nil {
				log.Error().Err(err).Str("system", sys.ID).Msg("Failed to connect new system")
			}
			m.mu.Lock()
		}
	}
	m.mu.Unlock()

	return nil
}

func (m *Manager) Status() []PBXStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make([]PBXStatus, 0, len(m.systems))
	for id, cp := range m.systems {
		statuses = append(statuses, PBXStatus{
			SystemID: id,
			State:    cp.State.String(),
			LastSeen: cp.LastSeen,
		})
	}
	return statuses
}

func (m *Manager) Close() {
	if m.cancel != nil {
		m.cancel()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, cp := range m.systems {
		cp.State = StateDisconnected
		cp.Provider.Close()
		delete(m.systems, id)
	}
}

type PBXStatus struct {
	SystemID string    `json:"system_id"`
	State    string    `json:"state"`
	LastSeen time.Time `json:"last_seen"`
}
