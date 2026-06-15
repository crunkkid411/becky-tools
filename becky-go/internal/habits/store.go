package habits

// store.go is the persistence half of the habit store: load/save a tiny,
// human-readable habits.json with a STABLE key order (CLAUDE.md determinism
// invariant). The in-memory Store uses a Go map for O(1) lookup, but Go map
// iteration order is randomised — so on disk the habits are written as an ARRAY
// sorted by {scope, field}, never as a JSON object built from map iteration.
// Same records in => byte-identical file out, which the determinism test asserts.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"becky-go/internal/pathx"
)

// diskFile is the on-disk shape. Habits is a sorted slice (NOT the in-memory
// map) precisely so the serialisation is deterministic and diff-friendly.
type diskFile struct {
	Version int     `json:"version"`
	Habits  []Habit `json:"habits"`
}

// Marshal renders the store to deterministic, indented JSON bytes. Habits are
// emitted in Keys() (scope-then-field) order and every habit's Counts map is a
// Go map — encoding/json sorts object keys lexically, so the counts block is
// stable too. This is the single source of truth for what Save writes.
func (s *Store) Marshal() ([]byte, error) {
	df := diskFile{Version: s.version()}
	for _, k := range s.Keys() {
		df.Habits = append(df.Habits, s.Habits[k])
	}
	b, err := json.MarshalIndent(df, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal habits: %w", err)
	}
	return append(b, '\n'), nil
}

// version returns the store's schema version, defaulting a zero-value store to
// the current schema (so a freshly-constructed Store{} still saves v1).
func (s *Store) version() int {
	if s.Version == 0 {
		return SchemaVersion
	}
	return s.Version
}

// Unmarshal rebuilds a Store from habits.json bytes. It tolerates the sorted
// ARRAY form written by Marshal. A malformed body degrades to a wrapped error so
// the caller can report it without crashing.
func Unmarshal(b []byte) (*Store, error) {
	var df diskFile
	if err := json.Unmarshal(b, &df); err != nil {
		return nil, fmt.Errorf("parse habits.json: %w", err)
	}
	s := &Store{Version: df.Version, Habits: map[string]Habit{}}
	if s.Version == 0 {
		s.Version = SchemaVersion
	}
	for _, h := range df.Habits {
		if h.Counts == nil {
			h.Counts = map[string]int{}
		}
		recompute(&h) // re-derive default/evidence/learned so disk edits stay honest
		s.Habits[key(h.Scope, h.Field)] = h
	}
	return s, nil
}

// Load reads the store from path. A MISSING file is not an error — it degrades to
// a fresh empty store (first run learns from zero). A present-but-unreadable or
// corrupt file returns a wrapped error so the caller decides whether to halt.
func Load(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewStore(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pathx.Base(path), err)
	}
	return Unmarshal(b)
}

// Save writes the store to path deterministically, creating the parent directory
// if needed. Writes atomically via a temp file + rename so a crash mid-write can
// never leave a half-written, unparseable habits.json (degrade-never-crash).
func (s *Store) Save(path string) error {
	if path == "" {
		return errors.New("save: empty path")
	}
	b, err := s.Marshal()
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", pathx.Base(tmp), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

// DefaultStorePath returns a sensible per-user location for habits.json. It
// prefers the OS user-config dir (e.g. %AppData% on Windows, ~/.config on Linux)
// and falls back to a CWD-local file if that can't be resolved — always returning
// a usable path so the CLI degrades rather than failing to pick a location.
func DefaultStorePath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "becky", "habits.json")
	}
	return "habits.json"
}
