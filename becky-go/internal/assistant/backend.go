package assistant

import "context"

// backend.go defines the Backend seam (R-AI §4.1). One Backend = one model tier's
// engine. Tier 0 has no Backend (it's pure Go in router.go). Every Backend hides
// its model/network dependency behind Available(), so the router can route around
// an absent engine and `go test` stays green offline with fake backends.
//
// Available() returns an error (the plain reason a backend is unusable), per the
// primary design source R-AI §4.1 — richer than the one-line `Available() bool`
// sketch in SPEC §8, and load-bearing for the "surface the plain reason" degrade
// behaviour both docs require. IsAvailable is the bool convenience wrapper.

// Request is what a Backend.Complete receives. System is becky's forensic + action
// rules; User is the per-turn payload (timeline state + candidate window + the
// utterance). JSONSchema, when set, asks a capable backend (claude CLI / API) for
// schema-validated structured output. Tier lets a frontier backend pick mid vs
// deep model alias.
type Request struct {
	System     string
	User       string
	JSONSchema string
	MaxTokens  int
	Tier       Tier

	// Agentic, when true, asks a capable backend (the claude CLI) to run as a
	// READ-ONLY file investigator: it may use Read/Glob/Grep/LS over AllowDirs and
	// take up to MaxTurns steps. This is what lets becky navigate an Obsidian vault +
	// transcripts and CITE the exact evidence video/timestamp, the way Claude Code
	// does. Backends that can't do agentic file access ignore these (degrade).
	Agentic   bool
	AllowDirs []string // absolute dirs the investigator may read (case folder, vault, …)
	MaxTurns  int      // tool-use turn cap for the agentic run (0 -> backend default)
}

// Backend is one model tier's engine.
type Backend interface {
	// Name identifies the backend ("local" | "claude-cli" | "anthropic-api").
	Name() string
	// Available reports nil if the backend is usable right now (binary/key/server
	// present). A non-nil error is the plain reason it is not — surfaced to the
	// user and used by the router to degrade.
	Available() error
	// Complete sends system+user and returns the model's raw text (which the
	// router parses into actions). Honors the request's context deadline.
	Complete(ctx context.Context, req Request) (string, error)
}

// IsAvailable is a bool convenience over Backend.Available(). A nil Backend is
// treated as unavailable.
func IsAvailable(b Backend) bool {
	return b != nil && b.Available() == nil
}
