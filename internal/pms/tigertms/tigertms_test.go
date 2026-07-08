package tigertms

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/sagostin/pbx-hospitality/internal/pms"
)

// stubResolver is an in-memory TokenResolver for tests. tokens map
// stores token_hash -> ResolvedToken.
type stubResolver struct {
	tokens map[string]*ResolvedToken
}

func (s *stubResolver) ResolveTokenHash(_ context.Context, hash string) (*ResolvedToken, error) {
	r, ok := s.tokens[hash]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func tokenHash(t *testing.T, token string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newTestHandler(t *testing.T, tokens map[string]*ResolvedToken, tenantAdapters map[string]*Adapter) *Handler {
	t.Helper()
	if tenantAdapters == nil {
		tenantAdapters = map[string]*Adapter{}
	}
	h := NewHandler()
	h.SetResolver(&stubResolver{tokens: tokens})
	for tid, a := range tenantAdapters {
		h.RegisterTenant(tid, a)
	}
	return h
}

func newTenantAdapter(t *testing.T, tenantID, siteid string) *Adapter {
	t.Helper()
	adapter, err := NewAdapter("localhost", 0, WithTenant(tenantID, siteid))
	if err != nil {
		t.Fatal(err)
	}
	a := adapter.(*Adapter)
	if err := a.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// post posts a JSON body with a token in the URL.
func post(t *testing.T, app *fiber.App, path, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "text/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

// postWithHeader is like post but lets the test set extra headers (e.g.
// Authorization).
func postWithHeader(t *testing.T, app *fiber.App, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "text/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp
}

func decodeResponse(t *testing.T, r *http.Response) ilinkResponse {
	t.Helper()
	b, _ := io.ReadAll(r.Body)
	var v ilinkResponse
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode response %q: %v", string(b), err)
	}
	return v
}

func drainEvent(t *testing.T, a *Adapter) pms.Event {
	t.Helper()
	select {
	case evt := <-a.Events():
		return evt
	case <-time.After(time.Second):
		t.Fatal("no event emitted within 1s")
		return pms.Event{}
	}
}

// =============================================================================
// setguest — happy path with url_token auth
// =============================================================================

func TestSetGuest_URITokenAuth(t *testing.T) {
	const token = "abcdef0123456789abcdef0123456789" // 32-char long secret
	a := newTenantAdapter(t, "tenant-a", "00200")
	h := newTestHandler(t, map[string]*ResolvedToken{
		tokenHash(t, token): {
			TokenID:  1,
			TenantID: "tenant-a",
			Strategy: "url_token",
		},
	}, map[string]*Adapter{"tenant-a": a})
	app := fiber.New()
	h.Routes(app)

	resp := post(t, app, "/api/v1/pms/inbound/"+token+"/API/setguest",
		`{"extn":"4100","status":"occupied","firstname":"John","lastname":"Smith"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	if body.Result != "success" {
		t.Errorf("result = %q, want success", body.Result)
	}

	evt := drainEvent(t, a)
	if evt.Type != pms.EventCheckIn {
		t.Fatalf("event type = %v, want EventCheckIn", evt.Type)
	}
	if evt.Room != "4100" {
		t.Errorf("room = %q, want 4100", evt.Room)
	}
	if evt.GuestName != "John Smith" {
		t.Errorf("guest_name = %q, want John Smith", evt.GuestName)
	}
	if evt.Metadata["siteid"] != "00200" {
		t.Errorf("siteid = %q, want 00200", evt.Metadata["siteid"])
	}
}

// =============================================================================
// setguest — bearer layered auth
// =============================================================================

func TestSetGuest_BearerAuth(t *testing.T) {
	const (
		token  = "abcdef0123456789abcdef0123456789"
		bearer = "shhh-secret-bearer"
	)
	bearerHashBytes := sha256.Sum256([]byte(bearer))
	bearerHash := hex.EncodeToString(bearerHashBytes[:])

	a := newTenantAdapter(t, "tenant-a", "00200")
	h := newTestHandler(t, map[string]*ResolvedToken{
		tokenHash(t, token): {
			TokenID:    1,
			TenantID:   "tenant-a",
			Strategy:   "bearer",
			BearerHash: bearerHash,
		},
	}, map[string]*Adapter{"tenant-a": a})
	app := fiber.New()
	h.Routes(app)

	// Missing bearer → 401
	resp := post(t, app, "/api/v1/pms/inbound/"+token+"/API/setguest",
		`{"extn":"4100","status":"occupied"}`)
	if resp.StatusCode != 401 {
		t.Errorf("missing bearer: status = %d, want 401", resp.StatusCode)
	}

	// Wrong bearer → 401
	resp = postWithHeader(t, app, "/api/v1/pms/inbound/"+token+"/API/setguest",
		`{"extn":"4100","status":"occupied"}`,
		map[string]string{"Authorization": "Bearer wrong-token"})
	if resp.StatusCode != 401 {
		t.Errorf("wrong bearer: status = %d, want 401", resp.StatusCode)
	}

	// Correct bearer → 200
	resp = postWithHeader(t, app, "/api/v1/pms/inbound/"+token+"/API/setguest",
		`{"extn":"4100","status":"occupied","firstname":"John","lastname":"Smith"}`,
		map[string]string{"Authorization": "Bearer " + bearer})
	if resp.StatusCode != 200 {
		t.Fatalf("correct bearer: status = %d, want 200", resp.StatusCode)
	}
	evt := drainEvent(t, a)
	if evt.Room != "4100" {
		t.Errorf("room = %q, want 4100", evt.Room)
	}
}

// =============================================================================
// setguest — basic layered auth
// =============================================================================

func TestSetGuest_BasicAuth(t *testing.T) {
	const (
		token = "abcdef0123456789abcdef0123456789"
		user  = "pmsuser"
		pass  = "pmspass"
	)
	passHash := sha256.Sum256([]byte(pass))
	basicHash := hex.EncodeToString(passHash[:])
	basicCreds := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))

	a := newTenantAdapter(t, "tenant-a", "00200")
	h := newTestHandler(t, map[string]*ResolvedToken{
		tokenHash(t, token): {
			TokenID:   1,
			TenantID:  "tenant-a",
			Strategy:  "basic",
			BasicUser: user,
			BasicHash: basicHash,
		},
	}, map[string]*Adapter{"tenant-a": a})
	app := fiber.New()
	h.Routes(app)

	// Missing basic → 401
	resp := post(t, app, "/api/v1/pms/inbound/"+token+"/API/setguest",
		`{"extn":"4100","status":"occupied"}`)
	if resp.StatusCode != 401 {
		t.Errorf("missing basic: status = %d, want 401", resp.StatusCode)
	}

	// Wrong password → 401
	wrongCreds := base64.StdEncoding.EncodeToString([]byte(user + ":wrong"))
	resp = postWithHeader(t, app, "/api/v1/pms/inbound/"+token+"/API/setguest",
		`{"extn":"4100","status":"occupied"}`,
		map[string]string{"Authorization": "Basic " + wrongCreds})
	if resp.StatusCode != 401 {
		t.Errorf("wrong basic: status = %d, want 401", resp.StatusCode)
	}

	// Correct basic → 200
	resp = postWithHeader(t, app, "/api/v1/pms/inbound/"+token+"/API/setguest",
		`{"extn":"4100","status":"occupied"}`,
		map[string]string{"Authorization": "Basic " + basicCreds})
	if resp.StatusCode != 200 {
		t.Fatalf("correct basic: status = %d, want 200", resp.StatusCode)
	}
}

// =============================================================================
// setguest — invalid token
// =============================================================================

func TestSetGuest_InvalidToken(t *testing.T) {
	a := newTenantAdapter(t, "tenant-a", "00200")
	h := newTestHandler(t, map[string]*ResolvedToken{
		tokenHash(t, "knowntoken"): {
			TokenID: 1, TenantID: "tenant-a", Strategy: "url_token",
		},
	}, map[string]*Adapter{"tenant-a": a})
	app := fiber.New()
	h.Routes(app)

	resp := post(t, app, "/api/v1/pms/inbound/unknowntoken1234567890abcdef012345/API/setguest",
		`{"extn":"4100","status":"occupied"}`)
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	if body.Result != "failed" {
		t.Errorf("result = %q, want failed", body.Result)
	}
}

// =============================================================================
// setguest — short token rejected (defense in depth — prevents
// enumeration of very short tokens)
// =============================================================================

func TestSetGuest_TooShortToken(t *testing.T) {
	a := newTenantAdapter(t, "tenant-a", "00200")
	h := newTestHandler(t, nil, map[string]*Adapter{"tenant-a": a})
	app := fiber.New()
	h.Routes(app)

	resp := post(t, app, "/api/v1/pms/inbound/short/API/setguest",
		`{"extn":"4100","status":"occupied"}`)
	if resp.StatusCode != 400 {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// =============================================================================
// Multi-tenant dispatch by token
// =============================================================================

func TestMultiTenantDispatch(t *testing.T) {
	const (
		tokenA = "tokenaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		tokenB = "tokenbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	a := newTenantAdapter(t, "tenant-a", "00200")
	b := newTenantAdapter(t, "tenant-b", "00300")
	h := newTestHandler(t, map[string]*ResolvedToken{
		tokenHash(t, tokenA): {TokenID: 1, TenantID: "tenant-a", Strategy: "url_token"},
		tokenHash(t, tokenB): {TokenID: 2, TenantID: "tenant-b", Strategy: "url_token"},
	}, map[string]*Adapter{"tenant-a": a, "tenant-b": b})
	app := fiber.New()
	h.Routes(app)

	// Tenant A
	resp := post(t, app, "/api/v1/pms/inbound/"+tokenA+"/API/setmw",
		`{"extn":"1001","mw":"on"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("tenant A: status = %d, want 200", resp.StatusCode)
	}
	evtA := drainEvent(t, a)
	if evtA.Metadata["siteid"] != "00200" {
		t.Errorf("tenant A siteid = %q, want 00200", evtA.Metadata["siteid"])
	}

	// Tenant B
	resp = post(t, app, "/api/v1/pms/inbound/"+tokenB+"/API/setmw",
		`{"extn":"2002","mw":"off"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("tenant B: status = %d, want 200", resp.StatusCode)
	}
	evtB := drainEvent(t, b)
	if evtB.Metadata["siteid"] != "00300" {
		t.Errorf("tenant B siteid = %q, want 00300", evtB.Metadata["siteid"])
	}
}

// =============================================================================
// Wake-up flow (smoke)
// =============================================================================

func TestSetWakeup_FullDateTime(t *testing.T) {
	const token = "abcdef0123456789abcdef0123456789"
	a := newTenantAdapter(t, "tenant-a", "00200")
	h := newTestHandler(t, map[string]*ResolvedToken{
		tokenHash(t, token): {TokenID: 1, TenantID: "tenant-a", Strategy: "url_token"},
	}, map[string]*Adapter{"tenant-a": a})
	app := fiber.New()
	h.Routes(app)

	resp := post(t, app, "/api/v1/pms/inbound/"+token+"/API/setwakeup",
		`{"extn":"4100","action":"set","wakeuptime":"24-08-2017 08:00:00"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	evt := drainEvent(t, a)
	if evt.Metadata["wakeup_action"] != "set" {
		t.Errorf("wakeup_action = %q, want set", evt.Metadata["wakeup_action"])
	}
	if evt.Metadata["wakeup_time_full"] != "24-08-2017 08:00:00" {
		t.Errorf("wakeup_time_full = %q, want 24-08-2017 08:00:00", evt.Metadata["wakeup_time_full"])
	}
}

func TestSetWakeup_ClearAll(t *testing.T) {
	const token = "abcdef0123456789abcdef0123456789"
	a := newTenantAdapter(t, "tenant-a", "00200")
	h := newTestHandler(t, map[string]*ResolvedToken{
		tokenHash(t, token): {TokenID: 1, TenantID: "tenant-a", Strategy: "url_token"},
	}, map[string]*Adapter{"tenant-a": a})
	app := fiber.New()
	h.Routes(app)

	resp := post(t, app, "/api/v1/pms/inbound/"+token+"/API/setwakeup",
		`{"extn":"4100","action":"clearall","wakeuptime":""}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeResponse(t, resp)
	if !strings.Contains(body.Information, "cleared all") {
		t.Errorf("information = %q, expected to mention 'cleared all'", body.Information)
	}

	evt := drainEvent(t, a)
	if evt.Metadata["wakeup_action"] != "clearall" {
		t.Errorf("wakeup_action = %q, want clearall", evt.Metadata["wakeup_action"])
	}
}

// =============================================================================
// Adapter basics
// =============================================================================

func TestNewAdapter_Defaults(t *testing.T) {
	adapter, err := NewAdapter("localhost", 0, WithTenant("tenant-x", "siteid-x"))
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	a := adapter.(*Adapter)
	if a.TenantID() != "tenant-x" {
		t.Errorf("TenantID = %q, want tenant-x", a.TenantID())
	}
	if a.SiteID() != "siteid-x" {
		t.Errorf("SiteID = %q, want siteid-x", a.SiteID())
	}
	if a.Protocol() != "tigertms" {
		t.Errorf("Protocol = %q, want tigertms", a.Protocol())
	}
	if err := a.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if !a.Connected() {
		t.Error("Connected() = false after Connect(), want true")
	}
}

func TestParseOnOff(t *testing.T) {
	cases := []struct {
		in      string
		want    bool
		wantErr bool
	}{
		{"on", true, false},
		{"ON", true, false},
		{"On", true, false},
		{"1", true, false},
		{"true", true, false},
		{"off", false, false},
		{"OFF", false, false},
		{"0", false, false},
		{"false", false, false},
		{"", false, false},
		{"maybe", false, true},
	}
	for _, tc := range cases {
		got, err := parseOnOff(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseOnOff(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("parseOnOff(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// silences unused imports warnings if bytes becomes unused.
var _ = bytes.NewBuffer
