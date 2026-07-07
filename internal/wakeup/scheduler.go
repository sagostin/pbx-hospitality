// Package wakeup contains the WakeUpScheduler that fires Bicom wake-up
// calls at the scheduled time via ARI Originate.
//
// Data flow:
//
//  1. tenant.handleWakeUp inserts a row into the wakeup_calls table
//     after a successful ScheduleWakeUpCall on the PBX provider.
//  2. WakeUpScheduler.Start launches a goroutine that ticks every
//     `interval` (default 10s).
//  3. On each tick, GetDueWakeUpCalls returns rows with status='pending'
//     AND scheduled_at <= NOW(). The scheduler calls
//     pbx.Provider.OriginateWakeUp for each.
//  4. On success the row transitions to status='originated'. On error
//     it transitions to status='failed' with last_error set.
//
// Tier 1 keeps the row at status='originated' after the originate
// succeeds — the actual ring-out / answer / hangup is the PBX's
// responsibility. Tier 2 (planned) will subscribe to ARI Stasis events
// to transition rows to status='completed' once the call is answered
// and hung up.
package wakeup

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/tenant"
)

// DefaultInterval is the default tick interval when none is provided.
const DefaultInterval = 10 * time.Second

// MaxBatchPerTick caps the number of wake-up rows processed per tick to
// avoid a thundering herd on startup or after a long DB outage.
const MaxBatchPerTick = 100

// Scheduler fires wake-up calls via pbx.Provider.OriginateWakeUp.
//
// One Scheduler is shared across all tenants. The tenant → provider
// mapping is resolved per-row via tenant.Manager.Get so that adding a
// tenant at runtime does not require restarting the scheduler.
type Scheduler struct {
	tm       *tenant.Manager
	repo     Repo
	interval time.Duration

	cancel context.CancelFunc
	wg     sync.WaitGroup

	// started guards against double Start.
	startedMu sync.Mutex
	started   bool
}

// Repo is the subset of *db.DB the scheduler needs. Extracted as an
// interface so tests can supply a fake without spinning up Postgres.
type Repo interface {
	GetDueWakeUpCalls(ctx context.Context, now time.Time, limit int) ([]db.WakeUpCall, error)
	MarkWakeUpOriginated(ctx context.Context, id int64) error
	MarkWakeUpFailed(ctx context.Context, id int64, reason string) error
	MarkWakeUpCompleted(ctx context.Context, id int64) error
}

// NewScheduler constructs a Scheduler. interval <= 0 falls back to
// DefaultInterval.
func NewScheduler(tm *tenant.Manager, repo Repo, interval time.Duration) *Scheduler {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Scheduler{
		tm:       tm,
		repo:     repo,
		interval: interval,
	}
}

// Start launches the scheduler goroutine. Safe to call once; subsequent
// calls are no-ops.
func (s *Scheduler) Start(ctx context.Context) {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()
	if s.started {
		return
	}
	s.started = true

	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go s.run(ctx)

	log.Info().
		Dur("interval", s.interval).
		Msg("WakeUpScheduler started")
}

// Stop signals the scheduler to exit and waits for the goroutine to
// finish processing the current tick. Safe to call even if Start was
// never called.
func (s *Scheduler) Stop() {
	s.startedMu.Lock()
	defer s.startedMu.Unlock()
	if !s.started {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.started = false

	log.Info().Msg("WakeUpScheduler stopped")
}

// Interval returns the configured tick interval.
func (s *Scheduler) Interval() time.Duration {
	return s.interval
}

func (s *Scheduler) run(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Run one tick immediately on startup so we don't wait `interval`
	// to catch up on existing pending rows.
	s.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick fetches all due wake-up rows and dispatches each to the tenant's
// PBX provider. Errors are logged and persisted to wakeup_calls.last_error
// but never abort the loop.
func (s *Scheduler) tick(ctx context.Context) {
	if s.repo == nil {
		return
	}
	due, err := s.repo.GetDueWakeUpCalls(ctx, time.Now(), MaxBatchPerTick)
	if err != nil {
		log.Error().Err(err).Msg("wakeup scheduler: failed to fetch due calls")
		return
	}
	if len(due) == 0 {
		return
	}

	log.Debug().Int("count", len(due)).Msg("wakeup scheduler: dispatching due calls")

	for _, w := range due {
		s.dispatch(ctx, w)
	}
}

func (s *Scheduler) dispatch(ctx context.Context, w db.WakeUpCall) {
	if s.tm == nil {
		s.markFailed(ctx, w.ID, "no tenant manager")
		return
	}
	t, ok := s.tm.Get(w.TenantID)
	if !ok {
		log.Warn().
			Int64("id", w.ID).
			Str("tenant", w.TenantID).
			Msg("wakeup scheduler: tenant not loaded")
		s.markFailed(ctx, w.ID, "tenant not loaded")
		return
	}
	provider := t.PBXProvider()
	if provider == nil {
		s.markFailed(ctx, w.ID, "no PBX provider")
		return
	}

	if err := provider.OriginateWakeUp(ctx, w.Extension, ""); err != nil {
		log.Warn().
			Err(err).
			Int64("id", w.ID).
			Str("tenant", w.TenantID).
			Str("extension", w.Extension).
			Msg("wakeup scheduler: originate failed")
		s.markFailed(ctx, w.ID, err.Error())
		return
	}

	log.Info().
		Int64("id", w.ID).
		Str("tenant", w.TenantID).
		Str("extension", w.Extension).
		Time("scheduled_at", w.ScheduledAt).
		Msg("wakeup scheduler: wake-up call originated")

	if err := s.repo.MarkWakeUpOriginated(ctx, w.ID); err != nil {
		log.Error().
			Err(err).
			Int64("id", w.ID).
			Msg("wakeup scheduler: failed to mark row originated")
	}
}

func (s *Scheduler) markFailed(ctx context.Context, id int64, reason string) {
	if err := s.repo.MarkWakeUpFailed(ctx, id, reason); err != nil {
		log.Error().
			Err(err).
			Int64("id", id).
			Msg("wakeup scheduler: failed to mark row failed")
	}
}

// ProviderFor returns the PBX provider registered for the given tenant
// via the scheduler's tenant manager. Exposed for tests that need to
// verify the dispatch path without going through tick().
func (s *Scheduler) ProviderFor(tenantID string) (any, bool) {
	t, ok := s.tm.Get(tenantID)
	if !ok {
		return nil, false
	}
	return t.PBXProvider(), t.PBXProvider() != nil
}