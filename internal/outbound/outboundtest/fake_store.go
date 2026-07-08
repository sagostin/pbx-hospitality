package outboundtest

import (
	"context"
	"sync"
	"time"

	"github.com/sagostin/pbx-hospitality/internal/db"
)

// FakeStore is a thread-safe in-memory DispatcherStore for tests.
// Copied into the router package would be silly; exported here so
// both packages can use it.
type FakeStore struct {
	mu   sync.Mutex
	rows map[int64]*db.OutboundWebhook
	next int64
}

func NewFakeStore() *FakeStore { return &FakeStore{rows: make(map[int64]*db.OutboundWebhook)} }

func (s *FakeStore) EnqueueOutboundWebhook(_ context.Context, w *db.OutboundWebhook) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.TenantID == w.TenantID && r.EventType == w.EventType && r.IdempotencyKey == w.IdempotencyKey {
			return r.ID, nil
		}
	}
	s.next++
	w.ID = s.next
	cp := *w
	s.rows[w.ID] = &cp
	return w.ID, nil
}

func (s *FakeStore) ClaimDueOutboundWebhooks(_ context.Context, now time.Time, limit int) ([]db.OutboundWebhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var claimed []db.OutboundWebhook
	for _, r := range s.rows {
		if (r.Status == db.OutboundStatusQueued || r.Status == db.OutboundStatusFailed) &&
			!r.NextAttemptAt.After(now) {
			r.Status = db.OutboundStatusSending
			claimed = append(claimed, *r)
			if len(claimed) >= limit {
				break
			}
		}
	}
	return claimed, nil
}

func (s *FakeStore) MarkOutboundSent(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return nil
	}
	now := time.Now()
	r.Status = db.OutboundStatusSent
	r.DeliveredAt = &now
	r.LastError = ""
	return nil
}

func (s *FakeStore) MarkOutboundFailed(_ context.Context, id int64, errMsg string, nextAttemptAt time.Time, maxAttempts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return nil
	}
	r.AttemptCount++
	r.LastError = errMsg
	r.NextAttemptAt = nextAttemptAt
	if r.AttemptCount >= maxAttempts {
		r.Status = db.OutboundStatusDropped
	} else {
		r.Status = db.OutboundStatusFailed
	}
	return nil
}

func (s *FakeStore) ListOutboundWebhooksForTenant(_ context.Context, tenantID string, _ int) ([]db.OutboundWebhook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.OutboundWebhook
	for _, r := range s.rows {
		if r.TenantID == tenantID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (s *FakeStore) Snapshot(tenantID string) []db.OutboundWebhook {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.OutboundWebhook
	for _, r := range s.rows {
		if r.TenantID == tenantID {
			out = append(out, *r)
		}
	}
	return out
}
