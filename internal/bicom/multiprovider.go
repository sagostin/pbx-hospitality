package bicom

import (
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type Endpoint struct {
	ID            string
	APIURL        string
	APIKey        string
	TenantID      string
	ARIURL        string
	ARIWSUrl      string
	ARIUser       string
	ARIPass       string
	ARIAppName    string
	WebhookSecret string
	Priority      int
	Failover      bool
	healthy       bool
	lastCheck     time.Time
}

type MultiProvider struct {
	endpoints []*Endpoint
	mu        sync.RWMutex
	current   int
}

func NewMultiProvider(endpoints []Endpoint) *MultiProvider {
	mp := &MultiProvider{
		endpoints: make([]*Endpoint, len(endpoints)),
	}
	for i := range endpoints {
		e := endpoints[i]
		e.healthy = true
		e.lastCheck = time.Now()
		mp.endpoints[i] = &e
	}
	return mp
}

func (m *MultiProvider) GetBestEndpoint() (*Endpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.endpoints) == 0 {
		return nil, fmt.Errorf("no bicom endpoints configured")
	}

	var best *Endpoint
	for _, ep := range m.endpoints {
		if !ep.Failover && !ep.healthy {
			continue
		}
		if best == nil || ep.Priority < best.Priority {
			best = ep
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no healthy bicom endpoints available")
	}
	return best, nil
}

func (m *MultiProvider) GetNextEndpoint() (*Endpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.endpoints) == 0 {
		return nil, fmt.Errorf("no bicom endpoints configured")
	}

	start := m.current
	for i := 0; i < len(m.endpoints); i++ {
		idx := (start + i) % len(m.endpoints)
		ep := m.endpoints[idx]
		if ep.healthy {
			m.current = (idx + 1) % len(m.endpoints)
			return ep, nil
		}
	}
	return nil, fmt.Errorf("no healthy bicom endpoints available")
}

func (m *MultiProvider) MarkEndpointHealthy(id string, healthy bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ep := range m.endpoints {
		if ep.ID == id {
			ep.healthy = healthy
			ep.lastCheck = time.Now()
			log.Info().Str("endpoint", id).Bool("healthy", healthy).Msg("Bicom endpoint health updated")
			return
		}
	}
}

func (m *MultiProvider) Endpoints() []*Endpoint {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Endpoint, len(m.endpoints))
	copy(result, m.endpoints)
	return result
}

type LoadBalancer interface {
	SelectEndpoint() (*Endpoint, error)
}

func (m *MultiProvider) SelectEndpoint() (*Endpoint, error) {
	return m.GetNextEndpoint()
}