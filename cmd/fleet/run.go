// run.go — single-loop execution and event chaining.
package main

import (
	"context"
	"time"

	"github.com/javimosch/fleet-cli/internal/config"
	"github.com/javimosch/fleet-cli/internal/hitl"
	"github.com/javimosch/fleet-cli/internal/runner"
	"github.com/javimosch/fleet-cli/internal/state"
)

// loopOutput is the JSON shape of one loop run.
type loopOutput struct {
	Loop      string                 `json:"loop"`
	Status    string                 `json:"status"`
	DryRun    bool                   `json:"dry_run"`
	Events    []runner.Event         `json:"events"`
	Proposals int                    `json:"proposals"`
	Log       string                 `json:"log"`
	Cost      map[string]interface{} `json:"cost"`
}

// runSummary is the top-level output when a run chains through events.
type runSummary struct {
	loopOutput
	Chain []loopOutput `json:"chain,omitempty"`
}

// cmdRun executes a loop, optionally chaining to event-driven downstream loops.
func cmdRun(args []string) int {
	if len(args) < 2 {
		fail("usage: fleet run <fleet> <loop> [--dry-run] [--no-chain]")
	}
	fleetName, loopName := args[0], args[1]
	fleet, fleetDir, err := loadFleet(fleetName)
	if err != nil {
		fail("load fleet: %v", err)
	}
	if fleet.GetLoop(loopName) == nil {
		fail("loop %s not found", loopName)
	}

	dryRun, noChain := false, false
	for _, a := range args[2:] {
		switch a {
		case "--dry-run":
			dryRun = true
		case "--no-chain":
			noChain = true
		}
	}

	st, q, err := openStores(fleet)
	if err != nil {
		fail("open stores: %v", err)
	}

	loop := fleet.GetLoop(loopName)
	ctx, cancel := loopContext(loop, fleet)
	defer cancel()

	if noChain {
		lo := runOneLoop(ctx, fleet, fleetDir, st, q, dryRun, loopName)
		if lo.Status != "ok" {
			fail("run %s: %s\n%s", loopName, lo.Status, lo.Log)
		}
		outputJSON(lo)
		return 0
	}

	sum, err := runChainFromLoop(ctx, fleet, fleetDir, st, q, dryRun, loopName)
	if err != nil {
		fail("run chain: %v", err)
	}
	if sum.Status != "ok" {
		fail("run %s: %s\n%s", loopName, sum.Status, sum.Log)
	}
	outputJSON(sum)
	return 0
}

// runOneLoop runs a single loop and records it.
func runOneLoop(ctx context.Context, fleet *config.Fleet, fleetDir string, st *state.Store, q *hitl.Queue, dryRun bool, loopName string) loopOutput {
	loop := fleet.GetLoop(loopName)
	if loop == nil {
		return loopOutput{Loop: loopName, Status: "error", Log: "loop not found"}
	}
	started := time.Now()
	res, err := runner.Run(ctx, fleet, loop, fleetDir, st, q, dryRun)
	finished := time.Now()
	lo := loopOutput{
		Loop:      loopName,
		Status:    status(err),
		DryRun:    dryRun,
		Events:    res.Events,
		Proposals: len(res.Proposals),
		Log:       res.Log,
		Cost:      res.Cost,
	}
	_ = st.RunRecord(loopName, lo.Status, started, finished, map[string]interface{}{
		"dry_run": dryRun,
		"events":  res.Events,
		"cost":    res.Cost,
	})
	return lo
}

// runChainFromLoop runs the requested loop, then BFS-runs any loops triggered by emitted events.
func runChainFromLoop(ctx context.Context, fleet *config.Fleet, fleetDir string, st *state.Store, q *hitl.Queue, dryRun bool, startLoop string) (*runSummary, error) {
	initial := runOneLoop(ctx, fleet, fleetDir, st, q, dryRun, startLoop)
	visited := map[string]bool{startLoop: true}
	chain := runEventBFS(ctx, fleet, fleetDir, st, q, dryRun, initial.Events, visited)
	return &runSummary{loopOutput: initial, Chain: chain}, nil
}

// runChainFromEvent runs all loops triggered by a synthetic event.
func runChainFromEvent(ctx context.Context, fleet *config.Fleet, fleetDir string, st *state.Store, q *hitl.Queue, dryRun bool, event runner.Event) ([]loopOutput, error) {
	visited := map[string]bool{}
	return runEventBFS(ctx, fleet, fleetDir, st, q, dryRun, []runner.Event{event}, visited), nil
}

// runEventBFS walks the fleet graph, running every loop whose `on` list matches a produced event.
func runEventBFS(ctx context.Context, fleet *config.Fleet, fleetDir string, st *state.Store, q *hitl.Queue, dryRun bool, events []runner.Event, visited map[string]bool) []loopOutput {
	chain := []loopOutput{}
	for i := 0; i < len(events); i++ {
		if err := ctx.Err(); err != nil {
			break
		}
		ev := events[i]
		for _, l := range fleet.Loops {
			if visited[l.Name] {
				continue
			}
			if !contains(l.On, ev.Name) {
				continue
			}
			lo := runOneLoop(ctx, fleet, fleetDir, st, q, dryRun, l.Name)
			chain = append(chain, lo)
			visited[l.Name] = true
			if lo.Status == "ok" {
				for _, e := range lo.Events {
					events = append(events, e)
				}
			}
		}
	}
	return chain
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// loopContext returns a context with the loop's timeout, or a background context for zero timeouts.
func loopContext(loop *config.Loop, fleet *config.Fleet) (context.Context, context.CancelFunc) {
	d := loop.TimeoutDuration(fleet.Defaults)
	if d <= 0 {
		return context.Background(), func() {}
	}
	return context.WithTimeout(context.Background(), d)
}

// fleetContext returns a context sized for the longest loop in the fleet,
// used for event chains that may run any listener.
func fleetContext(fleet *config.Fleet) (context.Context, context.CancelFunc) {
	var d time.Duration
	for _, l := range fleet.Loops {
		td := l.TimeoutDuration(fleet.Defaults)
		if td > d {
			d = td
		}
	}
	if d <= 0 {
		return context.Background(), func() {}
	}
	return context.WithTimeout(context.Background(), d)
}
