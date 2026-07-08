// Package router is the per-tenant event router. It composes an
// inbound source (a pms.Adapter) with an outbound dispatcher so a
// tenant's pipeline is fully described by its configuration, not by
// hard-coded wiring.
//
// The point of the router is that swapping a tenant between PMS
// providers (e.g. TigerTMS on-prem → TigerTMS cloud, or Bicom
// → Zultys) is a config change, not a code change. The router
// registry keeps an entry per tenant and the pipeline runs
// asynchronously off the inbound events channel.
package router

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/outbound"
	"github.com/sagostin/pbx-hospitality/internal/pms"
)

// OutboundEvent is a per-tenant event ready to be relayed outbound.
// Producers construct one and pass it to Router.Enqueue.
type OutboundEvent struct {
	TenantID       string
	EventType      string                 // 'cdr_posted', 'wakeup_completed', ...
	IdempotencyKey string                 // dedup key
	TargetURL      string                 // override; if empty, derived from tenant config
	TargetStrategy string                 // 'ilink_cdr', 'cloud_hmac', ...
	Payload        map[string]interface{} // event body
}

// TenantConfig is the per-tenant pipeline description. The router
// loads this from the DB on tenant enable / reload.
type TenantConfig struct {
	TenantID        string
	Enabled         bool
	InboundProtocol string                 // 'tigertms', 'mitel', 'fias', ...
	InboundSiteID   string                 // for tigertms: siteid (informational)
	InboundConfig   map[string]interface{} // protocol-specific config
	PBXType         string                 // 'bicom', 'zultys', ...
	PBXConfig       map[string]interface{} // PBX provider config

	OutboundEnabled   bool
	OutboundURL       string   // base URL
	OutboundStrategy  string   // default strategy
	OutboundEvents    []string // events this tenant wants emitted
	OutboundSecretRef string   // FK into encrypted_secrets
}

// Router owns the per-tenant pipeline: an inbound adapter goroutine +
// a set of outbound producers. The router is the integration point
// for the dispatcher; producers call Router.Enqueue to publish.
//
// `database` may be nil — when nil, the router uses the dispatcher's
// own store for enqueue. That keeps the router testable without a
// real DB (we wire a real outbound.Dispatcher with a fake store).
type Router struct {
	database   *db.DB
	dispatcher *outbound.Dispatcher

	mu      sync.RWMutex
	tenants map[string]*pipeline // tenantID → pipeline
}

// pipeline is the per-tenant goroutine + producers.
type pipeline struct {
	cfg      TenantConfig
	cancel   context.CancelFunc
	done     chan struct{}
	producer *outbound.Producer
}

// NewRouter creates an empty router. Call AddTenant / RemoveTenant
// per tenant lifecycle.
func NewRouter(database *db.DB, dispatcher *outbound.Dispatcher) *Router {
	return &Router{
		database:   database,
		dispatcher: dispatcher,
		tenants:    make(map[string]*pipeline),
	}
}

// store returns the DispatcherStore to enqueue into. Prefers the
// router's database; falls back to the dispatcher's store (useful
// for tests and for code paths that don't carry the DB handle).
func (r *Router) store() outbound.DispatcherStore {
	if r.database != nil {
		return r.database
	}
	return r.dispatcher.Store()
}

// AddTenant starts a pipeline for a tenant. If a pipeline already
// exists for the tenant it is stopped and replaced — used by
// reload flows.
func (r *Router) AddTenant(ctx context.Context, cfg TenantConfig, adapter pms.Adapter) {
	if !cfg.Enabled {
		r.RemoveTenant(cfg.TenantID)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.tenants[cfg.TenantID]; ok {
		existing.cancel()
		<-existing.done
	}

	pctx, cancel := context.WithCancel(ctx)
	p := &pipeline{
		cfg:      cfg,
		cancel:   cancel,
		done:     make(chan struct{}),
		producer: &outbound.Producer{Store: r.dispatcher.Store()},
	}
	r.tenants[cfg.TenantID] = p

	go r.run(pctx, p, adapter)
	log.Info().
		Str("tenant", cfg.TenantID).
		Str("inbound", cfg.InboundProtocol).
		Str("outbound_strategy", cfg.OutboundStrategy).
		Msg("Tenant pipeline started")
}

// RemoveTenant stops the pipeline for a tenant.
func (r *Router) RemoveTenant(tenantID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.tenants[tenantID]; ok {
		existing.cancel()
		<-existing.done
		delete(r.tenants, tenantID)
	}
}

// StopAll stops every tenant pipeline. Called at shutdown.
func (r *Router) StopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for tid, p := range r.tenants {
		p.cancel()
		<-p.done
		delete(r.tenants, tid)
	}
}

// Enqueue publishes an outbound event for a tenant. Returns an
// error if the tenant's pipeline is not running or the dispatcher
// rejects the event.
func (r *Router) Enqueue(ctx context.Context, evt OutboundEvent) error {
	r.mu.RLock()
	p, ok := r.tenants[evt.TenantID]
	r.mu.RUnlock()
	if !ok {
		return &NotRunningError{TenantID: evt.TenantID}
	}
	url := evt.TargetURL
	if url == "" {
		url = p.cfg.OutboundURL
	}
	strategy := evt.TargetStrategy
	if strategy == "" {
		strategy = p.cfg.OutboundStrategy
	}
	if strategy == "" {
		strategy = db.OutboundStrategyILinkCDR
	}

	// Hand off to the dispatcher's store. The store enforces
	// idempotency. We don't reach into the dispatcher here because
	// we want the dispatcher worker pool to own retries.
	row := &db.OutboundWebhook{
		TenantID:       evt.TenantID,
		EventType:      evt.EventType,
		IdempotencyKey: evt.IdempotencyKey,
		TargetURL:      url,
		TargetStrategy: strategy,
		Payload:        marshalPayload(evt.Payload),
		Status:         db.OutboundStatusQueued,
		NextAttemptAt:  time.Now(),
	}
	_, err := r.store().EnqueueOutboundWebhook(ctx, row)
	return err
}

// run is the per-tenant goroutine that drains the inbound adapter's
// events channel. Today it just logs — the actual PBX side effects
// happen in tenant.Manager (which reads the same channels). This
// goroutine exists as the integration point for future cross-cutting
// outbound events that should fire in response to any inbound event
// (audit log shipping, etc.) without modifying tenant.Manager.
//
// adapter may be nil (e.g. in tests) — in that case the goroutine
// just blocks on ctx.Done.
func (r *Router) run(ctx context.Context, p *pipeline, adapter pms.Adapter) {
	defer close(p.done)

	if adapter == nil {
		<-ctx.Done()
		return
	}
	events := adapter.Events()
	if events == nil {
		<-ctx.Done()
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			// Per-tenant cross-cutting hooks (audit, metering, etc.)
			// would live here. Outbound event emission is driven by
			// specific tenant event producers, not a generic drain.
		}
	}
}

// marshalPayload JSON-encodes the payload map for storage in
// outbound_webhooks.payload (jsonb).
func marshalPayload(p map[string]interface{}) string {
	if len(p) == 0 {
		return "{}"
	}
	// Tiny inline JSON to avoid importing encoding/json in the hot
	// path; the dispatcher handles full validation.
	// For real production this would json.Marshal; the cost is
	// negligible relative to the HTTP POST.
	out, err := jsonMarshal(p)
	if err != nil {
		log.Warn().Err(err).Msg("router: marshal payload failed")
		return "{}"
	}
	return out
}

// NotRunningError is returned by Enqueue when the tenant has no
// running pipeline.
type NotRunningError struct {
	TenantID string
}

func (e *NotRunningError) Error() string {
	return "tenant pipeline not running: " + e.TenantID
}
