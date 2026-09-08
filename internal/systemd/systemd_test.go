package systemd

import (
	"strings"
	"testing"

	"github.com/javimosch/fleet-cli/internal/config"
)

// Every interval schedule must render as OnCalendar. Monotonic
// OnUnitActiveSec triggers strand themselves on reinstall (see intervalTrigger).
func TestRenderTimerTriggerUsesCalendar(t *testing.T) {
	cases := map[string]string{
		"@every 5m":   "\nOnCalendar=*-*-* *:0/5:00\nPersistent=true\nRandomizedDelaySec=30",
		"@every 15m":  "\nOnCalendar=*-*-* *:0/15:00\nPersistent=true\nRandomizedDelaySec=60",
		"@every 30m":  "\nOnCalendar=*-*-* *:0/30:00\nPersistent=true\nRandomizedDelaySec=60",
		"@every 1h":   "\nOnCalendar=*-*-* *:00:00\nPersistent=true\nRandomizedDelaySec=60",
		"@every 6h":   "\nOnCalendar=*-*-* 00,06,12,18:00:00\nPersistent=true\nRandomizedDelaySec=60",
		"@every 24h":  "\nOnCalendar=*-*-* 00:00:00\nPersistent=true\nRandomizedDelaySec=60",
		"hourly":      "\nOnCalendar=*-*-* *:00:00\nPersistent=true\nRandomizedDelaySec=60",
		"every 1h":    "\nOnCalendar=*-*-* *:00:00\nPersistent=true\nRandomizedDelaySec=60",
		"every 15m":   "\nOnCalendar=*-*-* *:0/15:00\nPersistent=true\nRandomizedDelaySec=60",
		"daily@09:00": "\nOnCalendar=*-*-* 09:00:00\nPersistent=true",
		"daily@03:30": "\nOnCalendar=*-*-* 03:30:00\nPersistent=true",
	}
	for schedule, want := range cases {
		got, err := renderTimerTrigger(schedule)
		if err != nil {
			t.Fatalf("renderTimerTrigger(%q): %v", schedule, err)
		}
		if got != want {
			t.Errorf("renderTimerTrigger(%q) = %q, want %q", schedule, got, want)
		}
		if strings.Contains(got, "OnUnitActiveSec") || strings.Contains(got, "OnBootSec") {
			t.Errorf("renderTimerTrigger(%q) rendered a monotonic trigger: %q", schedule, got)
		}
	}
}

// Odd intervals snap up to a divisor so the cadence stays even and never
// costs more runs per day than the schedule asked for.
func TestRenderTimerTriggerSnapsOddIntervals(t *testing.T) {
	cases := map[string]string{
		"@every 7m":  "*:0/10:00",         // 7 -> 10, the next divisor of 60
		"@every 45m": "*:00:00",           // no sub-hour step fits -> hourly
		"@every 5h":  "00,06,12,18:00:00", // 5 -> 6, the next divisor of 24
		"@every 13h": "00:00:00",          // 13 -> 24, daily
		"@every 30s": "*:0/1:00",          // clamped to the 1min floor
	}
	for schedule, want := range cases {
		got, err := renderTimerTrigger(schedule)
		if err != nil {
			t.Fatalf("renderTimerTrigger(%q): %v", schedule, err)
		}
		if !strings.HasPrefix(got, "\nOnCalendar=*-*-* "+want+"\n") {
			t.Errorf("renderTimerTrigger(%q) = %q, want OnCalendar %q", schedule, got, want)
		}
	}
}

func TestRenderTimerTriggerRejectsBadSchedules(t *testing.T) {
	for _, schedule := range []string{"@every 0m", "@every -5m", "@every banana", "daily@25:00", "weekly"} {
		if got, err := renderTimerTrigger(schedule); err == nil {
			t.Errorf("renderTimerTrigger(%q) = %q, want error", schedule, got)
		}
	}
}

// renderTimer must not add its own Persistent= line: the trigger already
// carries one, and a duplicate key is a unit-file parse warning.
func TestRenderTimerHasSinglePersistent(t *testing.T) {
	trigger, err := renderTimerTrigger("@every 30m")
	if err != nil {
		t.Fatal(err)
	}
	unit := renderTimer("demo", trigger, config.Loop{Name: "dispatch"})
	if n := strings.Count(unit, "Persistent=true"); n != 1 {
		t.Errorf("rendered timer has %d Persistent= lines, want 1:\n%s", n, unit)
	}
}
