// Package outbound is the per-tenant event dispatcher. Producers
// (CDR builder, wake-up outcome, voicemail left, access-code dial)
// call Enqueue to insert a row into outbound_webhooks. The worker
// pool drains due rows, posts to the receiver with the strategy's
// signing, marks the row sent on 2xx, and reschedules with backoff
// on failure.
//
// Strategies are pluggable — add a new SigningStrategy to support a
// new receiver protocol (ilink_cdr is shipped today).
package outbound

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/db"
)

// SigningStrategy applies the appropriate auth / signing to a request
// before it is sent. The strategy reads whatever secrets it needs from
// the SecretResolver (which in turn looks up encrypted_secrets).
type SigningStrategy interface {
	// Name returns the strategy identifier persisted in
	// outbound_webhooks.target_strategy.
	Name() string

	// Apply mutates the request (adds Authorization, body wrapping,
	// etc.) and returns a possibly-modified body. idemKey is the
	// idempotency key from the row; signing strategies that
	// implement HMAC can include it in the signature.
	Apply(req *http.Request, body []byte, idemKey string, secrets SecretResolver) error

	// AcceptResponse declares whether a response counts as success.
	// Default: any 2xx. Strategies that need to inspect body shape
	// (e.g. iLink CDR's `{"response":"RECEIVEDOK"}`) implement this
	// directly on the strategy struct.
	AcceptResponse(resp *http.Response, body []byte) bool
}

// SecretResolver looks up secrets by name. Returns nil + nil for
// optional secrets; returns an error only when a required secret is
// missing.
type SecretResolver interface {
	Resolve(secretRef string) ([]byte, error)
}

// httpClient is overridable for tests.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// Dispatcher drains outbound_webhooks. One dispatcher per process;
// multiple goroutines cooperate via DB-side row claims.
//
// The dispatcher depends on the DispatcherStore interface (satisfied
// by *db.DB) so tests can plug in an in-memory store without a real
// database driver.
type Dispatcher struct {
	store         DispatcherStore
	strategies    map[string]SigningStrategy
	defaultSecret SecretResolver

	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
	tick        time.Duration
	batchSize   int
	concurrency int

	mu      sync.Mutex
	stopped bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// DispatcherStore is the persistence contract the dispatcher needs.
// *db.DB implements this; tests can implement a fake.
type DispatcherStore interface {
	ClaimDueOutboundWebhooks(ctx context.Context, now time.Time, limit int) ([]db.OutboundWebhook, error)
	MarkOutboundSent(ctx context.Context, id int64) error
	MarkOutboundFailed(ctx context.Context, id int64, errMsg string, nextAttemptAt time.Time, maxAttempts int) error
	EnqueueOutboundWebhook(ctx context.Context, w *db.OutboundWebhook) (int64, error)
	ListOutboundWebhooksForTenant(ctx context.Context, tenantID string, limit int) ([]db.OutboundWebhook, error)
}

// NewDispatcher creates a dispatcher. Caller must call Start.
func NewDispatcher(store DispatcherStore, secrets SecretResolver) *Dispatcher {
	d := &Dispatcher{
		store:         store,
		strategies:    make(map[string]SigningStrategy),
		defaultSecret: secrets,
		maxAttempts:   10,
		baseBackoff:   1 * time.Second,
		maxBackoff:    5 * time.Minute,
		tick:          5 * time.Second,
		batchSize:     25,
		concurrency:   4,
	}
	d.RegisterStrategy(ILinkCDRStrategy{})
	d.RegisterStrategy(CloudHmacStrategy{})
	d.RegisterStrategy(CloudBearerStrategy{})
	return d
}

// Store returns the DispatcherStore the dispatcher was constructed
// with. Useful for code (e.g. the router) that wants to enqueue
// rows without re-implementing idempotency.
func (d *Dispatcher) Store() DispatcherStore { return d.store }

// RegisterStrategy adds (or replaces) a signing strategy by name.
func (d *Dispatcher) RegisterStrategy(s SigningStrategy) {
	d.mu.Lock()
	d.strategies[s.Name()] = s
	d.mu.Unlock()
}

// WithOptions tunes dispatcher tunables. Chainable.
func (d *Dispatcher) WithOptions(maxAttempts int, baseBackoff, maxBackoff, tick time.Duration, batchSize, concurrency int) *Dispatcher {
	if maxAttempts > 0 {
		d.maxAttempts = maxAttempts
	}
	if baseBackoff > 0 {
		d.baseBackoff = baseBackoff
	}
	if maxBackoff > 0 {
		d.maxBackoff = maxBackoff
	}
	if tick > 0 {
		d.tick = tick
	}
	if batchSize > 0 {
		d.batchSize = batchSize
	}
	if concurrency > 0 {
		d.concurrency = concurrency
	}
	return d
}

// Start launches worker goroutines and the periodic ticker. Returns
// immediately; call Stop to terminate.
func (d *Dispatcher) Start(ctx context.Context) {
	ctx, d.cancel = context.WithCancel(ctx)
	d.wg.Add(1)
	go d.loop(ctx)
	log.Info().
		Dur("tick", d.tick).
		Int("concurrency", d.concurrency).
		Int("batch", d.batchSize).
		Msg("Outbound dispatcher started")
}

// Stop signals the dispatcher to terminate and waits for in-flight
// deliveries.
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	if d.cancel != nil {
		d.cancel()
	}
	d.mu.Unlock()
	d.wg.Wait()
	log.Info().Msg("Outbound dispatcher stopped")
}

func (d *Dispatcher) loop(ctx context.Context) {
	defer d.wg.Done()
	ticker := time.NewTicker(d.tick)
	defer ticker.Stop()

	// Kick off an immediate drain so we don't wait one tick after startup.
	d.drainBatch(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.drainBatch(ctx)
		}
	}
}

func (d *Dispatcher) drainBatch(ctx context.Context) {
	rows, err := d.store.ClaimDueOutboundWebhooks(ctx, time.Now(), d.batchSize)
	if err != nil {
		log.Error().Err(err).Msg("Outbound: claim due rows failed")
		return
	}
	if len(rows) == 0 {
		return
	}
	// Fan out deliveries with a small concurrency cap.
	sem := make(chan struct{}, d.concurrency)
	var wg sync.WaitGroup
	for _, row := range rows {
		row := row
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d.deliver(ctx, row)
		}()
	}
	wg.Wait()
}

// DrainOnceForTest runs a single drain cycle synchronously. Tests use
// this instead of waiting for the periodic ticker.
func (d *Dispatcher) DrainOnceForTest() {
	d.drainBatch(context.Background())
}

func (d *Dispatcher) deliver(ctx context.Context, row db.OutboundWebhook) {
	strat, ok := d.strategies[row.TargetStrategy]
	if !ok {
		// Unknown strategy — drop after one error so we don't loop forever.
		_ = d.store.MarkOutboundFailed(ctx, row.ID,
			fmt.Sprintf("unknown target_strategy %q", row.TargetStrategy),
			time.Now().Add(time.Hour), 1)
		log.Error().
			Int64("id", row.ID).
			Str("strategy", row.TargetStrategy).
			Msg("Outbound: unknown strategy, dropping row")
		return
	}

	body := []byte(row.Payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, row.TargetURL, bytes.NewReader(body))
	if err != nil {
		d.reschedule(ctx, row, fmt.Sprintf("build request: %v", err))
		return
	}
	req.Header.Set("Content-Type", "text/json")
	req.Header.Set("User-Agent", "pbx-hospitality-outbound/1")

	if err := strat.Apply(req, body, row.IdempotencyKey, d.defaultSecret); err != nil {
		d.reschedule(ctx, row, fmt.Sprintf("sign: %v", err))
		return
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		d.reschedule(ctx, row, fmt.Sprintf("network: %v", err))
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if strat.AcceptResponse(resp, respBody) {
		if err := d.store.MarkOutboundSent(ctx, row.ID); err != nil {
			log.Error().Err(err).Int64("id", row.ID).Msg("Outbound: mark sent failed")
		}
		log.Info().
			Int64("id", row.ID).
			Str("tenant", row.TenantID).
			Str("event_type", row.EventType).
			Int("status", resp.StatusCode).
			Msg("Outbound: delivered")
		return
	}

	d.reschedule(ctx, row, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 256)))
}

// Responder is implemented by SigningStrategy to declare when a 2xx
// counts as success. Some receivers (e.g. iLink CDR) return 200 with a
// `{"response":"ERROR"}` body — the strategy treats that as failure
// even though the HTTP layer says success.
type Responder interface {
	AcceptResponse(resp *http.Response, body []byte) bool
}

// DefaultAcceptResponse treats any 2xx as success.
type DefaultAcceptResponse struct{}

func (DefaultAcceptResponse) AcceptResponse(resp *http.Response, _ []byte) bool {
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func (d *Dispatcher) reschedule(ctx context.Context, row db.OutboundWebhook, errMsg string) {
	next := nextBackoff(row.AttemptCount+1, d.baseBackoff, d.maxBackoff)
	if err := d.store.MarkOutboundFailed(ctx, row.ID, errMsg, next, d.maxAttempts); err != nil {
		log.Error().Err(err).Int64("id", row.ID).Msg("Outbound: mark failed failed")
	}
	log.Warn().
		Int64("id", row.ID).
		Str("tenant", row.TenantID).
		Str("event_type", row.EventType).
		Int("attempt", row.AttemptCount+1).
		Time("next_attempt_at", next).
		Str("error", errMsg).
		Msg("Outbound: delivery failed, rescheduled")
}

// nextBackoff returns the next retry time using exponential backoff
// with full jitter. attempt is 1-indexed.
func nextBackoff(attempt int, base, max time.Duration) time.Time {
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d > max {
			d = max
			break
		}
	}
	// Full jitter: random in [0, d]. We don't import math/rand here;
	// use time.Now().UnixNano() % int64(d) for simple jitter.
	jitterNanos := time.Now().UnixNano() % int64(d+1)
	return time.Now().Add(time.Duration(jitterNanos))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// =============================================================================
// ILink CDR strategy
// =============================================================================
//
// The TigerTMS iLink CDR endpoint per docs/tigertms/TigerTMS_AsteriskPostCDRRestAPI.pdf:
//
//   POST {base}/API/CDR
//   Content-Type: text/json
//   Body: {"message":{...Asterisk CDR fields...}}
//
//   200 OK with body {"response":"RECEIVEDOK"} → success
//   200 OK with body {"response":"ERROR"}      → failure (transient, retry)
//
// Retry: 3 attempts then dump and log (per the PDF). The dispatcher
// uses its own backoff with maxAttempts; for iLink CDR tenants,
// configure max_attempts=3 at tenant-config time.

// ILinkCDRStrategy signs (or rather, doesn't — iLink CDR is unsigned)
// outbound requests for the iLink `/API/CDR` endpoint.
type ILinkCDRStrategy struct{}

func (ILinkCDRStrategy) Name() string { return "ilink_cdr" }

func (ILinkCDRStrategy) Apply(req *http.Request, _ []byte, _ string, _ SecretResolver) error {
	// iLink CDR is unauthenticated in the PDF examples; if a tenant
	// needs auth we layer it on later via a separate strategy.
	return nil
}

func (ILinkCDRStrategy) AcceptResponse(resp *http.Response, body []byte) bool {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	var ack struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &ack); err != nil {
		// Non-JSON body — fall back to HTTP status only.
		return true
	}
	return ack.Response == "RECEIVEDOK"
}

// =============================================================================
// Cloud HMAC strategy (placeholder for future TigerTMS cloud inbound)
// =============================================================================
//
// Strategy for when TigerTMS cloud ingests our outbound events and
// requires HMAC-SHA256 signed bodies. secretRef is read from
// encrypted_secrets via SecretResolver.

type CloudHmacStrategy struct{}

func (CloudHmacStrategy) Name() string { return "cloud_hmac" }

func (CloudHmacStrategy) Apply(req *http.Request, body []byte, idemKey string, secrets SecretResolver) error {
	ref := req.Header.Get("X-Outbound-Secret-Ref")
	if ref == "" {
		return errors.New("cloud_hmac: missing X-Outbound-Secret-Ref")
	}
	secret, err := secrets.Resolve(ref)
	if err != nil {
		return fmt.Errorf("cloud_hmac: resolve secret: %w", err)
	}
	if len(secret) == 0 {
		return errors.New("cloud_hmac: empty secret")
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	mac.Write([]byte(idemKey))
	sig := hex.EncodeToString(mac.Sum(nil))
	req.Header.Set("X-Signature", "sha256="+sig)
	req.Header.Set("X-Idempotency-Key", idemKey)
	return nil
}

func (CloudHmacStrategy) AcceptResponse(resp *http.Response, _ []byte) bool {
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// =============================================================================
// Cloud Bearer strategy
// =============================================================================

type CloudBearerStrategy struct{}

func (CloudBearerStrategy) Name() string { return "cloud_bearer" }

func (CloudBearerStrategy) Apply(req *http.Request, _ []byte, _ string, secrets SecretResolver) error {
	ref := req.Header.Get("X-Outbound-Secret-Ref")
	if ref == "" {
		return errors.New("cloud_bearer: missing X-Outbound-Secret-Ref")
	}
	secret, err := secrets.Resolve(ref)
	if err != nil {
		return fmt.Errorf("cloud_bearer: resolve secret: %w", err)
	}
	if len(secret) == 0 {
		return errors.New("cloud_bearer: empty secret")
	}
	req.Header.Set("Authorization", "Bearer "+string(secret))
	return nil
}

func (CloudBearerStrategy) AcceptResponse(resp *http.Response, _ []byte) bool {
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// =============================================================================
// Producer helpers — the shape of payload each event type uses
// =============================================================================

// ILinkCDRMessage is the body shape we POST to /API/CDR. The receiver
// (iLink) expects a JSON object whose `message` key wraps the Asterisk
// CDR fields (per docs/tigertms/TigerTMS_AsteriskPostCDRRestAPI.pdf).
type ILinkCDRMessage struct {
	Message map[string]interface{} `json:"message"`
}

// BuildILinkCDRMessage produces the payload bytes for an iLink CDR
// POST. fields is the asterisk-CDR-shaped map (src, dst, channel,
// start, answer, end, duration, billsec, disposition, uniqueid, etc).
// Returns the JSON-encoded ILinkCDRMessage envelope.
func BuildILinkCDRMessage(fields map[string]interface{}) ([]byte, error) {
	if fields == nil {
		fields = map[string]interface{}{}
	}
	return json.Marshal(ILinkCDRMessage{Message: fields})
}

// Producer is the contract for code that wants to enqueue outbound
// events. Concrete producers (CDR builder, wake-up outcome) embed this.
type Producer struct {
	Store DispatcherStore
}

// EnqueueILinkCDR posts a CDR record to the tenant's iLink outbound
// endpoint. idempotencyKey dedupes retries (typically
// `{tenant_id}:{pbx_unique_id}`).
func (p *Producer) EnqueueILinkCDR(ctx context.Context, tenantID, targetURL, idempotencyKey string, fields map[string]interface{}) error {
	if p.Store == nil {
		return errors.New("outbound: producer not initialized")
	}
	payload, err := BuildILinkCDRMessage(fields)
	if err != nil {
		return fmt.Errorf("build CDR payload: %w", err)
	}
	row := &db.OutboundWebhook{
		TenantID:       tenantID,
		EventType:      "cdr_posted",
		IdempotencyKey: idempotencyKey,
		TargetURL:      targetURL,
		TargetStrategy: db.OutboundStrategyILinkCDR,
		Payload:        string(payload),
		Status:         db.OutboundStatusQueued,
		NextAttemptAt:  time.Now(),
	}
	_, err = p.Store.EnqueueOutboundWebhook(ctx, row)
	return err
}
