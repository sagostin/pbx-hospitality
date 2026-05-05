package bicom

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				BaseURL:  "https://pbx.example.com",
				APIKey:   "test-key",
				TenantID: "1",
			},
			wantErr: false,
		},
		{
			name: "missing base URL",
			cfg: Config{
				APIKey: "test-key",
			},
			wantErr: true,
		},
		{
			name: "missing API key",
			cfg: Config{
				BaseURL: "https://pbx.example.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUpdateExtensionName(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request parameters
		if r.FormValue("action") != "pbxware.ext.edit" {
			t.Errorf("unexpected action: %s", r.FormValue("action"))
		}
		if r.FormValue("id") != "1001" {
			t.Errorf("unexpected id: %s", r.FormValue("id"))
		}
		if r.FormValue("name") != "John Smith" {
			t.Errorf("unexpected name: %s", r.FormValue("name"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Message: "Extension updated",
		})
	}))
	defer server.Close()

	client, _ := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	err := client.UpdateExtensionName(context.Background(), "1001", "John Smith")
	if err != nil {
		t.Errorf("UpdateExtensionName() error = %v", err)
	}
}

func TestScheduleWakeUpCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("action") != "pbxware.ext.es.wakeupcall.edit" {
			t.Errorf("unexpected action: %s", r.FormValue("action"))
		}
		if r.FormValue("time") != "07:30" {
			t.Errorf("unexpected time: %s", r.FormValue("time"))
		}
		if r.FormValue("enabled") != "1" {
			t.Errorf("expected enabled=1, got: %s", r.FormValue("enabled"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(APIResponse{Success: true})
	}))
	defer server.Close()

	client, _ := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	wakeTime := time.Date(2026, 1, 2, 7, 30, 0, 0, time.UTC)
	err := client.ScheduleWakeUpCall(context.Background(), "1001", wakeTime)
	if err != nil {
		t.Errorf("ScheduleWakeUpCall() error = %v", err)
	}
}

func TestDeleteAllVoicemails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("action") != "pbxware.vm.delete_all" {
			t.Errorf("unexpected action: %s", r.FormValue("action"))
		}
		if r.FormValue("id") != "1001" {
			t.Errorf("unexpected id: %s", r.FormValue("id"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(APIResponse{Success: true})
	}))
	defer server.Close()

	client, _ := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	err := client.DeleteAllVoicemails(context.Background(), "1001")
	if err != nil {
		t.Errorf("DeleteAllVoicemails() error = %v", err)
	}
}

func TestUpdateServicePlan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("action") != "pbxware.ext.edit" {
			t.Errorf("unexpected action: %s", r.FormValue("action"))
		}
		if r.FormValue("service_plan") != "guest-plan" {
			t.Errorf("unexpected service_plan: %s", r.FormValue("service_plan"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(APIResponse{Success: true})
	}))
	defer server.Close()

	client, _ := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	err := client.UpdateServicePlan(context.Background(), "1001", "guest-plan")
	if err != nil {
		t.Errorf("UpdateServicePlan() error = %v", err)
	}
}

func TestListExtensions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("action") != "pbxware.ext.list" {
			t.Errorf("unexpected action: %s", r.URL.Query().Get("action"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Data: json.RawMessage(`[
				{"id": "1", "extension": "1001", "name": "Room 101"},
				{"id": "2", "extension": "1002", "name": "Room 102"}
			]`),
		})
	}))
	defer server.Close()

	client, _ := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	exts, err := client.ListExtensions(context.Background())
	if err != nil {
		t.Errorf("ListExtensions() error = %v", err)
	}

	if len(exts) != 2 {
		t.Errorf("expected 2 extensions, got %d", len(exts))
	}
}

func TestClearVoicemailForGuest(t *testing.T) {
	t.Run("both steps succeed", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(APIResponse{Success: true})
		}))
		defer server.Close()

		client, _ := NewClient(Config{
			BaseURL: server.URL,
			APIKey:  "test-key",
		})

		err := client.ClearVoicemailForGuest(context.Background(), "1001")
		if err != nil {
			t.Errorf("ClearVoicemailForGuest() error = %v", err)
		}
	})

	t.Run("delete fails, greeting succeeds returns VoicemailClearError", func(t *testing.T) {
		deleteFailed := true
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.FormValue("action") == "pbxware.vm.delete_all" && deleteFailed {
				deleteFailed = false // reset for potential future calls
				json.NewEncoder(w).Encode(APIResponse{Success: false, Message: "delete failed"})
				return
			}
			json.NewEncoder(w).Encode(APIResponse{Success: true})
		}))
		defer server.Close()

		client, _ := NewClient(Config{
			BaseURL: server.URL,
			APIKey:  "test-key",
		})

		err := client.ClearVoicemailForGuest(context.Background(), "1001")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var clearErr *VoicemailClearError
		if !errors.As(err, &clearErr) {
			t.Fatalf("expected VoicemailClearError, got %T", err)
		}
		if !clearErr.DeleteFailed {
			t.Error("expected DeleteFailed to be true")
		}
		if clearErr.GreetingFailed {
			t.Error("expected GreetingFailed to be false")
		}
	})

	t.Run("both steps fail returns combined error", func(t *testing.T) {
		deleteFailed := true
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.FormValue("action") == "pbxware.vm.delete_all" && deleteFailed {
				deleteFailed = false
				json.NewEncoder(w).Encode(APIResponse{Success: false, Message: "delete failed"})
				return
			}
			json.NewEncoder(w).Encode(APIResponse{Success: false, Message: "greeting failed"})
		}))
		defer server.Close()

		client, _ := NewClient(Config{
			BaseURL: server.URL,
			APIKey:  "test-key",
		})

		err := client.ClearVoicemailForGuest(context.Background(), "1001")
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var clearErr *VoicemailClearError
		if !errors.As(err, &clearErr) {
			t.Fatalf("expected VoicemailClearError, got %T", err)
		}
		if !clearErr.DeleteFailed {
			t.Error("expected DeleteFailed to be true")
		}
		if !clearErr.GreetingFailed {
			t.Error("expected GreetingFailed to be true")
		}
	})
}

func TestAddExtension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("action") != "pbxware.ext.add" {
			t.Errorf("unexpected action: %s", r.FormValue("action"))
		}
		if r.FormValue("name") != "Room 101" {
			t.Errorf("unexpected name: %s", r.FormValue("name"))
		}
		if r.FormValue("ext") != "1001" {
			t.Errorf("unexpected ext: %s", r.FormValue("ext"))
		}
		if r.FormValue("prot") != "sip" {
			t.Errorf("unexpected prot: %s", r.FormValue("prot"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Data:    json.RawMessage(`{"id": "5", "extension": "1001", "name": "Room 101"}`),
		})
	}))
	defer server.Close()

	client, _ := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	ext, err := client.AddExtension(context.Background(), map[string]string{
		"name": "Room 101",
		"ext":  "1001",
		"prot": "sip",
	})
	if err != nil {
		t.Errorf("AddExtension() error = %v", err)
	}
	if ext == nil {
		t.Fatal("expected extension, got nil")
	}
	if ext.ID != "5" {
		t.Errorf("expected ID=5, got %s", ext.ID)
	}
	if ext.Extension != "1001" {
		t.Errorf("expected Extension=1001, got %s", ext.Extension)
	}
}

func TestAddExtensionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(APIResponse{
			Success: false,
			Message: "extension number already exists",
		})
	}))
	defer server.Close()

	client, _ := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	_, err := client.AddExtension(context.Background(), map[string]string{
		"name": "Room 101",
		"ext":  "1001",
		"prot": "sip",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDeleteExtension(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("action") != "pbxware.ext.delete" {
			t.Errorf("unexpected action: %s", r.FormValue("action"))
		}
		if r.FormValue("id") != "5" {
			t.Errorf("unexpected id: %s", r.FormValue("id"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(APIResponse{
			Success: true,
			Message: "Extension deleted",
		})
	}))
	defer server.Close()

	client, _ := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	err := client.DeleteExtension(context.Background(), "5")
	if err != nil {
		t.Errorf("DeleteExtension() error = %v", err)
	}
}

func TestDeleteExtensionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(APIResponse{
			Success: false,
			Message: "extension not found",
		})
	}))
	defer server.Close()

	client, _ := NewClient(Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})

	err := client.DeleteExtension(context.Background(), "999")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
