package mitel

import (
	"testing"

	"github.com/topsoffice/bicom-hospitality/internal/pms"
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
		t.Errorf("ENQ = 0x%02x, want 0x05", ENQ)
	}
	if ACK != 0x06 {
		t.Errorf("ACK = 0x%02x, want 0x06", ACK)
	}
	if NAK != 0x15 {
		t.Errorf("NAK = 0x%02x, want 0x15", NAK)
	}
}
