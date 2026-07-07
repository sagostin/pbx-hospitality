package tenant

import (
	"testing"
	"time"
)

// TestFirstNonEmpty is the helper used by handleWakeUp to look up the wake
// time across multiple metadata keys (FIAS "TI", TigerTMS "wakeup_time",
// raw "TI_RAW").
func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"all empty", []string{"", "", ""}, ""},
		{"first non-empty wins", []string{"TI", "wakeup_time"}, "TI"},
		{"falls through empty", []string{"", "wakeup_time", ""}, "wakeup_time"},
		{"third when earlier empty", []string{"", "", "TI_RAW"}, "TI_RAW"},
		{"no args", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstNonEmpty(tc.in...); got != tc.want {
				t.Errorf("firstNonEmpty(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseWakeUpTime covers the normalization of wake-up time strings
// across the three metadata key shapes we accept:
//
//   - "0730"      (FIAS, HHMM, no separator)
//   - "07:30"     (TigerTMS, HH:MM, with colon)
//   - ""          (rejected)
//
// "now" is anchored to 2026-06-15 09:00 UTC so the past/future
// distinction is deterministic.
func TestParseWakeUpTime(t *testing.T) {
	zone := time.UTC
	anchor := time.Date(2026, 6, 15, 9, 0, 0, 0, zone)

	ten := &Tenant{timezone: zone}

	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"HHMM", "0730", false},
		{"HH:MM", "07:30", false},
		{"blank", "", true},
		{"too short", "73", true},
		{"too long", "07300", true},
		{"bad hour", "2530", true},
		{"bad minute", "0760", true},
		{"non-numeric", "abcd", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ten.parseWakeUpTimeAt(tc.in, anchor)
			if (err != nil) != tc.wantErr {
				t.Errorf("parseWakeUpTimeAt(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
		})
	}
}

// TestParseWakeUpTime_RollsForwardWhenPast covers the "if time has already
// passed today, schedule for tomorrow" rule.
func TestParseWakeUpTime_RollsForwardWhenPast(t *testing.T) {
	zone := time.UTC
	now := time.Date(2026, 6, 15, 9, 0, 0, 0, zone)
	ten := &Tenant{timezone: zone}

	// 08:00 is in the past — expect 2026-06-16 08:00.
	got, err := ten.parseWakeUpTimeAt("0800", now)
	if err != nil {
		t.Fatalf("parseWakeUpTimeAt(0800): %v", err)
	}
	want := time.Date(2026, 6, 16, 8, 0, 0, 0, zone)
	if !got.Equal(want) {
		t.Errorf("from 09:00 with HHMM=0800 = %v, want %v", got, want)
	}

	// 10:00 is in the future — expect today.
	got, err = ten.parseWakeUpTimeAt("1000", now)
	if err != nil {
		t.Fatalf("parseWakeUpTimeAt(1000): %v", err)
	}
	want = time.Date(2026, 6, 15, 10, 0, 0, 0, zone)
	if !got.Equal(want) {
		t.Errorf("from 09:00 with HHMM=1000 = %v, want %v", got, want)
	}
}