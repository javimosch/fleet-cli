package hitl

import (
	"path/filepath"
	"testing"
)

func TestBodylessProposalEntersAsDraft(t *testing.T) {
	q := Open(filepath.Join(t.TempDir(), "q.jsonl"))
	id, err := q.Add(Proposal{ID: "p_empty", Fleet: "f", Kind: "cold_email", Target: "a@b.c"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// It must not show up as pending: pending is what relais, the digest and
	// dispatch all read, and this one has nothing a human could approve.
	if got, _ := q.List(Pending); len(got) != 0 {
		t.Fatalf("bodyless proposal is pending: %+v", got)
	}
	drafts, _ := q.Drafts()
	if len(drafts) != 1 || drafts[0].ID != id {
		t.Fatalf("want 1 draft, got %+v", drafts)
	}

	if err := q.SetBody(id, "  "); err == nil {
		t.Fatal("SetBody accepted whitespace as a body")
	}
	if err := q.SetBody(id, "hello there"); err != nil {
		t.Fatalf("set body: %v", err)
	}
	pending, _ := q.List(Pending)
	if len(pending) != 1 || pending[0].Body != "hello there" || pending[0].BodyHash != hash("hello there") {
		t.Fatalf("body not settled: %+v", pending)
	}
	// Once it is out of drafting the copy is frozen -- otherwise a later loop
	// could swap the body out from under an approval.
	if err := q.SetBody(id, "something else"); err == nil {
		t.Fatal("SetBody rewrote a pending proposal")
	}
	_ = q.UpdateStatus(id, Approved, "", "test")
	if err := q.SetBody(id, "sneaky"); err == nil {
		t.Fatal("SetBody rewrote an approved proposal")
	}
}

func TestProposalWithBodyIsPendingImmediately(t *testing.T) {
	q := Open(filepath.Join(t.TempDir(), "q.jsonl"))
	if _, err := q.Add(Proposal{ID: "p_full", Fleet: "f", Body: "copy"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got, _ := q.List(Pending); len(got) != 1 {
		t.Fatalf("proposal with a body should be pending, got %+v", got)
	}
}
