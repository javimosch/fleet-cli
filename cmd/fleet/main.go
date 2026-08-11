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

	"github.com/javimosch/fleet-cli/internal/channels"
	"github.com/javimosch/fleet-cli/internal/config"
	"github.com/javimosch/fleet-cli/internal/hitl"
	"github.com/javimosch/fleet-cli/internal/relais"
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
	case "help-json", "--help-json":
		os.Exit(cmdHelpJSON())
	case "guide":
		os.Exit(cmdGuide(tail))
	case "version":
		os.Exit(cmdVersion())
	case "feedback":
		os.Exit(cmdFeedback(tail))
	case "update":
		os.Exit(cmdUpdate(tail))
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
	case "relais-poll":
		os.Exit(cmdRelaisPoll(tail))
	case "approve", "reject":
		os.Exit(cmdDecision(cmd, tail))
	case "state":
		os.Exit(cmdState(tail))
	case "install":
		os.Exit(cmdInstall(tail))
	case "uninstall":
		os.Exit(cmdUninstall(tail))
	default:
		failCode(80, "unknown_command", fmt.Sprintf("unknown command: %s", cmd), "fleet help-json")
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
  relais-poll <fleet>
  state <fleet> get <key>
  state <fleet> set <key> <value>
  install <fleet> [--system] [--no-start]
  uninstall <fleet> [--system]
  guide [--human]
  help-json
  version
  feedback \"<message>\" [--kind bug|idea|praise|note] [--context <text>]
  update [--check|--force]

Environment:
  FLEET_DIR         fleet directory override
  FLEET_STATE_DIR   state directory override
  FEEDBACK_RELAY    feedback relay URL, or off`)
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
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	name := fs.String("name", "", "fleet name")
	repo := fs.String("repo", "", "target repo (owner/name)")
	dir := fs.String("dir", "", "fleet directory (default ./fleets/<name>)")
	if err := fs.Parse(args); err != nil {
		failCode(80, "invalid_arguments", fmt.Sprintf("parse flags: %v", err))
	}
	if *name == "" || *repo == "" {
		failCode(82, "missing_required_argument", "--name and --repo are required", "fleet init --name <name> --repo <owner/name>")
	}
	fleetDir := *dir
	if fleetDir == "" {
		fleetDir = filepath.Join(".", "fleets", *name)
	}
	if err := os.MkdirAll(fleetDir, 0o755); err != nil {
		failCode(90, "resource_unavailable", fmt.Sprintf("mkdir: %v", err), "choose a writable --dir")
	}
	if err := os.MkdirAll(filepath.Join(fleetDir, "loops"), 0o755); err != nil {
		failCode(90, "resource_unavailable", fmt.Sprintf("mkdir loops: %v", err), "choose a writable --dir")
	}
	fleetYML := fmt.Sprintf(initFleetYML, *name, *repo)
	if err := os.WriteFile(filepath.Join(fleetDir, "fleet.yml"), []byte(fleetYML), 0o644); err != nil {
		failCode(90, "resource_unavailable", fmt.Sprintf("write fleet.yml: %v", err), "choose a writable --dir")
	}
	// Placeholder observe loop.
	observe := `#!/usr/bin/env bash
set -euo pipefail
repo="${FLEET_REPO}"
gh api "repos/$repo" --jq '{stars:.stargazers_count, forks:.forks_count, open_issues:.open_issues_count, updated_at:.updated_at}' > "$FLEET_RUN_DIR/result.json"
`
	writeLoop(fleetDir, "observe.sh", observe)
	outputJSON(map[string]interface{}{"ok": true, "created": true, "fleet_dir": fleetDir})
	return 0
}

func writeLoop(fleetDir, name, body string) {
	path := filepath.Join(fleetDir, "loops", name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		failCode(90, "resource_unavailable", fmt.Sprintf("write %s: %v", name, err), "choose a writable --dir")
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
		failCode(80, "invalid_arguments", "usage: fleet validate <fleet>", "fleet help-json")
	}
	_, _, err := loadFleet(args[0])
	if err != nil {
		failCode(82, "validation_error", fmt.Sprintf("validate: %v", err), "inspect fleet.yml")
	}
	outputJSON(map[string]interface{}{"ok": true, "valid": true})
	return 0
}

// cmdPlan prints the next expected run times.
func cmdPlan(args []string) int {
	if len(args) < 1 {
		failCode(80, "invalid_arguments", "usage: fleet plan <fleet>", "fleet help-json")
	}
	fleet, _, err := loadFleet(args[0])
	if err != nil {
		failCode(92, "resource_not_found", fmt.Sprintf("load fleet: %v", err), "fleet init --name <name> --repo <owner/name>")
	}
	now := time.Now()
	var rows []map[string]interface{}
	for _, l := range fleet.Loops {
		next, _ := l.NextRun(now)
		rows = append(rows, map[string]interface{}{
			"loop":     l.Name,
			"schedule": l.Schedule,
			"next_run": next.Format(time.RFC3339),
			"outbound": l.Outbound,
			"rate":     l.Rate,
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
		failCode(80, "invalid_arguments", "usage: fleet status <fleet>", "fleet help-json")
	}
	fleet, _, err := loadFleet(args[0])
	if err != nil {
		failCode(92, "resource_not_found", fmt.Sprintf("load fleet: %v", err), "fleet init --name <name> --repo <owner/name>")
	}
	st, q, err := openStores(fleet)
	if err != nil {
		failCode(90, "state_unavailable", fmt.Sprintf("open stores: %v", err), "set FLEET_STATE_DIR to a writable directory")
	}
	pending, _ := q.List(hitl.Pending)
	approved, _ := q.List(hitl.Approved)
	rejected, _ := q.List(hitl.Rejected)
	outputJSON(map[string]interface{}{
		"fleet":    fleet.Name,
		"state":    st.All(),
		"pending":  len(pending),
		"approved": len(approved),
		"rejected": len(rejected),
	})
	return 0
}

// cmdQueue lists pending proposals.
func cmdQueue(args []string) int {
	if len(args) < 1 {
		failCode(80, "invalid_arguments", "usage: fleet queue <fleet>", "fleet help-json")
	}
	fleet, _, err := loadFleet(args[0])
	if err != nil {
		failCode(92, "resource_not_found", fmt.Sprintf("load fleet: %v", err), "fleet init --name <name> --repo <owner/name>")
	}
	_, q, err := openStores(fleet)
	if err != nil {
		failCode(90, "state_unavailable", fmt.Sprintf("open stores: %v", err), "set FLEET_STATE_DIR to a writable directory")
	}
	proposals, err := q.List("")
	if err != nil {
		failCode(90, "queue_unavailable", fmt.Sprintf("list queue: %v", err), "check FLEET_STATE_DIR")
	}
	outputJSON(proposals)
	return 0
}

// cmdRelaisPoll checks relais inboxes for pending proposals and applies decisions.
func cmdRelaisPoll(args []string) int {
	if len(args) < 1 {
		failCode(80, "invalid_arguments", "usage: fleet relais-poll <fleet>", "fleet help-json")
	}
	fleet, _, err := loadFleet(args[0])
	if err != nil {
		failCode(92, "resource_not_found", fmt.Sprintf("load fleet: %v", err), "fleet init --name <name> --repo <owner/name>")
	}
	st, q, err := openStores(fleet)
	if err != nil {
		failCode(90, "state_unavailable", fmt.Sprintf("open stores: %v", err), "set FLEET_STATE_DIR to a writable directory")
	}

	client := relais.NewClient()
	results, err := relais.PollQueue(client, q, st)
	if err != nil {
		failCode(105, "relais_poll_failed", fmt.Sprintf("poll: %v", err), "check RELAIS_URL and network")
	}

	// Best-effort cuzz notification for each applied decision.
	m := channels.NewManager(fleet)
	for _, r := range results {
		if r.Decision == "" || r.Error != "" {
			continue
		}
		_ = m.Send("ops", "decision", fmt.Sprintf("%s %s", r.Decision, r.ID), map[string]interface{}{
			"proposal_id": r.ID,
			"decision":    r.Decision,
			"approver":    r.By,
			"reason":      "relais",
		})
	}

	outputJSON(map[string]interface{}{"ok": true, "decisions": results})
	return 0
}

// cmdDecision approves or rejects a proposal.
func cmdDecision(decision string, args []string) int {
	// Extract --reason manually so it can appear before or after positional args.
	reason := ""
	clean := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--reason" {
			if i+1 < len(args) {
				reason = args[i+1]
				i++
			}
			continue
		}
		clean = append(clean, a)
	}
	if len(clean) < 2 {
		failCode(80, "invalid_arguments", fmt.Sprintf("usage: fleet %s <fleet> <proposal-id> [--reason <reason>]", decision), "fleet help-json")
	}
	fleetName, proposalID := clean[0], clean[1]
	fleet, _, err := loadFleet(fleetName)
	if err != nil {
		failCode(92, "resource_not_found", fmt.Sprintf("load fleet: %v", err), "fleet init --name <name> --repo <owner/name>")
	}
	_, q, err := openStores(fleet)
	if err != nil {
		failCode(90, "state_unavailable", fmt.Sprintf("open stores: %v", err), "set FLEET_STATE_DIR to a writable directory")
	}
	st := hitl.Approved
	if decision == "reject" {
		st = hitl.Rejected
	}
	if err := q.UpdateStatus(proposalID, st, reason, "cli"); err != nil {
		failCode(92, "proposal_not_found", fmt.Sprintf("update status: %v", err), "fleet queue <fleet>")
	}

	// Best-effort cuzz notification.
	m := channels.NewManager(fleet)
	_ = m.Send("ops", "decision", fmt.Sprintf("%s %s", decision, proposalID), map[string]interface{}{
		"proposal_id": proposalID,
		"decision":    decision,
		"reason":      reason,
		"approver":    "cli",
	})

	outputJSON(map[string]interface{}{"ok": true, "decision": decision, "proposal_id": proposalID})
	return 0
}

// cmdState reads or writes state keys.
func cmdState(args []string) int {
	if len(args) < 2 {
		failCode(80, "invalid_arguments", "usage: fleet state <fleet> get <key> | set <key> <value>", "fleet help-json")
	}
	fleetName := args[0]
	fleet, _, err := loadFleet(fleetName)
	if err != nil {
		failCode(92, "resource_not_found", fmt.Sprintf("load fleet: %v", err), "fleet init --name <name> --repo <owner/name>")
	}
	st, _, err := openStores(fleet)
	if err != nil {
		failCode(90, "state_unavailable", fmt.Sprintf("open stores: %v", err), "set FLEET_STATE_DIR to a writable directory")
	}
	sub := args[1]
	switch sub {
	case "get":
		if len(args) < 3 {
			failCode(80, "invalid_arguments", "state get <key>", "fleet help-json")
		}
		v, ok := st.Get(args[2])
		if !ok {
			outputJSON(nil)
			return 0
		}
		outputJSON(v)
	case "set":
		if len(args) < 4 {
			failCode(80, "invalid_arguments", "state set <key> <value>", "fleet help-json")
		}
		var value interface{}
		if err := json.Unmarshal([]byte(args[3]), &value); err != nil {
			value = args[3]
		}
		if err := st.Set(args[2], value); err != nil {
			failCode(90, "state_unavailable", fmt.Sprintf("set: %v", err), "set FLEET_STATE_DIR to a writable directory")
		}
		outputJSON(map[string]interface{}{"ok": true})
	case "all":
		outputJSON(st.All())
	default:
		failCode(80, "invalid_arguments", fmt.Sprintf("unknown state subcommand: %s", sub), "fleet help-json")
	}
	return 0
}
