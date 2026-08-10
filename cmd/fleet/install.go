// install.go — render and manage systemd units for a fleet.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/javimosch/fleet-cli/internal/config"
	"github.com/javimosch/fleet-cli/internal/systemd"
)

// cmdInstall renders and enables systemd timers for a fleet.
func cmdInstall(args []string) int {
	var system, noStart bool
	var positional []string
	for _, a := range args {
		switch a {
		case "-system", "--system":
			system = true
		case "-no-start", "--no-start":
			noStart = true
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) < 1 {
		failCode(80, "invalid_arguments", "usage: fleet install <fleet> [--system] [--no-start]", "fleet help-json")
	}
	fleetName := positional[0]
	fleet, fleetDir, err := loadFleet(fleetName)
	if err != nil {
		failCode(92, "resource_not_found", fmt.Sprintf("load fleet: %v", err), "fleet init --name <name> --repo <owner/name>")
	}

	bin, err := binaryPath()
	if err != nil {
		failCode(110, "internal_error", fmt.Sprintf("resolve fleet binary: %v", err))
	}

	opts := systemd.Options{
		FleetName: fleet.Name,
		FleetDir:  fleetDir,
		Binary:    bin,
		System:    system,
	}
	if err := systemd.Install(fleet, opts); err != nil {
		failCode(90, "resource_unavailable", fmt.Sprintf("install units: %v", err), "inspect systemd availability and permissions")
	}

	if err := enableUnits(fleet, fleetName, system, !noStart); err != nil {
		failCode(100, "systemd_error", fmt.Sprintf("enable units: %v", err), "check systemctl status and retry deliberately")
	}

	paths, _ := systemd.UnitPaths(fleetName, fleet, system)
	outputJSON(map[string]interface{}{
		"fleet":       fleetName,
		"units":       paths,
		"started":     !noStart,
		"system_wide": system,
	})
	return 0
}

// cmdUninstall removes systemd units for a fleet.
func cmdUninstall(args []string) int {
	var system bool
	var positional []string
	for _, a := range args {
		switch a {
		case "-system", "--system":
			system = true
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) < 1 {
		failCode(80, "invalid_arguments", "usage: fleet uninstall <fleet> [--system]", "fleet help-json")
	}
	fleetName := positional[0]
	if err := systemd.Uninstall(fleetName, system); err != nil {
		failCode(90, "resource_unavailable", fmt.Sprintf("uninstall units: %v", err), "inspect systemd availability and permissions")
	}
	outputJSON(map[string]interface{}{"ok": true, "uninstalled": true, "fleet": fleetName})
	return 0
}

// enableUnits enables (and optionally starts) all timers and daemon services for a fleet.
func enableUnits(fleet *config.Fleet, fleetName string, system bool, start bool) error {
	scope := "--user"
	if system {
		scope = "--system"
	}

	for _, loop := range fleet.Loops {
		if loop.Mode == "daemon" {
			name := fmt.Sprintf("fleet-%s-%s.service", fleetName, loop.Name)
			cmd := exec.Command("systemctl", scope, "enable", name)
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("enable %s: %w", name, err)
			}
			if start {
				_ = exec.Command("systemctl", scope, "start", name).Run()
			}
			continue
		}
		if loop.Schedule == "" {
			continue
		}
		name := fmt.Sprintf("fleet-%s-%s.timer", fleetName, loop.Name)
		cmd := exec.Command("systemctl", scope, "enable", name)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("enable %s: %w", name, err)
		}
		if start {
			_ = exec.Command("systemctl", scope, "start", name).Run()
		}
	}
	return nil
}

// binaryPath returns the absolute path to the current fleet binary.
func binaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}
