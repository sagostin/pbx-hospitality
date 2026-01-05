package tigertms

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/topsoffice/bicom-hospitality/internal/pms"
)

func TestNewAdapter(t *testing.T) {
	adapter, err := NewAdapter("localhost", 8080)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if adapter.Protocol() != "tigertms" {
		t.Errorf("expected protocol tigertms, got %s", adapter.Protocol())
	}
}

func TestAdapterConnect(t *testing.T) {
	adapter, _ := NewAdapter("localhost", 8080)
	ctx := t.Context()

	err := adapter.Connect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !adapter.Connected() {
		t.Error("expected adapter to be connected")
	}

	adapter.Close()
}

func TestHandleSetGuest(t *testing.T) {
	adapter, _ := NewAdapter("localhost", 8080)
	adapter.(*Adapter).events = make(chan pms.Event, 10)
	handler := NewHandler(adapter.(*Adapter))

	tests := []struct {
		name       string
		params     map[string]string
		wantStatus int
		wantType   pms.EventType
	}{
		{
			name:       "check in",
			params:     map[string]string{"room": "2129", "checkin": "true", "guest": "Smith, John"},
			wantStatus: http.StatusOK,
			wantType:   pms.EventCheckIn,
		},
		{
			name:       "check out",
			params:     map[string]string{"room": "2129", "checkin": "false"},
			wantStatus: http.StatusOK,
			wantType:   pms.EventCheckOut,
		},
		{
			name:       "missing room",
			params:     map[string]string{"checkin": "true"},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			for k, v := range tt.params {
				form.Set(k, v)
			}

			req := httptest.NewRequest(http.MethodPost, "/API/setguest", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			handler.handleSetGuest(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantStatus == http.StatusOK {
				select {
				case evt := <-adapter.Events():
					if evt.Type != tt.wantType {
						t.Errorf("got event type %v, want %v", evt.Type, tt.wantType)
					}
				case <-time.After(time.Second):
					t.Error("expected event but none received")
				}
			}
		})
	}
}

func TestHandleSetMW(t *testing.T) {
	adapter, _ := NewAdapter("localhost", 8080)
	adapter.(*Adapter).events = make(chan pms.Event, 10)
	handler := NewHandler(adapter.(*Adapter))

	tests := []struct {
		name       string
		params     map[string]string
		wantStatus int
		wantMW     bool
	}{
		{
			name:       "mw on",
			params:     map[string]string{"room": "2129", "mw": "true"},
			wantStatus: http.StatusOK,
			wantMW:     true,
		},
		{
			name:       "mw off",
			params:     map[string]string{"room": "2129", "mw": "false"},
			wantStatus: http.StatusOK,
			wantMW:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			for k, v := range tt.params {
				form.Set(k, v)
			}

			req := httptest.NewRequest(http.MethodPost, "/API/setmw", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			handler.handleSetMW(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantStatus == http.StatusOK {
				select {
				case evt := <-adapter.Events():
					if evt.Type != pms.EventMessageWaiting {
						t.Errorf("got event type %v, want EventMessageWaiting", evt.Type)
					}
					if evt.Status != tt.wantMW {
						t.Errorf("got status %v, want %v", evt.Status, tt.wantMW)
					}
				case <-time.After(time.Second):
					t.Error("expected event but none received")
				}
			}
		})
	}
}

func TestHandleSetDND(t *testing.T) {
	adapter, _ := NewAdapter("localhost", 8080)
	adapter.(*Adapter).events = make(chan pms.Event, 10)
	handler := NewHandler(adapter.(*Adapter))

	form := url.Values{}
	form.Set("room", "2129")
	form.Set("dnd", "true")

	req := httptest.NewRequest(http.MethodPost, "/API/setdnd", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler.handleSetDND(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}

	select {
	case evt := <-adapter.Events():
		if evt.Type != pms.EventDND {
			t.Errorf("got event type %v, want EventDND", evt.Type)
		}
		if !evt.Status {
			t.Error("expected DND status to be true")
		}
	case <-time.After(time.Second):
		t.Error("expected event but none received")
	}
}

func TestHandleSetWakeup(t *testing.T) {
	adapter, _ := NewAdapter("localhost", 8080)
	adapter.(*Adapter).events = make(chan pms.Event, 10)
	handler := NewHandler(adapter.(*Adapter))

	form := url.Values{}
	form.Set("room", "2129")
	form.Set("time", "07:00")
	form.Set("enabled", "true")

	req := httptest.NewRequest(http.MethodPost, "/API/setwakeup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler.handleSetWakeup(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}

	select {
	case evt := <-adapter.Events():
		if evt.Type != pms.EventWakeUp {
			t.Errorf("got event type %v, want EventWakeUp", evt.Type)
		}
		if evt.Metadata["wakeup_time"] != "07:00" {
			t.Errorf("got wakeup time %s, want 07:00", evt.Metadata["wakeup_time"])
		}
	case <-time.After(time.Second):
		t.Error("expected event but none received")
	}
}

func TestAuthMiddleware(t *testing.T) {
	adapter, _ := NewAdapter("localhost", 8080, WithAuthToken("secret123"))
	handler := NewHandler(adapter.(*Adapter))

	// Test with valid token
	req := httptest.NewRequest(http.MethodPost, "/API/setmw?room=101&mw=true", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	w := httptest.NewRecorder()

	router := handler.Routes()
	router.ServeHTTP(w, req)

	// Should succeed (either 200 or at least not 401)
	if w.Code == http.StatusUnauthorized {
		t.Error("expected request with valid token to succeed")
	}

	// Test with invalid token
	req2 := httptest.NewRequest(http.MethodPost, "/API/setmw?room=101&mw=true", nil)
	req2.Header.Set("Authorization", "Bearer wrongtoken")
	w2 := httptest.NewRecorder()

	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w2.Code)
	}
}
