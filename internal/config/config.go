// Package config parses fleet.yml into a typed Fleet graph.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Fleet is the top-level fleet configuration.
type Fleet struct {
	Version  int               `yaml:"version"`
	Name     string            `yaml:"name"`
	Repo     string            `yaml:"repo"`
	Defaults Defaults          `yaml:"defaults"`
	State    State             `yaml:"state"`
	Budgets  Budgets           `yaml:"budgets"`
	HITL     HITL              `yaml:"hitl"`
	Loops    []Loop            `yaml:"loops"`
	Channels map[string]Channel `yaml:"channels"`
}

// Defaults are inherited by loops unless overridden.
type Defaults struct {
	Timeout  string `yaml:"timeout"`
	OnFailure string `yaml:"on_failure"`
	Workdir  string `yaml:"workdir"`
}

// State configures the persistence backend.
type State struct {
	Backend string `yaml:"backend"`
	Path    string `yaml:"path"`
}

// Budgets are global rate and safety limits.
type Budgets struct {
	OutboundPerDay   int    `yaml:"outbound_per_day"`
	OutboundPerHour  int    `yaml:"outbound_per_hour"`
	PerTargetCooldown string `yaml:"per_target_cooldown"`
	QuietHours       string `yaml:"quiet_hours"`
}

// HITL configures human-in-the-loop gating.
type HITL struct {
	Mode       string `yaml:"mode"`
	Channel    string `yaml:"channel"`
	ExpireAfter string `yaml:"expire_after"`
}

// Loop is a node in the fleet graph.
type Loop struct {
	Name             string            `yaml:"name"`
	Command          string            `yaml:"command"`
	Mode             string            `yaml:"mode"`
	Timeout          string            `yaml:"timeout"`
	Schedule         string            `yaml:"schedule"`
	RuntimeMax       string            `yaml:"runtime_max"`
	On               []string          `yaml:"on"`
	Outbound         bool              `yaml:"outbound"`
	RequiresApproval bool              `yaml:"requires_approval"`
	Rate             Rate              `yaml:"rate"`
	Emits            []string          `yaml:"emits"`
	Env              map[string]string `yaml:"env"`
}

// Rate limits a single loop.
type Rate struct {
	MaxPerRun  int `yaml:"max_per_run"`
	MaxPerDay  int `yaml:"max_per_day"`
	MaxPerHour int `yaml:"max_per_hour"`
}

// Channel is an external notification target.
type Channel struct {
	Kind    string `yaml:"kind"`
	Channel string `yaml:"channel"`
	URL     string `yaml:"url"`
}

// Load reads and validates a fleet.yml from path.
func Load(path string) (*Fleet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fleet.yml: %w", err)
	}
	var f Fleet
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse fleet.yml: %w", err)
	}
	if f.Name == "" {
		return nil, fmt.Errorf("fleet.name is required")
	}
	if f.Repo == "" {
		return nil, fmt.Errorf("fleet.repo is required")
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// Validate checks invariants and returns an error for the first violation.
func (f *Fleet) Validate() error {
	names := make(map[string]bool)
	for _, l := range f.Loops {
		if l.Name == "" {
			return fmt.Errorf("loop name is required")
		}
		if names[l.Name] {
			return fmt.Errorf("duplicate loop name: %s", l.Name)
		}
		names[l.Name] = true
		if l.Command == "" {
			return fmt.Errorf("loop %s: command is required", l.Name)
		}
		if l.Mode == "" {
			l.Mode = "oneshot"
		}
		if l.Mode != "oneshot" && l.Mode != "daemon" {
			return fmt.Errorf("loop %s: mode must be oneshot or daemon", l.Name)
		}
		if l.Mode == "daemon" && l.Schedule != "" {
			return fmt.Errorf("loop %s: daemon loops cannot have a schedule", l.Name)
		}
		if l.Mode == "daemon" && l.RuntimeMax == "" {
			return fmt.Errorf("loop %s: daemon loops must set runtime_max", l.Name)
		}
		if l.RuntimeMax != "" {
			if _, err := time.ParseDuration(l.RuntimeMax); err != nil {
				return fmt.Errorf("loop %s: invalid runtime_max %q: %w", l.Name, l.RuntimeMax, err)
			}
		}
		if l.Timeout != "" {
			if _, err := time.ParseDuration(l.Timeout); err != nil {
				return fmt.Errorf("loop %s: invalid timeout %q: %w", l.Name, l.Timeout, err)
			}
		}
		if l.Outbound && !l.RequiresApproval {
			return fmt.Errorf("loop %s: outbound loops must set requires_approval: true", l.Name)
		}
	}
	return nil
}

// GetLoop returns a loop by name or nil.
func (f *Fleet) GetLoop(name string) *Loop {
	for i := range f.Loops {
		if f.Loops[i].Name == name {
			return &f.Loops[i]
		}
	}
	return nil
}

// TimeoutDuration resolves the loop timeout, falling back to fleet defaults.
// Daemon loops fall back to runtime_max so `fleet run` exits before systemd kills them.
func (l *Loop) TimeoutDuration(defaults Defaults) time.Duration {
	s := l.Timeout
	if s == "" && l.Mode == "daemon" {
		s = l.RuntimeMax
	}
	if s == "" {
		s = defaults.Timeout
	}
	if s == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

// RuntimeMaxDuration returns the daemon runtime_max duration, or zero.
func (l *Loop) RuntimeMaxDuration() time.Duration {
	if l.RuntimeMax == "" {
		return 0
	}
	d, err := time.ParseDuration(l.RuntimeMax)
	if err != nil {
		return 0
	}
	return d
}

// NextRun parses a simple schedule string into the next occurrence.
// Supported: "hourly", "daily@HH:MM", "@every Nm" or "@every Nh".
func (l *Loop) NextRun(now time.Time) (time.Time, error) {
	switch l.Schedule {
	case "":
		return now, nil
	case "hourly":
		next := now.Truncate(time.Hour).Add(time.Hour)
		return next, nil
	case "every 15m":
		return now.Truncate(15 * time.Minute).Add(15 * time.Minute), nil
	case "every 1h":
		return now.Truncate(time.Hour).Add(time.Hour), nil
	}
	if len(l.Schedule) > 6 && l.Schedule[:6] == "daily@" {
		t, err := time.Parse("15:04", l.Schedule[6:])
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid daily schedule %q: %w", l.Schedule, err)
		}
		next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		return next, nil
	}
	if len(l.Schedule) > 7 && l.Schedule[:7] == "@every " {
		d, err := time.ParseDuration(l.Schedule[7:])
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid @every schedule %q: %w", l.Schedule, err)
		}
		return now.Add(d), nil
	}
	return time.Time{}, fmt.Errorf("unsupported schedule %q", l.Schedule)
}
