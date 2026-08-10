// fleet-cli is a minimal fleet orchestrator.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/javimosch/fleet-cli/internal/config"
	"github.com/javimosch/fleet-cli/internal/hitl"
	"github.com/javimosch/fleet-cli/internal/state"
)

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(0)
	}
	cmd := os.Args[1]
	tail := os.Args[2:]

	switch cmd {
	case "help", "-h", "--help":
		printHelp()
	case "init":
		os.Exit(cmdInit(tail))
	case "validate":
		os.Exit(cmdValidate(tail))
	case "plan":
		os.Exit(cmdPlan(tail))
	case "run":
		os.Exit(cmdRun(tail))
	case "emit":
		os.Exit(cmdEmit(tail))
	case "status":
		os.Exit(cmdStatus(tail))
	case "queue":
		os.Exit(cmdQueue(tail))
	case "approve", "reject":
		os.Exit(cmdDecision(cmd, tail))
	case "state":
		os.Exit(cmdState(tail))
	case "install":
		os.Exit(cmdInstall(tail))
	case "uninstall":
		os.Exit(cmdUninstall(tail))
	default:
		fail("unknown command: %s", cmd)
	}
}

func printHelp() {
	fmt.Println(`fleet-cli - orchestrate agentic loops

Commands:
  init --name <name> --repo <repo> [--dir <dir>]
  validate <fleet>
  plan <fleet>
  run <fleet> <loop> [--dry-run] [--no-chain]
  emit <fleet> <event> [--data <json>] [--dry-run]
  status <fleet>
  queue <fleet>
  approve <fleet> <proposal-id> [--reason <reason>]
  reject <fleet> <proposal-id> [--reason <reason>]
  state <fleet> get <key>
  state <fleet> set <key> <value>
  install <fleet> [--system] [--no-start]
  uninstall <fleet> [--system]

Global flags:
  -f <path>   fleet directory (default ./fleets/<name>)
  --json      output JSON`)
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func outputJSON(v interface{}) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func fleetPath(name string) string {
	if f := flagValue([]string{}, "f"); f != "" {
		return f
	}
	if d := os.Getenv("FLEET_DIR"); d != "" {
		return d
	}
	return filepath.Join(".", "fleets", name)
}

func flagValue(args []string, name string) string {
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	var v string
	fs.StringVar(&v, name, "", "")
	_ = fs.Parse(args)
	return v
}

func loadFleet(name string) (*config.Fleet, string, error) {
	path := fleetPath(name)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	fleet, err := config.Load(filepath.Join(absPath, "fleet.yml"))
	if err != nil {
		return nil, "", err
	}
	return fleet, absPath, nil
}

func openStores(fleet *config.Fleet) (*state.Store, *hitl.Queue, error) {
	dir := stateDir()
	st, err := state.Open(filepath.Join(dir, fleet.Name+".json"))
	if err != nil {
		return nil, nil, err
	}
	q := hitl.Open(filepath.Join(dir, fleet.Name+"-proposals.jsonl"))
	return st, q, nil
}

func stateDir() string {
	d := os.Getenv("FLEET_STATE_DIR")
	if d == "" {
		d = filepath.Join(os.Getenv("HOME"), ".local", "share", "fleet-cli")
	}
	return d
}

// cmdInit scaffolds a new fleet directory.
func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	name := fs.String("name", "", "fleet name")
	repo := fs.String("repo", "", "target repo (owner/name)")
	dir := fs.String("dir", "", "fleet directory (default ./fleets/<name>)")
	if err := fs.Parse(args); err != nil {
		fail("parse flags: %v", err)
	}
	if *name == "" || *repo == "" {
		fail("--name and --repo are required")
	}
	fleetDir := *dir
	if fleetDir == "" {
		fleetDir = filepath.Join(".", "fleets", *name)
	}
	if err := os.MkdirAll(fleetDir, 0o755); err != nil {
		fail("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(fleetDir, "loops"), 0o755); err != nil {
		fail("mkdir loops: %v", err)
	}
	fleetYML := fmt.Sprintf(initFleetYML, *name, *repo)
	if err := os.WriteFile(filepath.Join(fleetDir, "fleet.yml"), []byte(fleetYML), 0o644); err != nil {
		fail("write fleet.yml: %v", err)
	}
	// Placeholder observe loop.
	observe := `#!/usr/bin/env bash
set -euo pipefail
repo="${FLEET_REPO}"
gh api "repos/$repo" --jq '{stars:.stargazers_count, forks:.forks_count, open_issues:.open_issues_count, updated_at:.updated_at}' > "$FLEET_RUN_DIR/result.json"
`
	writeLoop(fleetDir, "observe.sh", observe)
	fmt.Printf("created fleet at %s\n", fleetDir)
	return 0
}

func writeLoop(fleetDir, name, body string) {
	path := filepath.Join(fleetDir, "loops", name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		fail("write %s: %v", name, err)
	}
}

var initFleetYML = `version: 1
name: %s
repo: %s

defaults:
  timeout: 300s

state:
  backend: json

budgets:
  outbound_per_day: 10
  outbound_per_hour: 2

hitl:
  mode: required
  expire_after: 48h

loops:
  - name: observe
    schedule: "daily@09:00"
    command: ./loops/observe.sh
    outbound: false
`

// cmdValidate validates a fleet.yml.
func cmdValidate(args []string) int {
	if len(args) < 1 {
		fail("usage: fleet validate <fleet>")
	}
	_, _, err := loadFleet(args[0])
	if err != nil {
		fail("validate: %v", err)
	}
	fmt.Println("valid")
	return 0
}

// cmdPlan prints the next expected run times.
func cmdPlan(args []string) int {
	if len(args) < 1 {
		fail("usage: fleet plan <fleet>")
	}
	fleet, _, err := loadFleet(args[0])
	if err != nil {
		fail("load fleet: %v", err)
	}
	now := time.Now()
	var rows []map[string]interface{}
	for _, l := range fleet.Loops {
		next, _ := l.NextRun(now)
		rows = append(rows, map[string]interface{}{
			"loop":      l.Name,
			"schedule":  l.Schedule,
			"next_run":  next.Format(time.RFC3339),
			"outbound":  l.Outbound,
			"rate":      l.Rate,
		})
	}
	outputJSON(rows)
	return 0
}


func status(err error) string {
	if err == nil {
		return "ok"
	}
	if strings.Contains(err.Error(), "budget") {
		return "skipped"
	}
	return "error"
}

// cmdStatus shows fleet state and recent runs.
func cmdStatus(args []string) int {
	if len(args) < 1 {
		fail("usage: fleet status <fleet>")
	}
	fleet, _, err := loadFleet(args[0])
	if err != nil {
		fail("load fleet: %v", err)
	}
	st, q, err := openStores(fleet)
	if err != nil {
		fail("open stores: %v", err)
	}
	pending, _ := q.List(hitl.Pending)
	approved, _ := q.List(hitl.Approved)
	rejected, _ := q.List(hitl.Rejected)
	outputJSON(map[string]interface{}{
		"fleet":     fleet.Name,
		"state":     st.All(),
		"pending":   len(pending),
		"approved":  len(approved),
		"rejected":  len(rejected),
	})
	return 0
}

// cmdQueue lists pending proposals.
func cmdQueue(args []string) int {
	if len(args) < 1 {
		fail("usage: fleet queue <fleet>")
	}
	fleet, _, err := loadFleet(args[0])
	if err != nil {
		fail("load fleet: %v", err)
	}
	_, q, err := openStores(fleet)
	if err != nil {
		fail("open stores: %v", err)
	}
	proposals, err := q.List("")
	if err != nil {
		fail("list queue: %v", err)
	}
	outputJSON(proposals)
	return 0
}

// cmdDecision approves or rejects a proposal.
func cmdDecision(decision string, args []string) int {
	fs := flag.NewFlagSet(decision, flag.ContinueOnError)
	reason := fs.String("reason", "", "reason for decision")
	if err := fs.Parse(args); err != nil {
		fail("parse flags: %v", err)
	}
	remaining := fs.Args()
	if len(remaining) < 2 {
		fail("usage: fleet %s <fleet> <proposal-id>", decision)
	}
	fleetName, proposalID := remaining[0], remaining[1]
	fleet, _, err := loadFleet(fleetName)
	if err != nil {
		fail("load fleet: %v", err)
	}
	_, q, err := openStores(fleet)
	if err != nil {
		fail("open stores: %v", err)
	}
	st := hitl.Approved
	if decision == "reject" {
		st = hitl.Rejected
	}
	if err := q.UpdateStatus(proposalID, st, *reason, "cli"); err != nil {
		fail("update status: %v", err)
	}
	fmt.Printf("%s %s\n", decision, proposalID)
	return 0
}

// cmdState reads or writes state keys.
func cmdState(args []string) int {
	if len(args) < 2 {
		fail("usage: fleet state <fleet> get <key> | set <key> <value>")
	}
	fleetName := args[0]
	fleet, _, err := loadFleet(fleetName)
	if err != nil {
		fail("load fleet: %v", err)
	}
	st, _, err := openStores(fleet)
	if err != nil {
		fail("open stores: %v", err)
	}
	sub := args[1]
	switch sub {
	case "get":
		if len(args) < 3 {
			fail("state get <key>")
		}
		v, ok := st.Get(args[2])
		if !ok {
			fmt.Println("null")
			return 0
		}
		outputJSON(v)
	case "set":
		if len(args) < 4 {
			fail("state set <key> <value>")
		}
		var value interface{}
		if err := json.Unmarshal([]byte(args[3]), &value); err != nil {
			value = args[3]
		}
		if err := st.Set(args[2], value); err != nil {
			fail("set: %v", err)
		}
		fmt.Println("ok")
	case "all":
		outputJSON(st.All())
	default:
		fail("unknown state subcommand: %s", sub)
	}
	return 0
}
