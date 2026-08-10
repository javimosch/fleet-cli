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

	return fmt.Sprintf(`[Unit]
Description=Fleet loop %s for %s

[Service]
Type=%s
ExecStart=%s run %s %s
WorkingDirectory=%s
Environment="PATH=/usr/local/bin:/usr/bin:/bin"
Environment="FLEET_DIR=%s"
%s%s`, name, fleetName, serviceType, bin, fleetName, loop.Name, opts.FleetDir, opts.FleetDir, extra, install)
}

func renderTimer(name, trigger string, loop config.Loop) string {
	description := fmt.Sprintf("Run %s loop for %s", loop.Name, name)
	return fmt.Sprintf(`[Unit]
Description=%s

[Timer]%s
Persistent=true

[Install]
WantedBy=timers.target
`, description, trigger)
}

// renderTimerTrigger converts a fleet-cli schedule to a systemd timer trigger.
func renderTimerTrigger(schedule string) (string, error) {
	switch schedule {
	case "":
		return "", nil
	case "hourly":
		return "\nOnBootSec=2min\nOnUnitActiveSec=1h", nil
	case "every 15m":
		return "\nOnBootSec=2min\nOnUnitActiveSec=15min", nil
	case "every 1h":
		return "\nOnBootSec=2min\nOnUnitActiveSec=1h", nil
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
		return fmt.Sprintf("\nOnBootSec=2min\nOnUnitActiveSec=%s", d.String()), nil
	}
	return "", fmt.Errorf("unsupported schedule %q", schedule)
}
