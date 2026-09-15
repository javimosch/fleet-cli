package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunRecordDoesNotGrowTheStateFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.json")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("real_state", "kept"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)

	big := map[string]interface{}{"output": make([]interface{}, 0)}
	for i := 0; i < 200; i++ {
		if err := s.RunRecord("loop", "ok", time.Now(), time.Now(), big); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := os.Stat(p)
	// The whole point: run history must not enlarge the file that every Set
	// rewrites.
	if after.Size() != before.Size() {
		t.Fatalf("state file grew with run history: %d -> %d", before.Size(), after.Size())
	}
	if n := s.RunCount(); n != 200 {
		t.Fatalf("want 200 runs, got %d", n)
	}
	if _, ok := s.Get("real_state"); !ok {
		t.Fatal("state lost")
	}
}

func TestLegacyRunsAreMigratedOutOnOpen(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.json")
	legacy := map[string]interface{}{
		"keep_me": "yes",
		"runs": []interface{}{
			map[string]interface{}{"loop": "a", "status": "ok"},
			map[string]interface{}{"loop": "b", "status": "error"},
		},
	}
	b, _ := json.Marshal(legacy)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("runs"); ok {
		t.Fatal("runs still in state after migration")
	}
	if v, ok := s.Get("keep_me"); !ok || v != "yes" {
		t.Fatal("migration dropped real state")
	}
	runs, err := s.Runs(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0]["loop"] != "a" || runs[1]["loop"] != "b" {
		t.Fatalf("runs not migrated in order: %+v", runs)
	}
	// Re-opening must not duplicate them.
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if n := s2.RunCount(); n != 2 {
		t.Fatalf("reopen duplicated history: %d", n)
	}
}

func TestRunsAreTrimmedToRetention(t *testing.T) {
	t.Setenv("FLEET_RUN_RETENTION", "10")
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "f.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Each record carries a big payload so the log crosses runsCompactBytes.
	pad := make([]byte, 40<<10)
	for i := range pad {
		pad[i] = 'x'
	}
	for i := 0; i < 80; i++ {
		if err := s.RunRecord("loop", "ok", time.Now(), time.Now(),
			map[string]interface{}{"i": i, "pad": string(pad)}); err != nil {
			t.Fatal(err)
		}
	}
	// The contract is a SIZE ceiling, not a count: the log is trimmed back to
	// retention whenever it crosses runsCompactBytes, so between compactions it
	// sits somewhere between the two. What must hold is that it stays bounded
	// and never grows without limit.
	fi, err := os.Stat(s.RunsPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > runsCompactBytes+(64<<10) {
		t.Fatalf("log not bounded: %d bytes after 80 x 40KB records", fi.Size())
	}
	runs, _ := s.Runs(0)
	if len(runs) == 0 {
		t.Fatal("retention deleted everything")
	}
	// The survivors must be the NEWEST ones.
	last := runs[len(runs)-1]["output"].(map[string]interface{})
	if int(last["i"].(float64)) != 79 {
		t.Fatalf("kept the wrong end of the log: last i=%v", last["i"])
	}
}
