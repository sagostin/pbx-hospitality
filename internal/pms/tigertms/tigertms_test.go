package tigertms

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/sagostin/pbx-hospitality/internal/pms"
)

// TestTigerTMSWakeup_MetadataKey is the regression test for the bug where
// the wake-up time was stored in evt.Metadata["wakeup_time"] but
// tenant.handleWakeUp only looked at evt.Metadata["TI"], causing every
// TigerTMS wake-up request to silently fail with "Wake-up call requested
// but no time specified".
//
// The fix in tenant.handleWakeUp tries ["TI", "wakeup_time", "TI_RAW"] in
// that order. This test asserts the adapter produces the keys the
// downstream code expects.
func TestTigerTMSWakeup_MetadataKey(t *testing.T) {
	adapter, _ := NewAdapter("localhost", 0)
	handler := NewHandler(adapter.(*Adapter))
	app := fiber.New()
	handler.Routes(app)

	// Send wake-up for room 101 at 07:30
	req := httptest.NewRequest("POST", "/API/setwakeup?room=101&time=07%3A30&enabled=true", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Drain the event channel with a short deadline.
	var evt pms.Event
	select {
	case evt = <-adapter.Events():
	case <-time.After(time.Second):
		t.Fatal("no wake-up event emitted within 1s")
	}

	if evt.Type != pms.EventWakeUp {
		t.Fatalf("event type = %v, want EventWakeUp", evt.Type)
	}
	if evt.Room != "101" {
		t.Errorf("room = %q, want %q", evt.Room, "101")
	}

	// The handler currently writes "wakeup_time". tenant.handleWakeUp
	// accepts both "TI" and "wakeup_time" (and "TI_RAW") so the multi-key
	// lookup will find it. We assert at least one of the three is set to
	// a value that parseWakeUpTime can handle.
	got := evt.Metadata["TI"]
	if got == "" {
		got = evt.Metadata["wakeup_time"]
	}
	if got == "" {
		got = evt.Metadata["TI_RAW"]
	}
	if got == "" {
		t.Fatalf("metadata has no TI / wakeup_time / TI_RAW: %+v", evt.Metadata)
	}
	// Should be parsable as HH:MM or HHMM (allow colon or no colon).
	if !strings.ContainsAny(got, "0123456789") {
		t.Errorf("wake-up time metadata %q has no digits", got)
	}
	if len(strings.ReplaceAll(got, ":", "")) != 4 {
		t.Errorf("wake-up time metadata %q is not HH:MM or HHMM", got)
	}
}

// TestHandlerAuth verifies the Bearer token check rejects unauthenticated
// requests when an auth_token is configured.
func TestHandlerAuth(t *testing.T) {
	adapter, _ := NewAdapter("localhost", 0)
	adapter.(*Adapter).authToken = "secret-token"
	handler := NewHandler(adapter.(*Adapter))
	app := fiber.New()
	app.Use(handler.authMiddleware)
	app.Post("/API/setguest", handler.handleSetGuest)

	// Unauthenticated → 401
	req := httptest.NewRequest("POST", "/API/setguest?room=101&checkin=true&guest=Test", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Errorf("expected 401 unauthenticated, got %d", resp.StatusCode)
	}

	// Wrong token → 401
	req = httptest.NewRequest("POST", "/API/setguest?room=101&checkin=true&guest=Test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, _ = app.Test(req)
	if resp.StatusCode != 401 {
		t.Errorf("expected 401 wrong-token, got %d", resp.StatusCode)
	}

	// Correct token → 200
	req = httptest.NewRequest("POST", "/API/setguest?room=101&checkin=true&guest=Test", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, _ = app.Test(req)
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 correct-token, got %d", resp.StatusCode)
	}
}
