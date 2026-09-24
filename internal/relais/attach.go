package relais

import (
	"fmt"
	"time"

	"github.com/javimosch/fleet-cli/internal/hitl"
	"github.com/javimosch/fleet-cli/internal/state"
)

// Attach mints a relais inbox for a queued proposal and records its one-tap
// approve/reject URLs on the queue entry. The inbox token goes to fleet state,
// never the queue. Failure is non-fatal: the proposal stays approvable with
// `fleet approve` / `fleet reject`.
func Attach(client *Client, fleetName string, p hitl.Proposal, q *hitl.Queue, st *state.Store) error {
	if p.ID == "" {
		return nil
	}
	inbox, err := client.NewInbox(fmt.Sprintf("fleet-cli %s %s", fleetName, p.ID))
	if err != nil {
		return err
	}
	nonce, err := NewNonce()
	if err != nil {
		return err
	}
	if err := SetToken(st, p.ID, inbox.Token, inbox.InboxID); err != nil {
		return err
	}
	now := time.Now().UTC()
	pr := &hitl.ProposalRelais{
		InboxID:    inbox.InboxID,
		CatchURL:   inbox.CatchURL,
		Nonce:      nonce,
		ApproveURL: DecisionURL(inbox.CatchURL, "approve", nonce),
		RejectURL:  DecisionURL(inbox.CatchURL, "reject", nonce),
		CreatedAt:  &now,
	}
	if q == nil {
		return nil
	}
	return q.SetRelais(p.ID, pr)
}
