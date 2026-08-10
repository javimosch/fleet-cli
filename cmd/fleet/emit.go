// emit.go — manually trigger an event and run all loops listening for it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/javimosch/fleet-cli/internal/runner"
)

// cmdEmit triggers a named event and dispatches the matching `on` loops.
func cmdEmit(args []string) int {
	if len(args) < 2 {
		fail("usage: fleet emit <fleet> <event> [--data <json>] [--dry-run]")
	}
	fleetName, eventName := args[0], args[1]

	var dataJSON string
	dryRun := false
	for i := 2; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--data":
			if i+1 >= len(args) {
				fail("--data requires a JSON value")
			}
			dataJSON = args[i+1]
			i++
		case "--dry-run":
			dryRun = true
		}
	}

	fleet, fleetDir, err := loadFleet(fleetName)
	if err != nil {
		fail("load fleet: %v", err)
	}

	st, q, err := openStores(fleet)
	if err != nil {
		fail("open stores: %v", err)
	}

	data := map[string]interface{}{}
	if dataJSON != "" {
		if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
			fail("parse --data JSON: %v", err)
		}
	}
	event := runner.Event{Name: eventName, Data: data}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	chain, err := runChainFromEvent(ctx, fleet, fleetDir, st, q, dryRun, event)
	if err != nil {
		fail("emit chain: %v", err)
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
