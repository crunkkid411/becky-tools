package habits

// sources.go closes the loop from tool corrections on disk → the habit Store.
//
// # On-disk corrections-log contract (JSONL)
//
// Each becky tool that can be corrected writes a JSONL file — one JSON object
// per line — alongside its output.  Blank lines are ignored; a malformed line
// is skipped with no error (degrade, never crash).
//
// Canonical field set (all strings):
//
//	{
//	  "tool":  "daw",                    // emitting tool; maps to Context["tool"]
//	  "scope": "kick",                   // → CorrectionRecord.Scope (habit key, part 1)
//	  "field": "gain_db",                // → CorrectionRecord.Field (habit key, part 2)
//	  "auto":  "-3",                     // becky's generated value, serialised as text
//	  "fixed": "-7",                     // Jordan's corrected value
//	  "ts":    "2026-06-15T14:00:00Z"   // RFC 3339 UTC timestamp, informational only
//	}
//
// The legacy aliases accepted by ingest.go (kind→scope, corrected→fixed) are
// NOT supported in JSONL logs — tool authors MUST use the canonical field names
// above. This keeps the JSONL shape unambiguous.
//
// # File glob
//
// LoadCorrectionLogs walks a directory and loads every file whose name ends in
// ".jsonl". Tools may use either of these naming conventions:
//
//	<tool>.jsonl                  e.g. "daw.jsonl"
//	<tool>.corrections.jsonl      e.g. "hum.corrections.jsonl"
//
// Files are processed in lexicographic order of their base names, so the same
// directory always produces byte-identical records regardless of OS ordering.
//
// # Emit one-liner for tool authors (follow-up wiring, NOT done here)
//
// Each tool (hum/vox/daw/canvas) should call AppendCorrectionLog whenever
// Jordan overrides an auto-generated value:
//
//	habits.AppendCorrectionLog(logPath, "daw",    scope, field, auto, fixed)
//	habits.AppendCorrectionLog(logPath, "hum",    scope, field, auto, fixed)
//	habits.AppendCorrectionLog(logPath, "vox",    scope, field, auto, fixed)
//	habits.AppendCorrectionLog(logPath, "canvas", scope, field, auto, fixed)
//
// logPath is conventionally <output-dir>/<tool>.corrections.jsonl.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// jsonlRecord is the on-disk line shape for the JSONL corrections-log format.
// It is flat (no nesting) so the tool side is a trivial json.Marshal call.
// The "ts" field is informational; the learner ignores its value.
type jsonlRecord struct {
	Tool  string `json:"tool"`
	Scope string `json:"scope"`
	Field string `json:"field"`
	Auto  string `json:"auto"`
	Fixed string `json:"fixed"`
	TS    string `json:"ts,omitempty"` // RFC 3339 UTC, informational
}

// LoadCorrectionLog parses one JSONL corrections-log file into a slice of
// CorrectionRecord values ready for Store.ObserveAll.
//
// Blank lines are silently skipped. A malformed JSON line is skipped (degrade,
// never crash); a file that cannot be opened returns a wrapped error.
// The "tool" field populates Context["tool"] so the habit Sources provenance
// slice tracks which tool contributed each observation.
func LoadCorrectionLog(path string) ([]CorrectionRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open correction log %s: %w", filepath.Base(path), err)
	}
	defer f.Close()

	var out []CorrectionRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue // blank lines are valid JSONL separators
		}
		var row jsonlRecord
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			// degrade: malformed line is skipped, not fatal
			continue
		}
		var ctx map[string]string
		if row.Tool != "" {
			ctx = map[string]string{"tool": row.Tool}
		}
		out = append(out, CorrectionRecord{
			Scope:   row.Scope,
			Field:   row.Field,
			Auto:    row.Auto,
			Fixed:   row.Fixed,
			Context: ctx,
		})
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("read correction log %s: %w", filepath.Base(path), err)
	}
	return out, nil
}

// LoadCorrectionLogs walks dir and loads every *.jsonl file in lexicographic
// order of their base names, concatenating their records. The deterministic
// ordering means the same directory always yields byte-identical records.
//
// A missing directory degrades to an empty result and nil error — the tools
// may not have written any logs yet. An unreadable file within the directory
// is reported as a wrapped error but processing continues for remaining files
// (degrade, never crash).
func LoadCorrectionLogs(dir string) ([]CorrectionRecord, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil // no logs yet — not an error
	}
	if err != nil {
		return nil, fmt.Errorf("list corrections dir %s: %w", dir, err)
	}

	// Collect matching filenames, then sort explicitly so the result is
	// deterministic even on platforms that don't guarantee ReadDir ordering.
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // determinism invariant

	var (
		all     []CorrectionRecord
		lastErr error
	)
	for _, name := range names {
		recs, err := LoadCorrectionLog(filepath.Join(dir, name))
		if err != nil {
			lastErr = err // record but continue loading remaining files
		}
		all = append(all, recs...)
	}
	return all, lastErr
}

// AppendCorrectionLog appends one JSONL line to path (creating or appending
// the file). This is the canonical emit helper each tool should call when
// Jordan overrides an auto-generated value.
//
//	tool   — name of the emitting tool ("daw", "hum", "vox", "canvas")
//	scope  — habit bucket, e.g. "kick", "clip-001", "genre"
//	field  — knob name, e.g. "gain_db", "note.midi", "quantize"
//	auto   — becky's generated value, serialised to string by the caller
//	fixed  — Jordan's correction, serialised to string by the caller
//
// The timestamp is filled automatically (UTC RFC 3339). The function is safe
// to call from multiple goroutines only if they write to different paths; for
// a shared path, the caller must serialise writes.
func AppendCorrectionLog(path, tool, scope, field, auto, fixed string) error {
	row := jsonlRecord{
		Tool:  tool,
		Scope: scope,
		Field: field,
		Auto:  auto,
		Fixed: fixed,
		TS:    time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("marshal correction: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open correction log for append %s: %w", filepath.Base(path), err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write correction log %s: %w", filepath.Base(path), err)
	}
	return nil
}
