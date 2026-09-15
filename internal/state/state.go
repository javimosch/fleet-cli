// Package state provides a simple JSON-backed key/value store per fleet.
package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Run history is append-only and nothing operational reads it, but it used to
// live in the same map as the state itself -- so every Set, Append and
// RunRecord re-marshalled and rewrote the whole history to disk. On the busiest
// fleet that meant a 4.2 MB file whose actual state was 10 KB, rewritten ~420
// times a day; across the estate, ~8.7 GB/day of writes to keep ~9 MB of state.
// It now goes to its own JSONL file, one appended line per run.
// The log is bounded by SIZE, not by count: counting lines on every append
// would mean reading the file every run, which is the cost this change exists
// to remove. So runsCompactBytes is the real ceiling, and DefaultRunRetention
// is the floor it trims back to -- between compactions the log holds somewhere
// between the two. A typical run record is ~180 bytes, so 1000 of them is well
// under the ceiling; the ceiling only binds on fleets with large loop output.
const (
	// DefaultRunRetention is how many runs survive a compaction.
	DefaultRunRetention = 1000
	// runsCompactBytes is the size at which the log is trimmed. A stat on
	// append is cheap; the rewrite is rare and amortised.
	runsCompactBytes = 2 << 20
)

// Store is a JSON file store, safe across goroutines AND across processes.
//
// The mutex alone was not enough: every loop run is a separate `fleet run`
// process that loaded the whole map at start and wrote the whole map back at
// the end, so two processes touching the same fleet was a read-modify-write
// with no arbitration and the last writer silently won. That is not theory --
// during the run-history migration a repo-activity loop still running the old
// binary held its pre-migration map in memory and, on exit, wrote it straight
// back over the migrated file.
//
// Every mutating operation now takes an exclusive flock, re-reads the file
// underneath it, applies just its own change, and writes. So a stale process
// can no longer clobber a key it never touched. The lock is a sibling .lock
// file rather than the state file itself, because saveLocked swaps the state
// file by rename -- locking the inode we are about to replace would protect
// nothing.
type Store struct {
	path string
	mu   sync.Mutex
	data map[string]interface{}
}

// withFileLock runs fn while holding an flock on the store's lock file.
// exclusive=false takes a shared lock, for readers that want a torn-free view.
func (s *Store) withFileLock(exclusive bool, fn func() error) error {
	lockPath := s.path + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		// A store on a filesystem that cannot hold the lock file is still a
		// working store; refusing to write at all would be worse than the race.
		return fn()
	}
	defer f.Close()
	how := syscall.LOCK_EX
	if !exclusive {
		how = syscall.LOCK_SH
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		return fn()
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// reloadLocked re-reads the file into memory. Callers hold both s.mu and the
// file lock, so what it reads is what they are about to modify.
func (s *Store) reloadLocked() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.data = make(map[string]interface{})
			return nil
		}
		return err
	}
	fresh := make(map[string]interface{})
	if err := json.Unmarshal(data, &fresh); err != nil {
		return fmt.Errorf("corrupt state file %s: %w", s.path, err)
	}
	s.data = fresh
	return nil
}

// mutate is the one path by which state changes: lock, re-read, apply, save.
func (s *Store) mutate(apply func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.withFileLock(true, func() error {
		if err := s.reloadLocked(); err != nil {
			return err
		}
		apply()
		return s.saveLocked()
	})
}

// Update applies fn to the current value at key and stores the result, all
// under one lock. Use it wherever a Get would otherwise be followed by a Set:
// read and write have to be the same critical section or two processes can
// each read the same value and one of them loses. The budget log is exactly
// that shape, and losing a write there means an outbound cap that undercounts.
func (s *Store) Update(key string, fn func(current interface{}) interface{}) error {
	return s.mutate(func() {
		s.data[key] = fn(s.data[key])
	})
}

// Open loads or creates a store at path.
func Open(path string) (*Store, error) {
	s := &Store{path: path, data: make(map[string]interface{})}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &s.data); err != nil {
			return nil, fmt.Errorf("corrupt state file %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := s.withFileLock(true, func() error {
		// Re-read under the lock: another process may have migrated already
		// between our read above and acquiring it.
		if err := s.reloadLocked(); err != nil {
			return err
		}
		return s.migrateRunsLocked()
	}); err != nil {
		return nil, err
	}
	return s, nil
}

// migrateRunsLocked drains a legacy in-state `runs` array into the JSONL log.
//
// Order matters: the log is written and renamed into place BEFORE the key is
// dropped from the state file. Crash in that window and the next open appends
// the same runs twice -- duplicated history, which is cosmetic. The other order
// would lose it. Both writes are atomic renames, so the window is microseconds.
func (s *Store) migrateRunsLocked() error {
	v, ok := s.data["runs"]
	if !ok {
		return nil
	}
	legacy, _ := v.([]interface{})

	existing, err := readRunLines(s.RunsPath())
	if err != nil {
		return err
	}
	for _, r := range legacy {
		line, err := json.Marshal(r)
		if err != nil {
			continue
		}
		existing = append(existing, string(line))
	}
	if keep := runRetention(); keep > 0 && len(existing) > keep {
		existing = existing[len(existing)-keep:]
	}
	if len(existing) > 0 {
		tmp := s.RunsPath() + ".tmp"
		if err := os.WriteFile(tmp, []byte(strings.Join(existing, "\n")+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, s.RunsPath()); err != nil {
			return err
		}
	}
	delete(s.data, "runs")
	return s.saveLocked()
}

// Get returns a value or nil, reading through to the file so a long-running
// process does not answer from a snapshot another process has moved on from.
func (s *Store) Get(key string) (interface{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.withFileLock(false, func() error { return s.reloadLocked() })
	v, ok := s.data[key]
	return v, ok
}

// Set stores a value.
func (s *Store) Set(key string, value interface{}) error {
	return s.mutate(func() { s.data[key] = value })
}

// Append adds an item to a JSON array at key.
func (s *Store) Append(key string, item interface{}) error {
	return s.mutate(func() {
		var arr []interface{}
		if v, ok := s.data[key]; ok {
			if existing, ok := v.([]interface{}); ok {
				arr = existing
			}
		}
		s.data[key] = append(arr, item)
	})
}

// All returns a shallow copy of all data.
func (s *Store) All() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.withFileLock(false, func() error { return s.reloadLocked() })
	out := make(map[string]interface{}, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

// saveLocked writes data to disk while holding the lock.
func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// RunsPath is the append-only log of loop executions for this store.
func (s *Store) RunsPath() string {
	return strings.TrimSuffix(s.path, ".json") + "-runs.jsonl"
}

// RunRecord records an execution of a loop by appending one line.
func (s *Store) RunRecord(loop string, status string, started, finished time.Time, output map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	line, err := json.Marshal(map[string]interface{}{
		"loop":     loop,
		"status":   status,
		"started":  started.UTC().Format(time.RFC3339),
		"finished": finished.UTC().Format(time.RFC3339),
		"output":   output,
	})
	if err != nil {
		return err
	}
	path := s.RunsPath()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > runsCompactBytes {
		// The append above is atomic on its own (one small O_APPEND write), but
		// a compaction rewrites the whole log, so two of them at once would drop
		// records.
		return s.withFileLock(true, func() error { return compactRuns(path, runRetention()) })
	}
	return nil
}

// runRetention is how many runs to keep. 0 disables trimming entirely, for
// anyone who would rather the log grow than lose a run.
func runRetention() int {
	if v := os.Getenv("FLEET_RUN_RETENTION"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return DefaultRunRetention
}

// Runs returns the most recent runs, newest last. limit <= 0 returns all.
func (s *Store) Runs(limit int) ([]map[string]interface{}, error) {
	lines, err := readRunLines(s.RunsPath())
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	out := make([]map[string]interface{}, 0, len(lines))
	for _, l := range lines {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(l), &m); err == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// RunCount returns how many runs the log currently holds.
func (s *Store) RunCount() int {
	lines, err := readRunLines(s.RunsPath())
	if err != nil {
		return 0
	}
	return len(lines)
}

// readRunLines reads the log a line at a time. A run record can be large (it
// embeds the loop's whole output), so the scanner gets a generous buffer --
// the default 64 KB would silently drop exactly the noisiest runs.
func readRunLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	var out []string
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

// compactRuns trims the log to the last keep lines.
func compactRuns(path string, keep int) error {
	if keep <= 0 {
		return nil
	}
	lines, err := readRunLines(path)
	if err != nil || len(lines) <= keep {
		return err
	}
	lines = lines[len(lines)-keep:]
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
