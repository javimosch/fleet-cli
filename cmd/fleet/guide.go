package main

import (
	"fmt"
	"os"
)

var fleetVersion = "0.1.0"

const outputContractVersion = "1.0"

func guideData() map[string]interface{} {
	return map[string]interface{}{
		"fleet-cli": "a local orchestrator for event-driven agentic fleets",
		"one_liner": "fleet-cli runs ordinary loop scripts, records their JSON results, queues outbound proposals for human approval, and chains loops through events.",
		"model": map[string]interface{}{
			"binary":   "the Go binary is a thin scheduler, state ledger, proposal queue, and systemd renderer",
			"loops":    "ordinary executable files declared in fleet.yml; domain logic stays outside the binary",
			"state":    "per-fleet JSON state and run history under FLEET_STATE_DIR or ~/.local/share/fleet-cli",
			"approval": "outbound proposals are queued and must be approved before execution",
			"events":   "a loop can emit named events that trigger downstream loops in breadth-first order",
		},
		"loop": []string{
			"write or scaffold a fleet.yml and executable loops",
			"run fleet validate and fleet plan before touching anything",
			"run a read-only or dry-run loop and inspect its JSON result",
			"review fleet queue and approve or reject outbound proposals",
			"run the execution loop only after approval and with its own safety gates",
			"inspect fleet status and the costs/run ledger",
		},
		"concepts": map[string]interface{}{
			"fleet":    "a named YAML graph of loops, budgets, HITL policy, and channels",
			"loop":     "an executable command with optional schedule, timeout, event listeners, and outbound behavior",
			"run_dir":  "the temporary FLEET_RUN_DIR where a loop writes result.json, proposals.jsonl, state.json, and cost.json",
			"proposal": "a JSONL outbound action awaiting a human decision",
			"dry_run":  "executes the loop but does not persist state or queue proposals",
			"channel":  "an optional best-effort cuzz notification destination",
		},
		"commands": map[string]interface{}{
			"local": []string{
				"fleet init --name <name> --repo <owner/name>",
				"fleet validate <fleet>",
				"fleet plan <fleet>",
				"fleet run <fleet> <loop> [--dry-run] [--no-chain]",
				"fleet emit <fleet> <event> [--data <json>] [--dry-run]",
				"fleet status <fleet>",
				"fleet queue <fleet>",
				"fleet approve|reject <fleet> <proposal-id> [--reason <reason>]",
				"fleet state <fleet> get|set|all ...",
				"fleet install|uninstall <fleet> [--system]",
			},
			"introspection": []string{
				"fleet guide [--human]",
				"fleet help-json",
				"fleet version",
				"fleet feedback \"<message>\" [--kind bug|idea|praise|note] [--context <text>]",
				"fleet update [--check|--force]",
			},
		},
		"examples": []map[string]interface{}{
			{"goal": "inspect a fleet without side effects", "do": []string{"fleet validate machin-growth", "fleet plan machin-growth", "fleet run machin-growth prospect --dry-run --no-chain"}},
			{"goal": "review and execute approved work", "do": []string{"fleet queue machin-growth", "fleet approve machin-growth <proposal-id> --reason \"reviewed\"", "fleet run machin-growth execute --no-chain"}},
			{"goal": "report a problem from an agent machine", "do": []string{"FEEDBACK_RELAY=off fleet feedback \"the loop produced an invalid result\" --kind bug"}},
		},
		"gotchas": []string{
			"stdout is command data; progress and loop stderr are context on stderr",
			"dry-run does not persist state or queue proposals, but the loop itself may still call external tools unless it honors FLEET_DRY_RUN",
			"outbound loops must declare requires_approval: true",
			"PATH shims improve safety but are not a security boundary against a shell command that deliberately escapes them",
			"fleet install can enable systemd units; inspect generated units before using --system",
			"fleet feedback is best-effort and never fails the caller; FEEDBACK_RELAY=off disables relay delivery",
			"fleet update compares the running binary's SHA-256 content hash, verifies and smoke-tests a candidate, then atomically swaps it while retaining a timestamped backup",
		},
		"version":  fleetVersion,
		"see_also": []string{"fleet help-json", "fleet version"},
	}
}

func cmdGuide(args []string) int {
	human := false
	for _, arg := range args {
		if arg == "--human" {
			human = true
			continue
		}
		failCode(80, "invalid_arguments", "usage: fleet guide [--human]", "fleet help-json")
	}
	if human {
		fmt.Fprintln(os.Stdout, guideHuman())
		return 0
	}
	outputJSON(map[string]interface{}{"ok": true, "version": outputContractVersion, "guide": guideData()})
	return 0
}

func guideHuman() string {
	return `# fleet guide

fleet-cli runs executable loops declared in fleet.yml. A loop writes structured
files in FLEET_RUN_DIR; the binary persists safe state, queues outbound
proposals for human approval, and follows emitted events to downstream loops.

## Safe operating loop

1. fleet validate <fleet>
2. fleet plan <fleet>
3. fleet run <fleet> <loop> --dry-run --no-chain
4. fleet queue <fleet>
5. fleet approve or reject each outbound proposal
6. fleet run <fleet> execute --no-chain
7. fleet status <fleet>

Use fleet help-json for the complete command catalog. Use fleet feedback to
report a bug; it is best-effort and can be disabled with FEEDBACK_RELAY=off.
Use fleet update --check to detect a newer release without changing the binary.
`
}

func cmdHelpJSON() int {
	outputJSON(map[string]interface{}{
		"version":     fleetVersion,
		"output":      "json",
		"interactive": false,
		"commands":    helpCatalog(),
		"exit_codes":  map[string]string{"0": "success", "5": "update available", "80": "input/validation", "90": "precondition/resource", "100": "external/integration", "110": "internal"}, "env": []string{"FLEET_DIR", "FLEET_STATE_DIR", "FLEET_LIVE", "FLEET_CUZZ_BIN", "CUZZ_URL", "CUZZ_TOKEN", "CUZZ_AGENT", "FEEDBACK_RELAY", "FLEET_UPDATE_URL"},
		"see_also": []string{"fleet guide", "fleet version"},
	})
	return 0
}

func helpCatalog() map[string]interface{} {
	return map[string]interface{}{
		"help":      map[string]interface{}{"args": []string{}, "flags": []string{"--help", "-h"}, "auth": false},
		"init":      map[string]interface{}{"args": []string{}, "flags": []string{"--name <name>", "--repo <owner/name>", "--dir <path>"}, "auth": false},
		"validate":  map[string]interface{}{"args": []string{"<fleet>"}, "flags": []string{}, "auth": false},
		"plan":      map[string]interface{}{"args": []string{"<fleet>"}, "flags": []string{}, "auth": false},
		"run":       map[string]interface{}{"args": []string{"<fleet>", "<loop>"}, "flags": []string{"--dry-run", "--no-chain"}, "auth": false},
		"emit":      map[string]interface{}{"args": []string{"<fleet>", "<event>"}, "flags": []string{"--data <json>", "--dry-run"}, "auth": false},
		"status":    map[string]interface{}{"args": []string{"<fleet>"}, "flags": []string{}, "auth": false},
		"queue":     map[string]interface{}{"args": []string{"<fleet>"}, "flags": []string{}, "auth": false},
		"approve":   map[string]interface{}{"args": []string{"<fleet>", "<proposal-id>"}, "flags": []string{"--reason <reason>"}, "auth": false},
		"reject":    map[string]interface{}{"args": []string{"<fleet>", "<proposal-id>"}, "flags": []string{"--reason <reason>"}, "auth": false},
		"state":     map[string]interface{}{"args": []string{"<fleet>", "get|set|all"}, "flags": []string{}, "auth": false},
		"install":   map[string]interface{}{"args": []string{"<fleet>"}, "flags": []string{"--system", "--no-start"}, "auth": false},
		"uninstall": map[string]interface{}{"args": []string{"<fleet>"}, "flags": []string{"--system"}, "auth": false},
		"guide":     map[string]interface{}{"args": []string{}, "flags": []string{"--human"}, "auth": false},
		"help-json": map[string]interface{}{"args": []string{}, "flags": []string{}, "auth": false},
		"version":   map[string]interface{}{"args": []string{}, "flags": []string{}, "auth": false},
		"feedback":  map[string]interface{}{"args": []string{"<message>"}, "flags": []string{"--kind bug|idea|praise|note", "--context <text>"}, "auth": false},
		"update":    map[string]interface{}{"args": []string{}, "flags": []string{"--check", "--force"}, "auth": false},
	}
}

func cmdVersion() int {
	outputJSON(map[string]interface{}{"ok": true, "tool": "fleet", "version": fleetVersion, "output_contract": outputContractVersion})
	return 0
}
