package edl

import (
	"encoding/json"
	"fmt"
	"os"
)

// Load reads a Reel JSON timeline from disk. A missing or malformed file is a
// returned error, never a panic. The returned Reel is normalized only minimally
// (Version defaulted to "1" when empty) so callers see exactly what was on disk.
func Load(path string) (Reel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Reel{}, fmt.Errorf("read reel: %w", err)
	}
	var r Reel
	if err := json.Unmarshal(data, &r); err != nil {
		return Reel{}, fmt.Errorf("parse reel JSON: %w", err)
	}
	if r.Version == "" {
		r.Version = "1"
	}
	return r, nil
}

// Save writes a Reel as pretty (2-space indented) JSON, trailing newline. It
// writes 0644 and never modifies any source video — only the timeline file.
func Save(path string, r Reel) error {
	if r.Version == "" {
		r.Version = "1"
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode reel JSON: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write reel: %w", err)
	}
	return nil
}
