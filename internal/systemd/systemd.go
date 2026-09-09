// Package systemd renders user/system systemd units for a fleet.
package systemd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/javimosch/fleet-cli/internal/config"
)

// Options control how units are installed.
type Options struct {
	FleetName string
	FleetDir  string
	Binary    string
	System    bool
	User      string
}

// Install renders and enables timers for a fleet.
func Install(fleet *config.Fleet, opts Options) error {
	dir, err := unitDir(opts.System)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	for _, loop := range fleet.Loops {
		if loop.Schedule == "" && len(loop.On) == 0 && loop.Mode != "daemon" {
			continue
		}
		name := unitName(fleet.Name, loop.Name)
		service := renderService(name, opts, fleet.Name, loop)
		if err := os.WriteFile(filepath.Join(dir, name+".service"), []byte(service), 0o644); err != nil {
			return err
		}

		if loop.Schedule == "" {
			continue
		}
		trigger, err := renderTimerTrigger(loop.Schedule)
		if err != nil {
			return err
		}
		timer := renderTimer(name, trigger, loop)
		if err := os.WriteFile(filepath.Join(dir, name+".timer"), []byte(timer), 0o644); err != nil {
			return err
		}
	}

	return reload(opts.System)
}

// Uninstall disables and removes units for a fleet.
func Uninstall(fleetName string, system bool) error {
	dir, err := unitDir(system)
	if err != nil {
		return err
	}
	prefix := "fleet-" + fleetName + "-"

	// Stop and disable first so symlinks in *.wants are removed and daemon services terminate.
	if files, err := os.ReadDir(dir); err == nil {
		for _, e := range files {
			if strings.HasPrefix(e.Name(), prefix) && (strings.HasSuffix(e.Name(), ".timer") || strings.HasSuffix(e.Name(), ".service")) {
				_ = stopUnit(e.Name(), system)
				_ = disableUnit(e.Name(), system)
			}
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return reload(system)
}

func disableUnit(name string, system bool) error {
	scope := "--user"
	if system {
		scope = "--system"
	}
	cmd := exec.Command("systemctl", scope, "disable", "--now", name)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func stopUnit(name string, system bool) error {
	scope := "--user"
	if system {
		scope = "--system"
	}
	cmd := exec.Command("systemctl", scope, "stop", name)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// UnitPaths returns the paths where units were written.
func UnitPaths(fleetName string, fleet *config.Fleet, system bool) ([]string, error) {
	dir, err := unitDir(system)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, loop := range fleet.Loops {
		if loop.Schedule == "" && len(loop.On) == 0 && loop.Mode != "daemon" {
			continue
		}
		name := unitName(fleetName, loop.Name)
		paths = append(paths, filepath.Join(dir, name+".service"))
		if loop.Schedule != "" {
			paths = append(paths, filepath.Join(dir, name+".timer"))
		}
	}
	return paths, nil
}

// reload runs systemctl daemon-reload for the requested scope.
func reload(system bool) error {
	scope := "--user"
	if system {
		scope = "--system"
	}
	cmd := exec.Command("systemctl", scope, "daemon-reload")
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func unitDir(system bool) (string, error) {
	if system {
		return "/etc/systemd/system", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func unitName(fleet, loop string) string {
	return fmt.Sprintf("fleet-%s-%s", fleet, loop)
}

func renderService(name string, opts Options, fleetName string, loop config.Loop) string {
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		home = "/"
	}

	bin := opts.Binary
	if bin == "" {
		bin = "fleet"
	}
	if loop.Mode == "" {
		loop.Mode = "oneshot"
	}

	serviceType := "oneshot"
	extra := ""
	install := ""
	if loop.Mode == "daemon" {
		serviceType = "simple"
		secs := int(loop.RuntimeMaxDuration().Seconds())
		extra = fmt.Sprintf("RuntimeMaxSec=%d\nRestart=always\nRestartSec=10\n", secs)
		target := "default.target"
		if opts.System {
			target = "multi-user.target"
		}
		install = fmt.Sprintf("\n[Install]\nWantedBy=%s\n", target)
	}

	loopEnv := ""
	for k, v := range loop.Env {
		loopEnv += fmt.Sprintf("Environment=\"%s=%s\"\n", k, v)
	}

	// Optional resource limits. Emitted only when set, so units that don't ask
	// for them are unchanged.
	limits := ""
	if loop.Nice != nil {
		limits += fmt.Sprintf("Nice=%d\n", *loop.Nice)
	}
	if loop.IOClass != "" {
		limits += fmt.Sprintf("IOSchedulingClass=%s\n", loop.IOClass)
	}
	if loop.CPUWeight != nil {
		limits += fmt.Sprintf("CPUWeight=%d\n", *loop.CPUWeight)
	}

	// Every loop talks to something over the network (GitHub, an LLM, a site
	// being scraped), and the timers are Persistent=true, so at boot they all
	// fire the moment the timer arms. Without this the first run of each loop
	// races DHCP and dies on "error connecting to api.github.com". The old
	// monotonic OnBootSec=2min triggers hid this by accident.
	return fmt.Sprintf(`[Unit]
Description=Fleet loop %s for %s
Wants=network-online.target
After=network-online.target

[Service]
Type=%s
ExecStart=%s run %s %s
WorkingDirectory=%s
Environment="PATH=/usr/local/bin:/usr/bin:/bin"
Environment="HOME=%s"
Environment="FLEET_DIR=%s"
%s%s%sEnvironmentFile=-/etc/default/fleet-cli
%s`, name, fleetName, serviceType, bin, fleetName, loop.Name, opts.FleetDir, home, opts.FleetDir, extra, loopEnv, limits, install)
}

func renderTimer(name, trigger string, loop config.Loop) string {
	description := fmt.Sprintf("Run %s loop for %s", loop.Name, name)
	return fmt.Sprintf(`[Unit]
Description=%s

[Timer]%s

[Install]
WantedBy=timers.target
`, description, trigger)
}

// renderTimerTrigger converts a fleet-cli schedule to a systemd timer trigger.
func renderTimerTrigger(schedule string) (string, error) {
	switch schedule {
	case "":
		return "", nil
	case "hourly", "every 1h":
		return intervalTrigger(time.Hour), nil
	case "every 15m":
		return intervalTrigger(15 * time.Minute), nil
	}
	if len(schedule) > 6 && schedule[:6] == "daily@" {
		t, err := time.Parse("15:04", schedule[6:])
		if err != nil {
			return "", fmt.Errorf("invalid daily schedule %q: %w", schedule, err)
		}
		return fmt.Sprintf("\nOnCalendar=*-*-* %02d:%02d:00\nPersistent=true", t.Hour(), t.Minute()), nil
	}
	if len(schedule) > 7 && schedule[:7] == "@every " {
		d, err := time.ParseDuration(schedule[7:])
		if err != nil {
			return "", fmt.Errorf("invalid @every schedule %q: %w", schedule, err)
		}
		if d <= 0 {
			return "", fmt.Errorf("invalid @every schedule %q: interval must be positive", schedule)
		}
		return intervalTrigger(d), nil
	}
	return "", fmt.Errorf("unsupported schedule %q", schedule)
}

// intervalTrigger renders a repeating interval as a wall-clock OnCalendar
// trigger rather than the monotonic OnBootSec/OnUnitActiveSec pair.
//
// Monotonic timers anchor their next elapse on the *service's* last activation.
// The units here are Type=oneshot, so that anchor goes stale (or disappears
// entirely) whenever the unit files are rewritten on a live box, and the timer
// lands in SubState=elapsed with NextElapseUSecMonotonic=infinity — dead until
// someone starts the service by hand; neither restart nor a full
// uninstall/reinstall brings it back. Every OnCalendar timer on the fleet
// survived the same reinstalls untouched, so intervals are expressed as
// calendar expressions: they recompute the next elapse from the wall clock and
// cannot be stranded.
func intervalTrigger(d time.Duration) string {
	mins := int((d + 30*time.Second) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	if mins < 60 {
		// Sub-hour steps must divide 60 or the last window of each hour is
		// short. 31–59m has no such step, so it falls through to hourly.
		if step := snapDivisor(mins, 60); step < 60 {
			return jitteredCalendarTrigger(fmt.Sprintf("*:0/%d:00", step), time.Duration(step)*time.Minute)
		}
		mins = 60
	}

	hours := snapDivisor((mins+30)/60, 24)
	span := time.Duration(hours) * time.Hour
	if hours >= 24 {
		return jitteredCalendarTrigger("00:00:00", span)
	}
	if hours == 1 {
		return jitteredCalendarTrigger("*:00:00", span)
	}
	slots := make([]string, 0, 24/hours)
	for h := 0; h < 24; h += hours {
		slots = append(slots, fmt.Sprintf("%02d", h))
	}
	return jitteredCalendarTrigger(fmt.Sprintf("%s:00:00", strings.Join(slots, ",")), span)
}

// snapDivisor rounds n up to the next divisor of period so the rendered
// calendar expression repeats evenly. Rounding up rather than down keeps an
// odd interval from silently costing more runs per day than it asked for.
func snapDivisor(n, period int) int {
	if n < 1 {
		return 1
	}
	for i := n; i < period; i++ {
		if period%i == 0 {
			return i
		}
	}
	return period
}

func calendarTrigger(spec string) string {
	return fmt.Sprintf("\nOnCalendar=*-*-* %s\nPersistent=true", spec)
}

// jitteredCalendarTrigger is calendarTrigger plus a spread. Calendar
// expressions align to the wall clock, so without this every interval loop in
// every fleet fires on the same second — and Persistent=true means they all
// fire together again at boot and at install. The delay is a tenth of the
// interval, capped at a minute, which is enough to stagger them without
// meaningfully moving any single run.
func jitteredCalendarTrigger(spec string, d time.Duration) string {
	jitter := d / 10
	if jitter > time.Minute {
		jitter = time.Minute
	}
	if jitter < 5*time.Second {
		jitter = 5 * time.Second
	}
	return fmt.Sprintf("%s\nRandomizedDelaySec=%d", calendarTrigger(spec), int(jitter.Seconds()))
}
