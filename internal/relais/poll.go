package relais

import (
	"fmt"
	"strings"

	"github.com/javimosch/fleet-cli/internal/hitl"
	"github.com/javimosch/fleet-cli/internal/state"
)

// PollResult records one decision applied by PollQueue.
type PollResult struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	By       string `json:"by"`
	Error    string `json:"error,omitempty"`
}

// PollQueue checks all pending proposals with relais inboxes and applies decisions.
func PollQueue(client *Client, q *hitl.Queue, st *state.Store) ([]PollResult, error) {
	proposals, err := q.List(hitl.Pending)
	if err != nil {
		return nil, err
	}

	var out []PollResult
	for _, p := range proposals {
		if p.Relais == nil || p.Relais.InboxID == "" || p.Relais.Nonce == "" {
			continue
		}

		token, ok := GetToken(st, p.ID)
		if !ok {
			continue
		}

		msgs, err := client.Messages(token)
		if err != nil {
			out = append(out, PollResult{ID: p.ID, Error: fmt.Sprintf("relais messages: %v", err)})
			continue
		}

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

	return out, nil
}
