// Package hitl implements a proposal queue with approve/reject semantics.
package hitl

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Status values for a proposal.
const (
	Pending   = "pending"
	Approved  = "approved"
	Rejected  = "rejected"
	Dispatched = "dispatched"
	Expired   = "expired"
)

// Proposal is an outbound action awaiting human approval.
type Proposal struct {
	ID         string                 `json:"id"`
	Fleet      string                 `json:"fleet"`
	Loop       string                 `json:"loop"`
	Kind       string                 `json:"kind"`      // e.g. comment, post, dm
	Target     string                 `json:"target"`    // URL or repo/issue
	Body       string                 `json:"body"`
	BodyHash   string                 `json:"body_hash"` // hash at approval time
	Meta       map[string]interface{} `json:"meta"`
	Status     string                 `json:"status"`
	CreatedAt  time.Time              `json:"created_at"`
	ApprovedAt *time.Time             `json:"approved_at,omitempty"`
	Approver   string                 `json:"approver,omitempty"`
	Reason     string                 `json:"reason,omitempty"`
	DispatchedAt *time.Time           `json:"dispatched_at,omitempty"`
}

// Queue stores proposals in a JSONL file.
type Queue struct {
	path string
	mu   sync.Mutex
}

// Open returns a queue at path.
func Open(path string) *Queue {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	return &Queue{path: path}
}

// Add creates a new proposal and returns its ID.
func (q *Queue) Add(p Proposal) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if p.ID == "" {
		p.ID = fmt.Sprintf("p_%s", hash(fmt.Sprintf("%s-%s-%s-%d", p.Fleet, p.Loop, p.Target, time.Now().UnixNano()))[:12])
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	p.Status = Pending
	p.BodyHash = hash(p.Body)
	line, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(q.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		return "", err
	}
	return p.ID, nil
}

// List returns proposals filtered by status. status=="" returns all.
func (q *Queue) List(status string) ([]Proposal, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	data, err := os.ReadFile(q.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var out []Proposal
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var p Proposal
		if err := json.Unmarshal(line, &p); err != nil {
			continue
		}
		if status == "" || p.Status == status {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// UpdateStatus changes a proposal's status and optionally records a reason.
func (q *Queue) UpdateStatus(id, status, reason, approver string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	proposals, err := q.readAllLocked()
	if err != nil {
		return err
	}
	found := false
	now := time.Now().UTC()
	for i := range proposals {
		if proposals[i].ID == id {
			proposals[i].Status = status
			proposals[i].Reason = reason
			if status == Approved {
				proposals[i].ApprovedAt = &now
				proposals[i].Approver = approver
			}
			if status == Dispatched {
				proposals[i].DispatchedAt = &now
			}
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("proposal %s not found", id)
	}
	return q.writeAllLocked(proposals)
}

// Approved returns approved-but-not-dispatched proposals, sorted oldest first.
func (q *Queue) Approved() ([]Proposal, error) {
	all, err := q.List(Approved)
	if err != nil {
		return nil, err
	}
	var out []Proposal
	for _, p := range all {
		if p.DispatchedAt == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

// readAllLocked reads the entire queue.
func (q *Queue) readAllLocked() ([]Proposal, error) {
	data, err := os.ReadFile(q.path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var out []Proposal
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var p Proposal
		if err := json.Unmarshal(line, &p); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// writeAllLocked overwrites the queue file.
func (q *Queue) writeAllLocked(proposals []Proposal) error {
	tmp := q.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	for _, p := range proposals {
		line, err := json.Marshal(p)
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, q.path)
}

// hash returns a SHA-256 hex string.
func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// splitLines splits data into lines.
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
