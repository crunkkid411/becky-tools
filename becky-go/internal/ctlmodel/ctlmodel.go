// Package ctlmodel turns a natural-language instruction into a ctledit.BeckyEditBatch
// — the NL→batch half of becky-canvas's select→ask→transform loop. ctledit APPLIES a
// batch to a dawmodel.Arrangement; ctlmodel PRODUCES one from plain English.
//
// Two strategies, in cost order (the becky-wire / becky-drum house pattern):
//
//  1. KeywordProposer — fully deterministic, offline, no model. Handles the common,
//     unambiguous studio phrasings ("set tempo to 140", "mute the bass", "solo the
//     drums", "pan the lead left", "make the bass louder", "transpose up an octave").
//     It is GROUNDED in the live arrangement: track references resolve against the
//     real track IDs, and relative gain moves read the track's current strip gain.
//
//  2. ModelProposer — wraps a small local llama.cpp instruct model, GBNF-constrained
//     (Grammar()) to emit only a valid BeckyEditBatch JSON, for the richer phrasings a
//     keyword parser can't reach ("add a four-on-the-floor kick", "duck the synths
//     under the vocal"). On ANY failure (binary/model absent, bad JSON, zero edits) it
//     falls back to the keyword proposer. Degrade-never-crash.
//
// PickProposer() returns the model proposer when a binary + model resolve on disk, else
// the keyword proposer. The model EXEC (execRunner.run) is the GPU/model boundary and
// ships as a DOCUMENTED STUB for the local agent to wire — mirror
// internal/canvas.execModelRunner (os/exec llama-completion with --temp 0 --seed 42 and
// --grammar-file pointed at WriteGrammarFile's output). Until it is wired, every request
// flows through the deterministic keyword path, so the seam works offline today.
package ctlmodel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/ctledit"
	"becky-go/internal/dawmodel"
)

// Proposer turns an instruction + the current session into a proposed edit batch.
// It never errors and never panics: when it cannot turn the instruction into any
// edit it returns a batch whose Edits list is empty and whose Summary explains why.
// The caller (the canvas agent box) shows Summary and applies whatever edits exist
// through ctledit.Apply — nothing mutates until the human approves in the overlay.
type Proposer interface {
	Propose(instruction string, arr *dawmodel.Arrangement) ctledit.BeckyEditBatch
}

// ─── environment + defaults (mirrors internal/canvas.model_transformer) ────────

const (
	// EnvCtlBin overrides the llama.cpp one-shot completion binary path.
	EnvCtlBin = "BECKY_CTL_BIN"
	// EnvCtlModel overrides the small-instruct GGUF path.
	EnvCtlModel = "BECKY_CTL_MODEL"

	// DefaultCtlBin is llama.cpp's one-shot completion tool on Jordan's PC.
	// (llama-cli became an interactive TUI; llama-completion is the one-shot tool —
	// see internal/canvas.DefaultTransformBin for the full note.)
	DefaultCtlBin = `C:/llama.cpp/build/bin/llama-completion.exe`
	// DefaultCtlModel is the becky-owned Qwen3-4B-Instruct GGUF already on the PC.
	DefaultCtlModel = `X:/AI-2/becky-tools/models/Qwen3-4B-Instruct-2507-Q4_K_M.gguf`
)

// ─── KeywordProposer (the deterministic core) ──────────────────────────────────

// KeywordProposer implements Proposer with the offline ParseKeyword.
type KeywordProposer struct{}

// Propose runs the deterministic keyword parser.
func (KeywordProposer) Propose(instruction string, arr *dawmodel.Arrangement) ctledit.BeckyEditBatch {
	return ParseKeyword(instruction, arr)
}

// Keyword returns the deterministic keyword-only proposer.
func Keyword() Proposer { return KeywordProposer{} }

// ─── ModelProposer (model first, keyword fallback) ─────────────────────────────

// runner abstracts the llama.cpp exec call so tests never spawn a real binary.
// run receives the resolved binary + model paths, the assembled prompt, and the
// GBNF grammar text; it returns the model's stdout.
type runner interface {
	run(bin, model, prompt, grammar string) (stdout string, err error)
}

// ModelProposer asks a GBNF-constrained local model first and falls back to the
// keyword proposer when the model is unavailable, errors, or returns nothing usable.
type ModelProposer struct {
	bin, model string
	fallback   Proposer
	// exec is the model-runner seam; nil means "no model wired", so Propose is
	// keyword-only.
	exec runner
}

// Propose tries the model, then degrades to the fallback proposer.
func (m ModelProposer) Propose(instruction string, arr *dawmodel.Arrangement) ctledit.BeckyEditBatch {
	fb := m.fallback.Propose(instruction, arr)
	if m.exec == nil {
		return fb // model not wired → keyword result
	}
	out, err := m.exec.run(m.bin, m.model, BuildPrompt(instruction, Snapshot(arr)), Grammar())
	if err != nil {
		return fb // exec failed → degrade
	}
	batch, derr := DecodeBatch(out)
	if derr != nil || len(batch.Edits) == 0 {
		return fb // unusable model output → degrade
	}
	return batch
}

// ─── PickProposer (single call-site for cmd/canvas) ────────────────────────────

// PickProposer returns a ModelProposer when both the llama.cpp binary and the model
// GGUF resolve on disk, otherwise the keyword-only proposer. Set BECKY_CTL_BIN and
// BECKY_CTL_MODEL to point at them. Note the shipped execRunner.run is a STUB
// (returns errModelStub), so even the ModelProposer degrades to keywords until the
// local agent wires the exec — keeping the seam working offline today.
func PickProposer() Proposer {
	bin, model := resolvePaths()
	if fileExists(bin) && fileExists(model) {
		return ModelProposer{bin: bin, model: model, fallback: Keyword(), exec: execRunner{}}
	}
	return Keyword()
}

func resolvePaths() (bin, model string) {
	bin = firstNonEmpty(os.Getenv(EnvCtlBin), DefaultCtlBin)
	model = firstNonEmpty(os.Getenv(EnvCtlModel), DefaultCtlModel)
	return bin, model
}

// ─── execRunner: the DOCUMENTED model boundary (local-agent task) ──────────────

// execRunner is the production model runner. Its run method is a STUB: the local
// Windows agent fills it in exactly like internal/canvas.execModelRunner —
//
//	cmd := exec.Command(bin,
//	    "-m", model, "-p", prompt,
//	    "--grammar-file", <path from WriteGrammarFile>,
//	    "--temp", "0", "--seed", "42", "-n", "512", "--no-display-prompt")
//	out, err := cmd.Output() // return string(out), err
//
type execRunner struct{}

func (execRunner) run(bin, model, prompt, grammar string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "ctlmodel-*")
	if err != nil {
		return "", fmt.Errorf("ctlmodel: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	gbnfPath := filepath.Join(tmpDir, "becky-edit.gbnf")
	if err := os.WriteFile(gbnfPath, []byte(grammar), 0o644); err != nil {
		return "", fmt.Errorf("ctlmodel: write grammar: %w", err)
	}

	cmd := exec.Command(bin,
		"-m", model,
		"-p", prompt,
		"--grammar-file", gbnfPath,
		"--temp", "0",
		"--seed", "42",
		"-n", "512",
		"--no-display-prompt")
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := errBuf.String()
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return outBuf.String(), fmt.Errorf("ctlmodel: llama exec: %w: %s", err, strings.TrimSpace(msg))
	}
	return outBuf.String(), nil
}

// ─── small helpers ─────────────────────────────────────────────────────────────

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
