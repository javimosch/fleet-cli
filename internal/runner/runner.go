// Package runner executes a fleet loop and captures its outputs.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/javimosch/fleet-cli/internal/channels"
	"github.com/javimosch/fleet-cli/internal/config"
	"github.com/javimosch/fleet-cli/internal/hitl"
	"github.com/javimosch/fleet-cli/internal/state"
)

// Result contains the parsed output of a loop.
type Result struct {
	Events    []Event                `json:"events"`
	Proposals []hitl.Proposal        `json:"proposals"`
	StateSet  map[string]interface{} `json:"state_set"`
	StateAppend map[string][]interface{} `json:"state_append"`
	Cost      map[string]interface{} `json:"cost"`
	DryRun    bool                   `json:"dry_run"`
	Log       string                 `json:"log"`
}

// Event is an edge emitted by a loop.
type Event struct {
	Name string                 `json:"name"`
	Data map[string]interface{} `json:"data"`
}

// Run executes a loop command with fleet environment.
func Run(ctx context.Context, fleet *config.Fleet, loop *config.Loop, fleetDir string, st *state.Store, q *hitl.Queue, dryRun bool) (Result, error) {
	cmdPath := loop.Command
	if !filepath.IsAbs(cmdPath) {
		cmdPath = filepath.Join(fleetDir, loop.Command)
	}
	absPath, err := filepath.Abs(cmdPath)
	if err != nil {
		return Result{}, fmt.Errorf("abs command path: %w", err)
	}
	cmdPath = absPath
	runID := fmt.Sprintf("%s-%s-%d", fleet.Name, loop.Name, time.Now().Unix())
	runDir := filepath.Join(os.TempDir(), "fleet-cli-runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("mkdir run dir: %w", err)
	}

	cmd := exec.CommandContext(ctx, cmdPath)
	cmd.Dir = fleetDir
	cmd.Env = os.Environ()

	// Prepend fleet bin/ to PATH so fleets can supply command wrappers (e.g. gh cost tracker).
	binDir := filepath.Join(fleetDir, "bin")
	if info, err := os.Stat(binDir); err == nil && info.IsDir() {
		for i, e := range cmd.Env {
			if len(e) > 5 && e[:5] == "PATH=" {
				cmd.Env[i] = "PATH=" + binDir + string(filepath.ListSeparator) + e[5:]
				break
			}
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf("FLEET_BIN_DIR=%s", binDir))
	}

	// Inject fleet context into environment.
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("FLEET_NAME=%s", fleet.Name),
		fmt.Sprintf("FLEET_REPO=%s", fleet.Repo),
		fmt.Sprintf("FLEET_DIR=%s", fleetDir),
		fmt.Sprintf("FLEET_RUN_DIR=%s", runDir),
		fmt.Sprintf("FLEET_DRY_RUN=%t", dryRun),
		fmt.Sprintf("FLEET_STATE_FILE=%s", stPath(fleet)),
		fmt.Sprintf("FLEET_QUEUE_FILE=%s", queuePath(fleet)),
	)
	if dryRun {
		cmd.Env = append(cmd.Env, "DRY_RUN=1")
	}
	for k, v := range loop.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return Result{Log: out.String()}, fmt.Errorf("loop %s: %w", loop.Name, err)
	}

	res, err := parseOutputs(runDir)
	res.Log = out.String()
	if err != nil {
		return res, fmt.Errorf("parse outputs: %w", err)
	}
	res.DryRun = dryRun

	// Apply state mutations (only when not dry-running).
	if !dryRun {
		for k, v := range res.StateSet {
			if err := st.Set(k, v); err != nil {
				return res, fmt.Errorf("state set %s: %w", k, err)
			}
		}
		for k, arr := range res.StateAppend {
			for _, item := range arr {
				if err := st.Append(k, item); err != nil {
					return res, fmt.Errorf("state append %s: %w", k, err)
				}
			}
		}
	}

	// Queue proposals and notify channels (only when not dry-running).
	m := channels.NewManager(fleet)
	if err != nil {
		_ = m.NotifyError(loop.Name, err, res.Log)
		return res, err
	}
	if !dryRun {
		for _, p := range res.Proposals {
			p.Fleet = fleet.Name
			p.Loop = loop.Name
			id, err := q.Add(p)
			if err != nil {
				return res, fmt.Errorf("queue proposal: %w", err)
			}
			p.ID = id
			_ = m.NotifyHITL(p)
		}
		// Append run cost to state ledger.
		if res.Cost != nil && len(res.Cost) > 0 {
			if err := st.Append("costs", res.Cost); err != nil {
				return res, fmt.Errorf("append cost: %w", err)
			}
		}
	}
	events := make([]map[string]interface{}, len(res.Events))
	for i, e := range res.Events {
		events[i] = map[string]interface{}{"name": e.Name, "data": e.Data}
	}
	_ = m.NotifyEvent(loop.Name, "ok", events, res.Cost)

	return res, nil
}

// parseOutputs reads stdout JSON or run_dir/result.json.
func parseOutputs(runDir string) (Result, error) {
	res := Result{}
	if data, err := os.ReadFile(filepath.Join(runDir, "result.json")); err == nil {
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &res); err != nil {
				return res, err
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(runDir, "proposals.jsonl")); err == nil {
		for _, line := range splitLines(data) {
			if len(line) == 0 {
				continue
			}
			var p hitl.Proposal
			if err := json.Unmarshal(line, &p); err != nil {
				return res, err
			}
			res.Proposals = append(res.Proposals, p)
		}
	}
	if data, err := os.ReadFile(filepath.Join(runDir, "state.json")); err == nil {
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			return res, err
		}
		res.StateSet = m
	}

	// Load explicit cost.json if a loop or wrapper wrote one.
	if data, err := os.ReadFile(filepath.Join(runDir, "cost.json")); err == nil {
		var cost map[string]interface{}
		if err := json.Unmarshal(data, &cost); err == nil {
			res.Cost = cost
		}
	}

	// Aggregate gh call logs from fleet bin/gh wrapper.
	res.Cost = mergeGHCost(res.Cost, runDir)

	return res, nil
}

// splitLines splits byte data by newline.
func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// stPath and queuePath return default paths.
func stPath(fleet *config.Fleet) string {
	return filepath.Join(stateDir(), fleet.Name+".json")
}

func queuePath(fleet *config.Fleet) string {
	return filepath.Join(stateDir(), fleet.Name+"-proposals.jsonl")
}

// mergeGHCost reads gh-calls.jsonl produced by a fleet bin/gh wrapper and returns an aggregated cost map.
func mergeGHCost(cost map[string]interface{}, runDir string) map[string]interface{} {
	if cost == nil {
		cost = make(map[string]interface{})
	}
	data, err := os.ReadFile(filepath.Join(runDir, "gh-calls.jsonl"))
	if err != nil {
		return cost
	}
	type call struct {
		Cmd     string `json:"cmd"`
		Subcmd  string `json:"subcmd"`
		At      string `json:"at"`
	}
	var calls []call
	for _, line := range splitLines(data) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var c call
		if err := json.Unmarshal(line, &c); err != nil {
			continue
		}
		calls = append(calls, c)
	}
	if len(calls) == 0 {
		return cost
	}

	byCmd := make(map[string]int)
	for _, c := range calls {
		key := c.Cmd
		if c.Subcmd != "" && c.Subcmd != c.Cmd {
			key = c.Cmd + " " + c.Subcmd
		}
		byCmd[key]++
	}

	cost["gh_calls"] = len(calls)
	cost["gh_calls_by_cmd"] = byCmd
	cost["unit"] = "gh_api_call"
	cost["rate_limit_notes"] = map[string]string{
		"rest":    "5000/hour authenticated",
		"search":  "10/minute authenticated",
		"comment": "rest endpoint, counted against 5000/hour",
	}
	return cost
}

func stateDir() string {
	d := os.Getenv("FLEET_STATE_DIR")
	if d == "" {
		home := os.Getenv("HOME")
		if home == "" && os.Getuid() == 0 {
			home = "/root"
		}
		if home == "" {
			home = "."
		}
		d = filepath.Join(home, ".local", "share", "fleet-cli")
	}
	return d
}
