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

	// Queue proposals (only when not dry-running).
	if !dryRun {
		for _, p := range res.Proposals {
			p.Fleet = fleet.Name
			p.Loop = loop.Name
			if _, err := q.Add(p); err != nil {
				return res, fmt.Errorf("queue proposal: %w", err)
			}
		}
	}

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

func stateDir() string {
	d := os.Getenv("FLEET_STATE_DIR")
	if d == "" {
		d = filepath.Join(os.Getenv("HOME"), ".local", "share", "fleet-cli")
	}
	return d
}
