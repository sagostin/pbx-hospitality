package wakeup

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sagostin/pbx-hospitality/internal/db"
)

// fakeRepo is a minimal in-memory implementation of the wakeup.Repo
// interface. It records every call so tests can assert on side effects.
type fakeRepo struct {
	mu sync.Mutex

	due []db.WakeUpCall

	originated []int64
	failed     map[int64]string
	completed  []int64

	getErr    error
	markErr   error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{failed: map[int64]string{}}
}

func (f *fakeRepo) GetDueWakeUpCalls(_ context.Context, _ time.Time, _ int) ([]db.WakeUpCall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	return append([]db.WakeUpCall(nil), f.due...), nil
}

func (f *fakeRepo) MarkWakeUpOriginated(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	f.originated = append(f.originated, id)
	return nil
}

func (f *fakeRepo) MarkWakeUpFailed(_ context.Context, id int64, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	f.failed[id] = reason
	return nil
}

func (f *fakeRepo) MarkWakeUpCompleted(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = append(f.completed, id)
	return nil
}

// --- fake PBX provider ---------------------------------------------------

type fakeProvider struct {
	mu          sync.Mutex
	originated  []string
	failWith    error
}

func (f *fakeProvider) OriginateWakeUp(_ context.Context, ext, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	f.originated = append(f.originated, ext)
	return nil
}

// All other Provider methods are no-ops for the scheduler tests.
func (f *fakeProvider) Connect(_ context.Context) error                      { return nil }
func (f *fakeProvider) Close() error                                       { return nil }
func (f *fakeProvider) Connected() bool                                     { return true }
func (f *fakeProvider) UpdateExtensionName(_ context.Context, _, _ string) error {
	return nil
}
func (f *fakeProvider) DeleteAllVoicemails(_ context.Context, _ string) error { return nil }
func (f *fakeProvider) ResetVoicemailGreeting(_ context.Context, _ string) error {
	return nil
}
func (f *fakeProvider) ClearVoicemailForGuest(_ context.Context, _ string) error {
	return nil
}
func (f *fakeProvider) SetMWI(_ context.Context, _ string, _ bool) error       { return nil }
func (f *fakeProvider) SetDND(_ context.Context, _ string, _ bool) error       { return nil }
func (f *fakeProvider) ScheduleWakeUpCall(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (f *fakeProvider) CancelWakeUpCall(_ context.Context, _ string) error    { return nil }
func (f *fakeProvider) SetCallForward(_ context.Context, _, _ string, _ bool) error {
	return nil
}

// --- tests --------------------------------------------------------------

func TestNewScheduler_DefaultInterval(t *testing.T) {
	s := NewScheduler(nil, newFakeRepo(), 0)
	if s.Interval() != DefaultInterval {
		t.Errorf("interval = %v, want %v", s.Interval(), DefaultInterval)
	}
}

func TestScheduler_TickNoDue(t *testing.T) {
	repo := newFakeRepo()
	s := NewScheduler(nil, repo, time.Minute)
	// No tenants registered, no due rows. tick() should be a no-op.
	// We can't call tick() directly because it's unexported, but the
	// run() loop calls tick() immediately on Start. Cancel before it
	// can do anything; that's fine.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Start(ctx)
	s.Stop()
	if len(repo.originated) != 0 {
		t.Errorf("expected no originates, got %d", len(repo.originated))
	}
}

func TestScheduler_HandlesTenantLookupMiss(t *testing.T) {
	// ProviderFor / dispatch path: a row whose tenant isn't loaded must
	// be marked failed, NOT panicked.
	repo := newFakeRepo()
	repo.due = []db.WakeUpCall{{
		ID:        42,
		TenantID:  "ghost-tenant",
		Extension: "101",
	}}
	s := NewScheduler(nil, repo, time.Hour)
	// nil tm → dispatch hits the "no tenant manager" guard.
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	s.Stop()

	if reason, ok := repo.failed[42]; !ok || reason != "no tenant manager" {
		t.Errorf("expected failed[42] = %q, got %q (present=%v)", "no tenant manager", reason, ok)
	}
}

func TestScheduler_RepoGetErrorIsSwallowed(t *testing.T) {
	repo := newFakeRepo()
	repo.getErr = errors.New("simulated db outage")
	s := NewScheduler(nil, repo, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
	s.Stop()
	// No panic, no originated calls. That's the whole test.
	if len(repo.originated) != 0 {
		t.Errorf("expected no originates, got %d", len(repo.originated))
	}
}