package fias

import (
	"testing"

	"github.com/sagostin/pbx-hospitality/internal/pms"
)

func TestParseRecord(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      pms.EventType
		room      string
		guestName string
		status    bool
		wantErr   bool
	}{
		{
			name:      "guest check-in",
			input:     "GI|RN1015|GNSmith, John|DA260102|TI1430|",
			want:      pms.EventCheckIn,
			room:      "1015",
			guestName: "Smith, John",
			status:    true,
		},
		{
			name:   "guest check-out",
			input:  "GO|RN1015|DA260102|TI1100|",
			want:   pms.EventCheckOut,
			room:   "1015",
			status: false,
		},
		{
			name:   "message waiting ON",
			input:  "MW|RN1015|FL1|",
			want:   pms.EventMessageWaiting,
			room:   "1015",
			status: true,
		},
		{
			name:   "message waiting OFF",
			input:  "MW|RN1015|FL0|",
			want:   pms.EventMessageWaiting,
			room:   "1015",
			status: false,
		},
		{
			name:   "room status occupied",
			input:  "RS|RN2020|FL1|",
			want:   pms.EventRoomStatus,
			room:   "2020",
			status: true,
		},
		{
			name:   "wake-up call",
			input:  "WK|RN1015|TI0700|",
			want:   pms.EventWakeUp,
			room:   "1015",
			status: true,
		},
		{
			name:    "invalid format",
			input:   "XX",
			wantErr: true,
		},
		{
			name:    "unknown record type",
			input:   "ZZ|RN1015|",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt, err := ParseRecord(tt.input)

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

			if tt.guestName != "" && evt.GuestName != tt.guestName {
				t.Errorf("guest name = %q, want %q", evt.GuestName, tt.guestName)
			}

			if evt.Status != tt.status {
				t.Errorf("status = %v, want %v", evt.Status, tt.status)
			}
		})
	}
}

func TestParseLinkRecords(t *testing.T) {
	// Link records should return errLinkRecord
	linkRecords := []string{
		"LR|DA|TI|RN|GN|FL|RI|",
		"LS|",
		"LA|",
		"LE|",
	}

	for _, line := range linkRecords {
		t.Run(line[:2], func(t *testing.T) {
		// Link records should return ErrLinkRecord
			if err != ErrLinkRecord {
				t.Errorf("expected ErrLinkRecord, got %v", err)
			}
		})
	}
}

func TestFieldExtraction(t *testing.T) {
	line := "GI|RN1015|GNDoe, Jane|DA260115|TI0900|RI12345|G#001|"
	evt, err := ParseRecord(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check metadata contains all fields
	expected := map[string]string{
		"RN": "1015",
		"GN": "Doe, Jane",
		"DA": "260115",
		"TI": "0900",
		"RI": "12345",
		"G#": "001",
	}

	for key, want := range expected {
		if got := evt.Metadata[key]; got != want {
			t.Errorf("metadata[%s] = %q, want %q", key, got, want)
		}
	}
}
