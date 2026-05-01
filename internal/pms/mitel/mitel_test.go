package mitel

import (
	"testing"

	"github.com/sagostin/pbx-hospitality/internal/pms"
)

func TestParseMessage(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    pms.EventType
		room    string
		status  bool
		wantErr bool
	}{
		{
			name:   "check-in room 2129",
			input:  []byte("CHK1  2129"), // 10 chars: CHK(3) + 1(1) + space(1) + 2129(4) + null(1)
			want:   pms.EventCheckIn,
			room:   "2129",
			status: true,
		},
		{
			name:   "check-out room 2129",
			input:  []byte("CHK0  2129"),
			want:   pms.EventCheckOut,
			room:   "2129",
			status: false,
		},
		{
			name:   "message waiting ON for room 101",
			input:  []byte("MW 1   101"),
			want:   pms.EventMessageWaiting,
			room:   "101",
			status: true,
		},
		{
			name:   "message waiting OFF for room 101",
			input:  []byte("MW 0   101"),
			want:   pms.EventMessageWaiting,
			room:   "101",
			status: false,
		},
		{
			name:   "DND on for room 500",
			input:  []byte("DND1   500"),
			want:   pms.EventDND,
			room:   "500",
			status: true,
		},
		{
			name:   "room status occupied",
			input:  []byte("RM 1  1015"),
			want:   pms.EventRoomStatus,
			room:   "1015",
			status: true,
		},
		{
			name:   "name update for room 2129",
			input:  []byte("NAM1  2129"),
			want:   pms.EventNameUpdate,
			room:   "2129",
			status: true,
		},
		{
			name:    "message too short",
			input:   []byte("CHK1"),
			wantErr: true,
		},
		{
			name:    "unknown function code",
			input:   []byte("XXX1 2129"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt, err := ParseMessage(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if evt.Type != tt.want {
				t.Errorf("event type = %v, want %v", evt.Type, tt.want)
			}

			if evt.Room != tt.room {
				t.Errorf("room = %q, want %q", evt.Room, tt.room)
			}

			if evt.Status != tt.status {
				t.Errorf("status = %v, want %v", evt.Status, tt.status)
			}
		})
	}
}

func TestControlCharacters(t *testing.T) {
	if STX != 0x02 {
		t.Errorf("STX = 0x%02x, want 0x02", STX)
	}
	if ETX != 0x03 {
		t.Errorf("ETX = 0x%02x, want 0x03", ETX)
	}
	if ENQ != 0x05 {
		t.Errorf("ENQ = 0x%05x, want 0x05", ENQ)
	}
	if ACK != 0x06 {
		t.Errorf("ACK = 0x%02x, want 0x06", ACK)
	}
	if NAK != 0x15 {
		t.Errorf("NAK = 0x%02x, want 0x15", NAK)
	}
}

func TestParseMessageWithGuestName(t *testing.T) {
	t.Run("NAM with pending name populates GuestName", func(t *testing.T) {
		pending := map[string]string{"2129": "John Smith"}
		evt, err := parseMessage([]byte("NAM1  2129"), pending)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if evt.Type != pms.EventNameUpdate {
			t.Errorf("event type = %v, want %v", evt.Type, pms.EventNameUpdate)
		}
		if evt.Room != "2129" {
			t.Errorf("room = %q, want %q", evt.Room, "2129")
		}
		if evt.GuestName != "John Smith" {
			t.Errorf("guest name = %q, want %q", evt.GuestName, "John Smith")
		}
		if evt.Status != true {
			t.Errorf("status = %v, want true", evt.Status)
		}
		// Pending entry should be consumed
		if _, ok := pending["2129"]; ok {
			t.Errorf("pending entry for room 2129 was not cleared")
		}
	})

	t.Run("NAM without prior pending name registers as pending", func(t *testing.T) {
		pending := map[string]string{}
		evt, err := parseMessage([]byte("NAM1  2129"), pending)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if evt.Type != pms.EventNameUpdate {
			t.Errorf("event type = %v, want %v", evt.Type, pms.EventNameUpdate)
		}
		if evt.GuestName != "" {
			t.Errorf("guest name = %q, want empty string (pending)", evt.GuestName)
		}
		// Pending entry should be registered
		if _, ok := pending["2129"]; !ok {
			t.Errorf("pending entry for room 2129 was not registered")
		}
	})

	t.Run("NAM with nil pendingName leaves GuestName empty", func(t *testing.T) {
		evt, err := parseMessage([]byte("NAM1  2129"), nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if evt.GuestName != "" {
			t.Errorf("guest name = %q, want empty string", evt.GuestName)
		}
	})

	t.Run("name payload message with pending room populates GuestName", func(t *testing.T) {
		// Room 2129 is waiting for a name; name payload arrives
		pending := map[string]string{"2129": ""}
		evt, err := parseMessage([]byte("2129 John Smith      "), pending)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if evt.Type != pms.EventNameUpdate {
			t.Errorf("event type = %v, want %v", evt.Type, pms.EventNameUpdate)
		}
		if evt.Room != "2129" {
			t.Errorf("room = %q, want %q", evt.Room, "2129")
		}
		if evt.GuestName != "John Smith" {
			t.Errorf("guest name = %q, want %q", evt.GuestName, "John Smith")
		}
		if evt.Status != true {
			t.Errorf("status = %v, want true", evt.Status)
		}
		// Pending entry should be consumed
		if _, ok := pending["2129"]; ok {
			t.Errorf("pending entry for room 2129 was not cleared")
		}
	})

	t.Run("name payload without pending room returns error", func(t *testing.T) {
		// No pending room; name payload parsed as generic message
		pending := map[string]string{}
		_, err := parseMessage([]byte("2129 John Smith      "), pending)
		if err == nil {
			t.Errorf("expected error for unrecognized format without pending room, got nil")
		}
	})
}
