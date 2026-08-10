// emit.go — manually trigger an event and run all loops listening for it.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/javimosch/fleet-cli/internal/runner"
)

// cmdEmit triggers a named event and dispatches the matching `on` loops.
func cmdEmit(args []string) int {
	if len(args) < 2 {
		failCode(80, "invalid_arguments", "usage: fleet emit <fleet> <event> [--data <json>] [--dry-run]", "fleet help-json")
	}
	fleetName, eventName := args[0], args[1]

	var dataJSON string
	dryRun := false
	for i := 2; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--data":
			if i+1 >= len(args) {
				failCode(80, "invalid_arguments", "--data requires a JSON value", "fleet help-json")
			}
			dataJSON = args[i+1]
			i++
		case "--dry-run":
			dryRun = true
		}
	}

	fleet, fleetDir, err := loadFleet(fleetName)
	if err != nil {
		failCode(92, "resource_not_found", fmt.Sprintf("load fleet: %v", err), "fleet init --name <name> --repo <owner/name>")
	}

	st, q, err := openStores(fleet)
	if err != nil {
		failCode(90, "state_unavailable", fmt.Sprintf("open stores: %v", err), "set FLEET_STATE_DIR to a writable directory")
	}

	data := map[string]interface{}{}
	if dataJSON != "" {
		if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
			failCode(85, "invalid_json", fmt.Sprintf("parse --data JSON: %v", err), "pass a JSON object to --data")
		}
	}
	event := runner.Event{Name: eventName, Data: data}

	ctx, cancel := fleetContext(fleet)
	defer cancel()

	chain, err := runChainFromEvent(ctx, fleet, fleetDir, st, q, dryRun, event)
	if err != nil {
		failCode(105, "external_error", fmt.Sprintf("emit chain: %v", err), "retry the command after inspecting the loop error")
	}

	outputJSON(map[string]interface{}{
		"event":   eventName,
		"data":    data,
		"dry_run": dryRun,
		"chain":   chain,
	})
	fmt.Fprintf(os.Stderr, "emitted %s, triggered %d loop(s)\n", eventName, len(chain))
	return 0
}
