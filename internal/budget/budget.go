// Package budget enforces the outbound limits declared in fleet.yml.
//
// These keys existed for a long time and did nothing: fleet-cli parsed
// outbound_per_day, outbound_per_hour and quiet_hours and never read them, so a
// cold-outreach fleet could declare "at most five a day, none at night" and send
// whatever it liked whenever it liked. That is worse than declaring nothing,
// because it reads as a guarantee. This package makes the guarantee real.
//
// Only outbound loops are gated. What counts as one outbound action is the
// `count` on an `action.executed` event, which is what every outbound loop in
// the fleets already emits; a dry run counts as zero.
package budget

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LogKey is the state key holding the timestamps of outbound actions.
const LogKey = "outbound_log"

// retain caps how far back the log is kept. The longest window we enforce is a
// day, so anything older can never affect a decision.
const retain = 25 * time.Hour

// Decision is the outcome of a pre-run check.
type Decision struct {
	Allowed bool
	// Reason is empty when allowed, and human-readable when not -- it is
	// surfaced to the operator, so it says which limit and what to wait for.
	Reason string
}

// QuietHours reports whether now falls inside a "HH:MM-HH:MM [Location]"
// window. An empty spec is not a window, and never blocks.
//
// A window whose end is before its start crosses midnight ("22:00-08:00"), which
// is the normal shape for this setting and the reason it cannot be a simple
// range comparison.
func QuietHours(spec string, now time.Time) (bool, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false, nil
	}
	fields := strings.Fields(spec)
	if len(fields) > 1 {
		loc, err := time.LoadLocation(fields[len(fields)-1])
		if err != nil {
			return false, fmt.Errorf("quiet_hours: unknown timezone %q: %w", fields[len(fields)-1], err)
		}
		now = now.In(loc)
	}
	parts := strings.SplitN(fields[0], "-", 2)
	if len(parts) != 2 {
		return false, fmt.Errorf("quiet_hours: want HH:MM-HH:MM, got %q", fields[0])
	}
	start, err := parseHM(parts[0])
	if err != nil {
		return false, err
	}
	end, err := parseHM(parts[1])
	if err != nil {
		return false, err
	}
	cur := now.Hour()*60 + now.Minute()
	if start == end {
		return false, nil
	}
	if start < end {
		return cur >= start && cur < end, nil
	}
	// Crosses midnight.
	return cur >= start || cur < end, nil
}

func parseHM(s string) (int, error) {
	s = strings.TrimSpace(s)
	hm := strings.SplitN(s, ":", 2)
	if len(hm) != 2 {
		return 0, fmt.Errorf("quiet_hours: want HH:MM, got %q", s)
	}
	h, err1 := strconv.Atoi(hm[0])
	m, err2 := strconv.Atoi(hm[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("quiet_hours: not a time of day: %q", s)
	}
	return h*60 + m, nil
}

// Check decides whether an outbound loop may run now.
//
// A limit of 0 is "no limit", matching how the other numeric keys in fleet.yml
// read, so a fleet that wants to send nothing should not declare 0 -- it should
// not declare an outbound loop.
func Check(perDay, perHour int, quiet string, log []time.Time, now time.Time) (Decision, error) {
	if quiet != "" {
		in, err := QuietHours(quiet, now)
		if err != nil {
			return Decision{Allowed: false, Reason: err.Error()}, err
		}
		if in {
			return Decision{false, fmt.Sprintf("quiet hours (%s)", quiet)}, nil
		}
	}
	if perHour > 0 {
		n := countSince(log, now.Add(-time.Hour))
		if n >= perHour {
			return Decision{false, fmt.Sprintf("outbound_per_hour reached (%d in the last hour, limit %d)", n, perHour)}, nil
		}
	}
	if perDay > 0 {
		n := countSince(log, now.Add(-24*time.Hour))
		if n >= perDay {
			return Decision{false, fmt.Sprintf("outbound_per_day reached (%d in the last 24h, limit %d)", n, perDay)}, nil
		}
	}
	return Decision{Allowed: true}, nil
}

func countSince(log []time.Time, cutoff time.Time) int {
	n := 0
	for _, t := range log {
		if t.After(cutoff) {
			n++
		}
	}
	return n
}

// Record appends n actions at now and drops entries too old to matter.
func Record(log []time.Time, n int, now time.Time) []time.Time {
	for i := 0; i < n; i++ {
		log = append(log, now)
	}
	cutoff := now.Add(-retain)
	out := log[:0]
	for _, t := range log {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

// Decode and Encode move the log through the state store, which holds JSON.
func Decode(v interface{}) []time.Time {
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]time.Time, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			out = append(out, t)
		}
	}
	return out
}

func Encode(log []time.Time) []string {
	out := make([]string, 0, len(log))
	for _, t := range log {
		out = append(out, t.UTC().Format(time.RFC3339))
	}
	return out
}
