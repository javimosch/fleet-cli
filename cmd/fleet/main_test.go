package main

import (
	"os"
	"path/filepath"
	"testing"
)

// fleetPath must not let an ambient FLEET_DIR silently redirect a bare fleet
// name: inside a loop env `fleet status <other>` used to answer with the
// loop's own fleet. A name that exists under ./fleets/ wins; FLEET_DIR remains
// the fallback for names with no ./fleets/ entry (loop working dirs).
func TestFleetPathNameBeatsFleetDir(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"mine", "other"} {
		dir := filepath.Join(root, "fleets", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "fleet.yml"), []byte("name: "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(cwd) }()
	t.Setenv("FLEET_DIR", filepath.Join(root, "fleets", "mine"))

	if got, want := fleetPath("other"), filepath.Join(".", "fleets", "other"); got != want {
		t.Fatalf("fleetPath(other) = %q, want %q", got, want)
	}
	// A name with no ./fleets/ entry still falls back to FLEET_DIR.
	if got, want := fleetPath("absent"), filepath.Join(root, "fleets", "mine"); got != want {
		t.Fatalf("fleetPath(absent) = %q, want FLEET_DIR %q", got, want)
	}
	// An explicit directory argument still wins over everything.
	dirArg := filepath.Join(root, "fleets", "other")
	if got := fleetPath(dirArg); got != dirArg {
		t.Fatalf("fleetPath(dir) = %q, want %q", got, dirArg)
	}
}
