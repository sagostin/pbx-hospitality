package tenant

import "testing"

func TestRoomMapperIndividual(t *testing.T) {
	m := NewRoomMapper("")
	if err := m.SetMapping("101", "1101"); err != nil {
		t.Fatalf("SetMapping: %v", err)
	}
	if err := m.SetMapping("102", "1102"); err != nil {
		t.Fatalf("SetMapping: %v", err)
	}

	got, err := m.GetExtension("101")
	if err != nil {
		t.Fatalf("GetExtension: %v", err)
	}
	if got != "1101" {
		t.Errorf("GetExtension(101) = %q, want %q", got, "1101")
	}

	got, err = m.GetExtension("102")
	if err != nil {
		t.Fatalf("GetExtension: %v", err)
	}
	if got != "1102" {
		t.Errorf("GetExtension(102) = %q, want %q", got, "1102")
	}
}

func TestRoomMapperRange(t *testing.T) {
	m := NewRoomMapper("")
	if err := m.SetRange("101", "105", "201", "205"); err != nil {
		t.Fatalf("SetRange: %v", err)
	}

	cases := []struct {
		room string
		want string
	}{
		{"101", "201"},
		{"102", "202"},
		{"103", "203"},
		{"104", "204"},
		{"105", "205"},
	}
	for _, tc := range cases {
		got, err := m.GetExtension(tc.room)
		if err != nil {
			t.Errorf("GetExtension(%q) error: %v", tc.room, err)
			continue
		}
		if got != tc.want {
			t.Errorf("GetExtension(%q) = %q, want %q", tc.room, got, tc.want)
		}
	}

	// Room outside the range falls back to identity (no prefix).
	got, err := m.GetExtension("999")
	if err != nil {
		t.Fatalf("GetExtension: %v", err)
	}
	if got != "999" {
		t.Errorf("GetExtension(999) = %q, want %q (identity fallback)", got, "999")
	}
}

func TestRoomMapperPattern(t *testing.T) {
	m := NewRoomMapper("")
	if err := m.SetPattern(`^1[0-2]\d{2}$`, "5000"); err != nil {
		t.Fatalf("SetPattern: %v", err)
	}

	for _, room := range []string{"1001", "1101", "1299"} {
		got, _ := m.GetExtension(room)
		if got != "5000" {
			t.Errorf("GetExtension(%q) = %q, want %q", room, got, "5000")
		}
	}

	// Room outside the pattern falls back.
	got, _ := m.GetExtension("2001")
	if got != "2001" {
		t.Errorf("GetExtension(2001) = %q, want %q (identity fallback)", got, "2001")
	}
}

func TestRoomMapperInvalidPattern(t *testing.T) {
	m := NewRoomMapper("")
	if err := m.SetPattern("[invalid", "5000"); err == nil {
		t.Error("expected error for invalid regex, got nil")
	}
}

func TestRoomMapperPrefix(t *testing.T) {
	m := NewRoomMapper("1")
	got, err := m.GetExtension("2345")
	if err != nil {
		t.Fatalf("GetExtension: %v", err)
	}
	if got != "12345" {
		t.Errorf("GetExtension(2345) with prefix=1 = %q, want %q", got, "12345")
	}

	// Individual mapping overrides prefix.
	if err := m.SetMapping("2345", "9999"); err != nil {
		t.Fatalf("SetMapping: %v", err)
	}
	got, _ = m.GetExtension("2345")
	if got != "9999" {
		t.Errorf("individual mapping should override prefix: got %q, want %q", got, "9999")
	}
}

func TestRoomMapperRangeValidation(t *testing.T) {
	m := NewRoomMapper("")
	if err := m.SetRange("105", "101", "201", "205"); err == nil {
		t.Error("expected error when room_end < room_start, got nil")
	}
}

func TestRoomMapperGetRoom(t *testing.T) {
	m := NewRoomMapper("")
	_ = m.SetMapping("101", "1101")

	got, _ := m.GetRoom("1101")
	if got != "101" {
		t.Errorf("GetRoom(1101) = %q, want %q", got, "101")
	}
}

func TestRoomMapperRemove(t *testing.T) {
	m := NewRoomMapper("")
	_ = m.SetMapping("101", "1101")
	_ = m.SetMapping("102", "1102")

	m.RemoveMapping("101")
	if _, err := m.GetExtension("101"); err != nil {
		t.Fatalf("GetExtension after remove: %v", err)
	}

	// Should now fall back to identity (no prefix).
	got, _ := m.GetExtension("101")
	if got != "101" {
		t.Errorf("after RemoveMapping(101), GetExtension(101) = %q, want %q (identity)", got, "101")
	}

	// The other mapping is untouched.
	got, _ = m.GetExtension("102")
	if got != "1102" {
		t.Errorf("GetExtension(102) = %q, want %q", got, "1102")
	}
}
