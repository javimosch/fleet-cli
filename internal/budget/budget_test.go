package budget

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestQuietHoursCrossingMidnight(t *testing.T) {
	// The normal shape for this setting, and the reason it cannot be a plain
	// range comparison: the window wraps.
	spec := "22:00-08:00 Europe/Paris"
	cases := []struct {
		utc  string
		want bool
		why  string
	}{
		{"2026-09-13T21:00:00Z", true, "23:00 Paris, inside"},
		{"2026-09-13T01:00:00Z", true, "03:00 Paris, inside"},
		{"2026-09-13T12:00:00Z", false, "14:00 Paris, outside"},
		{"2026-09-13T06:30:00Z", false, "08:30 Paris, just outside the end"},
		{"2026-09-13T20:00:00Z", true, "22:00 Paris, exactly the start is inside"},
	}
	for _, c := range cases {
		got, err := QuietHours(spec, at(c.utc))
		if err != nil {
			t.Fatalf("%s: %v", c.why, err)
		}
		if got != c.want {
			t.Errorf("%s (%s): got %v want %v", c.why, c.utc, got, c.want)
		}
	}
}

func TestQuietHoursEmptyNeverBlocks(t *testing.T) {
	got, err := QuietHours("", at("2026-09-13T03:00:00Z"))
	if err != nil || got {
		t.Fatalf("empty spec must not block: got %v err %v", got, err)
	}
}

func TestQuietHoursRejectsGarbage(t *testing.T) {
	// A malformed window must be an error, not silently "not quiet" -- the
	// whole point is that a declared limit is a real one.
	for _, bad := range []string{"22:00", "25:00-08:00", "22-08", "22:00-08:00 Mars/Olympus"} {
		if _, err := QuietHours(bad, time.Now()); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestPerHourAndPerDay(t *testing.T) {
	now := at("2026-09-13T12:00:00Z")
	log := []time.Time{
		now.Add(-10 * time.Minute),
		now.Add(-20 * time.Minute), // 2 in the last hour
		now.Add(-5 * time.Hour),
		now.Add(-9 * time.Hour), // 4 in the last day
	}
	if d, _ := Check(0, 2, "", log, now); d.Allowed {
		t.Error("per-hour limit of 2 with 2 in the hour must block")
	}
	if d, _ := Check(0, 3, "", log, now); !d.Allowed {
		t.Error("per-hour limit of 3 with 2 in the hour must allow")
	}
	if d, _ := Check(4, 0, "", log, now); d.Allowed {
		t.Error("per-day limit of 4 with 4 in the day must block")
	}
	if d, _ := Check(5, 0, "", log, now); !d.Allowed {
		t.Error("per-day limit of 5 with 4 in the day must allow")
	}
}

func TestZeroMeansNoLimit(t *testing.T) {
	now := at("2026-09-13T12:00:00Z")
	log := []time.Time{now, now, now, now, now}
	if d, _ := Check(0, 0, "", log, now); !d.Allowed {
		t.Error("0 must mean no limit, matching how the other numeric keys read")
	}
}

func TestRecordPrunesBeyondADay(t *testing.T) {
	now := at("2026-09-13T12:00:00Z")
	log := []time.Time{now.Add(-48 * time.Hour), now.Add(-2 * time.Hour)}
	log = Record(log, 3, now)
	if len(log) != 4 {
		t.Fatalf("want 4 entries (1 kept + 3 new), got %d", len(log))
	}
	for _, ts := range log {
		if ts.Before(now.Add(-retain)) {
			t.Error("entry older than the retention window survived")
		}
	}
}

func TestRoundTripThroughState(t *testing.T) {
	now := at("2026-09-13T12:00:00Z").UTC()
	enc := Encode([]time.Time{now})
	// state stores JSON, so it comes back as []interface{} of strings.
	var asAny []interface{}
	for _, s := range enc {
		asAny = append(asAny, s)
	}
	got := Decode(asAny)
	if len(got) != 1 || !got[0].Equal(now) {
		t.Fatalf("round trip lost the timestamp: %v", got)
	}
}

func TestDecodeTolerature(t *testing.T) {
	// Anything unparseable in state must be ignored rather than panic: this
	// gates outbound, so it has to fail safe by counting fewer, not crashing.
	got := Decode([]interface{}{"nonsense", 42, "2026-09-13T12:00:00Z"})
	if len(got) != 1 {
		t.Fatalf("want 1 usable entry, got %d", len(got))
	}
}
