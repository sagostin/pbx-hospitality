package tigertms

import (
	"context"
	"testing"
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
	ctx := context.Background()

	err := adapter.Connect(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !adapter.Connected() {
		t.Error("expected adapter to be connected")
	}

	adapter.Close()
}

func TestAdapterClose(t *testing.T) {
	adapter, _ := NewAdapter("localhost", 8080)
	ctx := context.Background()
	adapter.Connect(ctx)
	adapter.Close()

	if adapter.Connected() {
		t.Error("expected adapter to be disconnected after Close")
	}
}

func TestHandlerCreation(t *testing.T) {
	adapter, _ := NewAdapter("localhost", 8080)
	handler := NewHandler(adapter.(*Adapter))

	if handler == nil {
		t.Error("expected handler to be created")
	}

	if handler.adapter == nil {
		t.Error("expected handler to have adapter")
	}
}
