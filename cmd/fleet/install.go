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
		fail("usage: fleet install <fleet> [--system] [--no-start]")
	}
	fleetName := positional[0]
	fleet, fleetDir, err := loadFleet(fleetName)
	if err != nil {
		fail("load fleet: %v", err)
	}

	bin, err := binaryPath()
	if err != nil {
		fail("resolve fleet binary: %v", err)
	}

	opts := systemd.Options{
		FleetName: fleet.Name,
		FleetDir:  fleetDir,
		Binary:    bin,
		System:    system,
	}
	if err := systemd.Install(fleet, opts); err != nil {
		fail("install units: %v", err)
	}

	if err := enableTimers(fleet, fleetName, system, !noStart); err != nil {
		fail("enable timers: %v", err)
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
		fail("usage: fleet uninstall <fleet> [--system]")
	}
	fleetName := positional[0]
	if err := systemd.Uninstall(fleetName, system); err != nil {
		fail("uninstall units: %v", err)
	}
	fmt.Printf("uninstalled fleet-%s units\n", fleetName)
	return 0
}

// enableTimers enables (and optionally starts) all timers for a fleet.
func enableTimers(fleet *config.Fleet, fleetName string, system bool, start bool) error {
	scope := "--user"
	if system {
		scope = "--system"
	}

	for _, loop := range fleet.Loops {
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
