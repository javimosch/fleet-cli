// Package channels sends fleet notifications to configured external relays.
package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/javimosch/fleet-cli/internal/config"
	"github.com/javimosch/fleet-cli/internal/hitl"
)

// Manager routes messages to the channels defined in fleet.yml.
type Manager struct {
	fleet *config.Fleet
	bin   string
}

// NewManager returns a channel manager for the fleet.
// It silently becomes a no-op if no cuzz binary is on PATH and no override is set.
func NewManager(fleet *config.Fleet) *Manager {
	bin := os.Getenv("FLEET_CUZZ_BIN")
	if bin == "" {
		if p, err := exec.LookPath("cuzz"); err == nil {
			bin = p
		}
	}
	return &Manager{fleet: fleet, bin: bin}
}

// Send a structured message to a named channel (e.g. "ops" or "alerts").
// It is best-effort and returns an error only for logging; callers should not fail.
func (m *Manager) Send(channelName, kind, summary string, data map[string]interface{}) error {
	cfg, ok := m.fleet.Channels[channelName]
	if !ok {
		return nil // no channel configured
	}
	if cfg.Kind != "cuzz" {
		return fmt.Errorf("unsupported channel kind %q", cfg.Kind)
	}
	if m.bin == "" {
		return fmt.Errorf("cuzz binary not found; set FLEET_CUZZ_BIN or install cuzz in PATH")
	}

	payload := map[string]interface{}{
		"fleet":   m.fleet.Name,
		"channel": channelName,
		"kind":    kind,
		"summary": summary,
		"ts":      time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range data {
		payload[k] = v
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	cuzzChannel := cfg.Channel
	if cuzzChannel == "" {
		cuzzChannel = m.fleet.Name
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cuzzKind := cuzzKindFor(kind)
	cmd := exec.CommandContext(ctx, m.bin, "send",
		"--channel", cuzzChannel,
		"--kind", cuzzKind,
		"--content", string(content),
		"--author", author(),
	)
	cmd.Env = os.Environ()
	if cfg.URL != "" {
		cmd.Env = append(cmd.Env, "CUZZ_URL="+cfg.URL)
	}
	if tok := os.Getenv("CUZZ_TOKEN"); tok == "" {
		// cuzz will refuse without a token; leave a clear stderr trace.
		_ = os.Setenv("CUZZ_TOKEN", "")
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cuzz send to %s: %w: %s", channelName, err, strings.TrimSpace(out.String()))
	}
	return nil
}

// NotifyHITL sends a human-in-the-loop request to the ops channel.
func (m *Manager) NotifyHITL(p hitl.Proposal) error {
	summary := fmt.Sprintf("HITL: %s proposal for %s", p.Loop, p.Target)
	cmd := fmt.Sprintf("fleet approve %s %s", m.fleet.Name, p.ID)
	if p.Reason != "" {
		cmd += " --reason \"" + p.Reason + "\""
	}
	data := map[string]interface{}{
		"proposal_id": p.ID,
		"loop":        p.Loop,
		"target":      p.Target,
		"kind":        p.Kind,
		"score":       p.Meta["score"],
		"approve_cmd": cmd,
		"reject_cmd":  fmt.Sprintf("fleet reject %s %s", m.fleet.Name, p.ID),
	}
	return m.Send("ops", "hitl", summary, data)
}

// NotifyEvent sends a loop completion event to the ops channel.
func (m *Manager) NotifyEvent(loopName, status string, events []map[string]interface{}, cost map[string]interface{}) error {
	summary := fmt.Sprintf("loop %s finished with status %s", loopName, status)
	data := map[string]interface{}{
		"loop":   loopName,
		"status": status,
		"events": events,
		"cost":   cost,
	}
	if status != "ok" {
		return m.Send("alerts", "event", summary, data)
	}
	return m.Send("ops", "event", summary, data)
}

// NotifyError sends an error to the alerts channel.
func (m *Manager) NotifyError(loopName string, err error, log string) error {
	summary := fmt.Sprintf("error in loop %s: %v", loopName, err)
	data := map[string]interface{}{
		"loop": loopName,
		"error": err.Error(),
		"log_snippet": truncate(log, 500),
	}
	return m.Send("alerts", "alert", summary, data)
}

func cuzzKindFor(kind string) string {
	switch kind {
	case "alert":
		return "alert"
	case "hitl":
		return "question"
	case "decision":
		return "status"
	default:
		return "message"
	}
}

func author() string {
	if a := os.Getenv("CUZZ_AGENT"); a != "" {
		return a
	}
	return "fleet-cli"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
