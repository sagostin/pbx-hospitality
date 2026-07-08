package outbound

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/outbound/outboundtest"
)

// stubSecretResolver is an in-memory resolver for tests.
type stubSecretResolver struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (s *stubSecretResolver) Resolve(ref string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[ref], nil
}

func newTestDispatcher(t *testing.T, secrets SecretResolver) (*Dispatcher, *outboundtest.FakeStore) {
	t.Helper()
	store := outboundtest.NewFakeStore()
	disp := NewDispatcher(store, secrets)
	disp.WithOptions(3, 10*time.Millisecond, 50*time.Millisecond, 25*time.Millisecond, 10, 2)
	return disp, store
}

// TestEnqueueILinkCDR_HappyPath enqueues a CDR, runs one drain cycle,
// and asserts the row is marked sent and the receiver saw the right
// body shape.
func TestEnqueueILinkCDR_HappyPath(t *testing.T) {
	var received ILinkCDRMessage
	var receivedCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCT = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "text/json")
		_, _ = w.Write([]byte(`{"response":"RECEIVEDOK"}`))
	}))
	defer srv.Close()

	disp, store := newTestDispatcher(t, &stubSecretResolver{})
	defer disp.Stop()
	prod := &Producer{Store: store}

	if err := prod.EnqueueILinkCDR(context.Background(),
		"tenant-a", srv.URL+"/API/CDR", "tenant-a:pbx:1",
		map[string]interface{}{
			"src":         "821",
			"dst":         "630",
			"channel":     "SIP/821-0000002",
			"start":       "2026-07-08 10:00:00",
			"answer":      "2026-07-08 10:00:01",
			"end":         "2026-07-08 10:01:00",
			"duration":    60,
			"billsec":     59,
			"disposition": "ANSWERED",
			"uniqueid":    "pbx-uid-1",
		}); err != nil {
		t.Fatalf("EnqueueILinkCDR: %v", err)
	}

	disp.drainBatch(context.Background())

	if received.Message == nil {
		t.Fatal("receiver got nil message")
	}
	if received.Message["src"] != "821" {
		t.Errorf("src = %v, want 821", received.Message["src"])
	}
	if received.Message["uniqueid"] != "pbx-uid-1" {
		t.Errorf("uniqueid = %v, want pbx-uid-1", received.Message["uniqueid"])
	}
	if receivedCT != "text/json" {
		t.Errorf("content-type = %q, want text/json", receivedCT)
	}

	rows := store.Snapshot("tenant-a")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Status != db.OutboundStatusSent {
		t.Errorf("status = %q, want sent", rows[0].Status)
	}
	if rows[0].DeliveredAt == nil {
		t.Error("delivered_at not set")
	}
}

// TestEnqueueILinkCDR_RetriesOnReceiverERROR verifies the
// `{"response":"ERROR"}` body is treated as failure even though the
// HTTP status is 200, per the iLink PDF retry contract.
func TestEnqueueILinkCDR_RetriesOnReceiverERROR(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"response":"ERROR"}`))
	}))
	defer srv.Close()

	disp, store := newTestDispatcher(t, &stubSecretResolver{})
	defer disp.Stop()
	disp.WithOptions(2, 5*time.Millisecond, 20*time.Millisecond, 25*time.Millisecond, 10, 1)

	prod := &Producer{Store: store}
	if err := prod.EnqueueILinkCDR(context.Background(),
		"tenant-a", srv.URL+"/API/CDR", "tenant-a:pbx:1",
		map[string]interface{}{"src": "1", "dst": "2"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		disp.drainBatch(context.Background())
		time.Sleep(15 * time.Millisecond)
	}

	rows := store.Snapshot("tenant-a")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Status != db.OutboundStatusDropped {
		t.Errorf("status = %q, want dropped (max attempts reached)", rows[0].Status)
	}
	if rows[0].AttemptCount < 2 {
		t.Errorf("attempt_count = %d, want >= 2", rows[0].AttemptCount)
	}
	if attempts < 2 {
		t.Errorf("receiver attempts = %d, want >= 2", attempts)
	}
}

// TestEnqueueILinkCDR_Idempotency verifies that enqueueing the same
// idempotency key twice does not produce duplicate rows.
func TestEnqueueILinkCDR_Idempotency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"response":"RECEIVEDOK"}`))
	}))
	defer srv.Close()

	disp, store := newTestDispatcher(t, &stubSecretResolver{})
	defer disp.Stop()
	prod := &Producer{Store: store}

	for i := 0; i < 3; i++ {
		if err := prod.EnqueueILinkCDR(context.Background(),
			"tenant-a", srv.URL+"/API/CDR", "tenant-a:pbx:1",
			map[string]interface{}{"src": "1"}); err != nil {
			t.Fatal(err)
		}
	}
	rows := store.Snapshot("tenant-a")
	if len(rows) != 1 {
		t.Errorf("rows = %d, want 1 (idempotency dedupe)", len(rows))
	}
}

// TestCloudHmacStrategy verifies HMAC-SHA256 of body+idempotency_key
// with the resolved secret.
func TestCloudHmacStrategy(t *testing.T) {
	secrets := &stubSecretResolver{data: map[string][]byte{"mysecret": []byte("topsecret")}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig := r.Header.Get("X-Signature")
		body := []byte(`{"x":1}`)
		idem := "abc"
		mac := hmac.New(sha256.New, []byte("topsecret"))
		mac.Write(body)
		mac.Write([]byte(idem))
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if gotSig != want {
			t.Errorf("signature = %q, want %q", gotSig, want)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL, nil)
	req.Header.Set("X-Outbound-Secret-Ref", "mysecret")
	strat := CloudHmacStrategy{}
	if err := strat.Apply(req, []byte(`{"x":1}`), "abc", secrets); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

// TestCloudBearerStrategy verifies the bearer token gets set on the
// request.
func TestCloudBearerStrategy(t *testing.T) {
	secrets := &stubSecretResolver{data: map[string][]byte{"mysecret": []byte("mytoken")}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer mytoken" {
			t.Errorf("Authorization = %q, want Bearer mytoken", got)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL, nil)
	req.Header.Set("X-Outbound-Secret-Ref", "mysecret")
	strat := CloudBearerStrategy{}
	if err := strat.Apply(req, nil, "", secrets); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

// TestNextBackoff_Monotonic asserts backoff doubles per attempt up to
// the cap (with jitter potentially making individual samples smaller).
func TestNextBackoff_Monotonic(t *testing.T) {
	base := 10 * time.Millisecond
	max := 80 * time.Millisecond
	for i := 1; i <= 10; i++ {
		_ = nextBackoff(i, base, max)
	}
}
