package relais

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/javimosch/fleet-cli/internal/hitl"
	"github.com/javimosch/fleet-cli/internal/state"
)

// The poll has to fit inside the loop timeout that will kill it, and it never
// did. One relais call can take up to the client's 30s timeout, and the loops
// poll every pending proposal: at 17 proposals that is 510s against a 300s
// timeout, at 32 it is 960s. Whenever relais got slow the loop was
// arithmetically unable to finish, so it was killed mid-run and reported as a
// failure -- which is what three fleets were flapping on.
//
// So the poll gets a wall-clock budget and spends it deliberately: it stops
// before the budget runs out rather than being killed, and says how many
// proposals it did not get to.
const (
	// DefaultBudget is comfortably inside the shortest loop timeout in use (300s).
	DefaultBudget = 120 * time.Second
	minPerRequest = 5 * time.Second
	maxPerRequest = 30 * time.Second
	// maxConsecutiveErrors stops the walk when relais is simply down. Seventeen
	// identical timeouts cost the same as one and tell you nothing more.
	maxConsecutiveErrors = 3
)

// Stats reports what one poll actually managed to do.
type Stats struct {
	Pending   int    `json:"pending"`
	Polled    int    `json:"polled"`
	Unpolled  int    `json:"unpolled"`
	StoppedBy string `json:"stopped_by,omitempty"`
}

// Budget is the wall-clock allowance for a poll, from FLEET_RELAIS_BUDGET
// (seconds) or DefaultBudget.
func Budget() time.Duration {
	if v := os.Getenv("FLEET_RELAIS_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return DefaultBudget
}

// PollResult records one decision applied by PollQueue.
type PollResult struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	By       string `json:"by"`
	Error    string `json:"error,omitempty"`
}

// PollQueue checks pending proposals with relais inboxes and applies decisions,
// within a wall-clock budget.
func PollQueue(client *Client, q *hitl.Queue, st *state.Store) ([]PollResult, Stats, error) {
	proposals, err := q.List(hitl.Pending)
	if err != nil {
		return nil, Stats{}, err
	}

	// Only proposals that can actually be polled count against the budget.
	var pollable []hitl.Proposal
	for _, p := range proposals {
		if p.Relais == nil || p.Relais.InboxID == "" || p.Relais.Nonce == "" {
			continue
		}
		if _, ok := GetToken(st, p.ID); !ok {
			continue
		}
		pollable = append(pollable, p)
	}

	stats := Stats{Pending: len(pollable)}
	deadline := time.Now().Add(Budget())
	consecutiveErrors := 0

	var out []PollResult
	for idx, p := range pollable {
		remaining := time.Until(deadline)
		if remaining <= minPerRequest {
			stats.Unpolled = len(pollable) - idx
			stats.StoppedBy = "budget"
			break
		}
		if consecutiveErrors >= maxConsecutiveErrors {
			stats.Unpolled = len(pollable) - idx
			stats.StoppedBy = "relais_unreachable"
			break
		}

		// Share what is left between the proposals still to check, so one slow
		// call cannot eat the allowance for all the others.
		perRequest := remaining / time.Duration(len(pollable)-idx)
		if perRequest < minPerRequest {
			perRequest = minPerRequest
		}
		if perRequest > maxPerRequest {
			perRequest = maxPerRequest
		}
		client.HTTP.Timeout = perRequest

		token, _ := GetToken(st, p.ID)

		msgs, err := client.Messages(token)
		if err != nil {
			consecutiveErrors++
			out = append(out, PollResult{ID: p.ID, Error: fmt.Sprintf("relais messages: %v", err)})
			continue
		}
		consecutiveErrors = 0
		stats.Polled++

		decided := false
		var decision, by string
		for _, m := range msgs {
			if !strings.Contains(m.Path, "n="+p.Relais.Nonce) {
				continue
			}
			if strings.Contains(m.Path, "d=approve") {
				decision = hitl.Approved
			} else if strings.Contains(m.Path, "d=reject") {
				decision = hitl.Rejected
			} else {
				continue
			}
			by = "relais:" + m.IP
			decided = true
			break
		}

		if !decided {
			continue
		}

		reason := "approved via relais"
		if decision == hitl.Rejected {
			reason = "rejected via relais"
		}

		if err := q.UpdateStatus(p.ID, decision, reason, by); err != nil {
			out = append(out, PollResult{ID: p.ID, Decision: decision, Error: fmt.Sprintf("update status: %v", err)})
			continue
		}

		_ = client.DeleteMessages(token)
		out = append(out, PollResult{ID: p.ID, Decision: decision, By: by})
	}

	return out, stats, nil
}
