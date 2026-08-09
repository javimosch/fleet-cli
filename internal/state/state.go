// Package state provides a simple JSON-backed key/value store per fleet.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is a thread-safe JSON file store.
type Store struct {
	path string
	mu   sync.Mutex
	data map[string]interface{}
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
	return s, nil
}

// Get returns a value or nil.
func (s *Store) Get(key string) (interface{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return v, ok
}

// Set stores a value.
func (s *Store) Set(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return s.saveLocked()
}

// Append adds an item to a JSON array at key.
func (s *Store) Append(key string, item interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var arr []interface{}
	if v, ok := s.data[key]; ok {
		if existing, ok := v.([]interface{}); ok {
			arr = existing
		}
	}
	arr = append(arr, item)
	s.data[key] = arr
	return s.saveLocked()
}

// All returns a shallow copy of all data.
func (s *Store) All() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
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

// RunRecord records an execution of a loop.
func (s *Store) RunRecord(loop string, status string, started, finished time.Time, output map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := "runs"
	var runs []interface{}
	if v, ok := s.data[key]; ok {
		runs, _ = v.([]interface{})
	}
	runs = append(runs, map[string]interface{}{
		"loop":     loop,
		"status":   status,
		"started":  started.UTC().Format(time.RFC3339),
		"finished": finished.UTC().Format(time.RFC3339),
		"output":   output,
	})
	s.data[key] = runs
	return s.saveLocked()
}
