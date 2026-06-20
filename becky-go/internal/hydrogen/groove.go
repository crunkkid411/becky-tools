package hydrogen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"becky-go/internal/samplelib"
)

// groove.go ties the pieces together for becky-groove: pick REAL drum samples from a
// scanned sample library, assemble a Hydrogen Kit, and (offline) export a beat to audio
// via Hydrogen's CLI. Pure orchestration over song.go/drumkit.go + samplelib; the only
// non-determinism is the user's library contents, which is why sample SELECTION is
// itself deterministic (first match by sorted path).

// General-MIDI percussion notes for the standard drum voices. These are the MIDI
// notes Hydrogen's GMRockKit assigns, so a becky kit triggers identically over OSC.
const (
	MIDIKick    = 36
	MIDISnare   = 38
	MIDIHatClsd = 42
	MIDIHatOpen = 46
	MIDIClap    = 39
	MIDITomLow  = 41
	MIDITomMid  = 45
	MIDITomHigh = 48
	MIDICrash   = 49
	MIDIRide    = 51
)

// BeatVoice is one logical drum voice the groove builder fills from the library.
type BeatVoice struct {
	Name     string // instrument display name (e.g. "Kick")
	Role     string // samplelib role to search for (samplelib.RoleKick, ...)
	MidiNote int    // trigger note
	// NameHint, when set, prefers samples whose name contains this token (case-
	// insensitive) over a bare role match — e.g. "808" to pull an 808 kick.
	NameHint string
}

// DefaultBeatVoices is the kick/snare/hat trio becky-groove builds by default.
var DefaultBeatVoices = []BeatVoice{
	{Name: "Kick", Role: samplelib.RoleKick, MidiNote: MIDIKick},
	{Name: "Snare", Role: samplelib.RoleSnare, MidiNote: MIDISnare},
	{Name: "Hat", Role: samplelib.RoleHat, MidiNote: MIDIHatClsd},
}

// KitFromLibrary assembles a Kit by choosing one real sample per voice from idx. Each
// chosen sample is referenced by ABSOLUTE path (Hydrogen loads absolute layer paths
// fine), so the kit needs no file copying. A voice with no matching sample is SKIPPED
// (degrade-never-crash) and reported in `missing`. The instrument IDs are assigned in
// voice order (0,1,2,...). Selection is deterministic: among role matches, the
// lexicographically-first path wins (NameHint matches are preferred).
func KitFromLibrary(name string, idx *samplelib.Index, voices []BeatVoice) (Kit, []string) {
	kit := Kit{Name: name, Author: "becky", License: "Unknown license"}
	var missing []string
	id := 0
	for _, v := range voices {
		s, ok := pickSample(idx, v.Role, v.NameHint)
		if !ok {
			missing = append(missing, v.Name)
			continue
		}
		inst := NewInstrument(id, v.Name, v.MidiNote, s.Path)
		kit.Instruments = append(kit.Instruments, inst)
		id++
	}
	return kit, missing
}

// pickSample returns the best deterministic sample for a role. Preference order:
//  1. role match whose name contains hint (if hint set), lexicographically first
//  2. any role match, lexicographically first
//  3. (role unknown but) name contains the role word, lexicographically first
//
// All candidate slices are sorted by path so the choice is reproducible.
func pickSample(idx *samplelib.Index, role, hint string) (samplelib.Sample, bool) {
	if idx == nil {
		return samplelib.Sample{}, false
	}
	byRole := idx.ByRole(role)
	sort.Slice(byRole, func(i, j int) bool { return byRole[i].Path < byRole[j].Path })

	if hint != "" {
		h := strings.ToLower(hint)
		for _, s := range byRole {
			if strings.Contains(strings.ToLower(s.Name), h) {
				return s, true
			}
		}
	}
	if len(byRole) > 0 {
		return byRole[0], true
	}

	// Fall back to a name search for the role word (e.g. role guess was unknown but the
	// file is literally named "kick"). Keeps becky useful on messy libraries.
	hits := idx.Search(role)
	sort.Slice(hits, func(i, j int) bool { return hits[i].Path < hits[j].Path })
	for _, s := range hits {
		// Prefer one-shots / short hits as drum sources, not long loops, when we have
		// duration info; unknown duration is allowed (don't over-filter messy libs).
		if s.Kind == samplelib.KindLoop {
			continue
		}
		return s, true
	}
	return samplelib.Sample{}, false
}

// ---------------------------------------------------------------------------
// Audio export via Hydrogen's CLI
// ---------------------------------------------------------------------------

// ExportOptions configures ExportSong.
type ExportOptions struct {
	// CLIPath is the Hydrogen CLI exporter. If empty, FindHydrogenCLI() is used.
	CLIPath string
	// Drumkit, when set, is passed via -k so Hydrogen loads it at startup (use this
	// when the song references a kit by name rather than self-contained absolute paths).
	Drumkit string
	// Timeout bounds the export. 0 -> 90s.
	Timeout time.Duration
}

// ExportSong renders songPath to a WAV at outPath using Hydrogen's CLI
// (`h2cli -s <song> -o <out.wav>`). It works around the Windows GUI-subsystem quirk
// (the binary detaches from the parent console, so stdout/exit are unreliable) by
// POLLING for the output file to appear and stabilize. Returns an error if no audio
// file is produced within the timeout.
//
// degrade-never-crash: a missing CLI returns a typed error (ErrCLINotFound), not a panic.
func ExportSong(songPath, outPath string, opts ExportOptions) error {
	cli := opts.CLIPath
	if cli == "" {
		found, ok := FindHydrogenCLI()
		if !ok {
			return ErrCLINotFound
		}
		cli = found
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}

	// Remove any stale output so polling detects a fresh write.
	_ = os.Remove(outPath)

	args := []string{"-s", songPath, "-o", outPath}
	if opts.Drumkit != "" {
		args = append(args, "-k", opts.Drumkit)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, args...)
	// RunAsInvoker stops the Hydrogen installer/runtime from triggering UAC on Windows,
	// and is harmless elsewhere.
	if runtime.GOOS == "windows" {
		cmd.Env = append(os.Environ(), "__COMPAT_LAYER=RunAsInvoker")
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("hydrogen: start CLI %q: %w", cli, err)
	}

	// Wait for the process and the file concurrently. The process usually exits 0 once
	// the file is written, but on Windows the detach can make Wait() return early or
	// late; the file is the source of truth.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	stableFor := 600 * time.Millisecond
	deadline := time.Now().Add(timeout)
	var lastSize int64 = -1
	var stableSince time.Time

	for {
		if fi, err := os.Stat(outPath); err == nil && fi.Size() > 0 {
			if fi.Size() == lastSize {
				if stableSince.IsZero() {
					stableSince = time.Now()
				} else if time.Since(stableSince) >= stableFor {
					// File written and not growing — success. Reap the process.
					_ = cmd.Process.Kill()
					<-done
					return nil
				}
			} else {
				lastSize = fi.Size()
				stableSince = time.Time{}
			}
		}

		select {
		case werr := <-done:
			// Process exited. Give the file one last check.
			if fi, err := os.Stat(outPath); err == nil && fi.Size() > 0 {
				return nil
			}
			if werr != nil {
				return fmt.Errorf("hydrogen: CLI exited without producing %q: %w", outPath, werr)
			}
			return fmt.Errorf("hydrogen: CLI exited without producing %q", outPath)
		case <-time.After(150 * time.Millisecond):
		}

		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			<-done
			if fi, err := os.Stat(outPath); err == nil && fi.Size() > 0 {
				return nil
			}
			return fmt.Errorf("hydrogen: CLI timed out after %s without producing %q", timeout, outPath)
		}
	}
}

// ErrCLINotFound is returned when no Hydrogen CLI exporter can be located.
var ErrCLINotFound = errors.New("hydrogen: CLI (h2cli/hydrogen) not found; set BECKY_HYDROGEN_CLI or pass --hydrogen-cli")

// FindHydrogenCLI locates the Hydrogen CLI exporter. Order:
//  1. $BECKY_HYDROGEN_CLI (explicit override)
//  2. h2cli / hydrogen on $PATH
//  3. well-known install dirs (Program Files + per-user LocalAppData on Windows)
//
// h2cli is preferred (it is the dedicated headless exporter); hydrogen.exe is a fallback.
func FindHydrogenCLI() (string, bool) {
	if env := strings.TrimSpace(os.Getenv("BECKY_HYDROGEN_CLI")); env != "" {
		if fileExists(env) {
			return env, true
		}
	}
	// PATH lookups.
	for _, name := range []string{"h2cli", "hydrogen"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, true
		}
	}
	// Well-known Windows locations.
	if runtime.GOOS == "windows" {
		var roots []string
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			roots = append(roots, filepath.Join(la, "Hydrogen"))
		}
		if pf := os.Getenv("ProgramFiles"); pf != "" {
			roots = append(roots, filepath.Join(pf, "Hydrogen"))
		}
		if pf := os.Getenv("ProgramFiles(x86)"); pf != "" {
			roots = append(roots, filepath.Join(pf, "Hydrogen"))
		}
		for _, r := range roots {
			for _, exe := range []string{"h2cli.exe", "hydrogen.exe"} {
				p := filepath.Join(r, exe)
				if fileExists(p) {
					return p, true
				}
			}
		}
	}
	return "", false
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// osExecutable returns the running executable path (a thin wrapper over os.Executable,
// used by tests as a known-real file for the CLI-override path).
func osExecutable() (string, error) { return os.Executable() }
