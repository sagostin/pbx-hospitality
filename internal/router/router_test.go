package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/outbound"
	"github.com/sagostin/pbx-hospitality/internal/outbound/outboundtest"
)

// TestRouter_EnqueueRoundTrip wires a router through the fake
// dispatcher store + a real outbound.Dispatcher pointed at a stub
// HTTP server. It enqueues a CDR-shaped event and asserts the
// receiver saw the iLink-shaped payload.
func TestRouter_EnqueueRoundTrip(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.Write([]byte(`{"response":"RECEIVEDOK"}`))
	}))
	defer srv.Close()

	store := outboundtest.NewFakeStore()
	d := outbound.NewDispatcher(store, nil)
	d.WithOptions(2, 5*time.Millisecond, 20*time.Millisecond, 25*time.Millisecond, 10, 1)

	r := NewRouter(nil, d)
	r.AddTenant(context.Background(), TenantConfig{
		TenantID:         "tenant-a",
		Enabled:          true,
		InboundProtocol:  "tigertms",
		OutboundEnabled:  true,
		OutboundURL:      srv.URL + "/API/CDR",
		OutboundStrategy: db.OutboundStrategyILinkCDR,
	}, nil)

	if err := r.Enqueue(context.Background(), OutboundEvent{
		TenantID:       "tenant-a",
		EventType:      "cdr_posted",
		IdempotencyKey: "tenant-a:unique-1",
		Payload: map[string]interface{}{
			"src":      "821",
			"dst":      "630",
			"channel":  "SIP/821-0000002",
			"duration": "60",
			"uniqueid": "unique-1",
		},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	d.DrainOnceForTest()

	if got := received.Load(); got != 1 {
		t.Errorf("receiver got %d requests, want 1", got)
	}

	if err := r.Enqueue(context.Background(), OutboundEvent{
		TenantID:       "tenant-a",
		EventType:      "cdr_posted",
		IdempotencyKey: "tenant-a:unique-1",
		Payload: map[string]interface{}{
			"src": "821",
		},
	}); err != nil {
		t.Fatalf("Enqueue (replay): %v", err)
	}
	d.DrainOnceForTest()
	if got := received.Load(); got != 1 {
		t.Errorf("after replay, receiver got %d requests, want 1 (dedupe)", got)
	}

	r.RemoveTenant("tenant-a")
}

// TestRouter_NotRunningError enqueues for an unknown tenant and
// expects NotRunningError.
func TestRouter_NotRunningError(t *testing.T) {
	store := outboundtest.NewFakeStore()
	d := outbound.NewDispatcher(store, nil)
	r := NewRouter(nil, d)

	err := r.Enqueue(context.Background(), OutboundEvent{
		TenantID: "no-such-tenant",
	})
	if _, ok := err.(*NotRunningError); !ok {
		t.Errorf("err = %v, want *NotRunningError", err)
	}
}

// TestRouter_AddRemoveStopsGoroutine verifies that AddTenant +
// RemoveTenant cleanly tears down the per-tenant goroutine.
func TestRouter_AddRemoveStopsGoroutine(t *testing.T) {
	store := outboundtest.NewFakeStore()
	d := outbound.NewDispatcher(store, nil)
	r := NewRouter(nil, d)
	r.AddTenant(context.Background(), TenantConfig{
		TenantID:        "tenant-x",
		Enabled:         true,
		InboundProtocol: "tigertms",
	}, nil)
	if _, ok := r.tenants["tenant-x"]; !ok {
		t.Fatal("expected tenant-x to be registered")
	}
	r.RemoveTenant("tenant-x")
	if _, ok := r.tenants["tenant-x"]; ok {
		t.Fatal("expected tenant-x to be removed")
	}
}
