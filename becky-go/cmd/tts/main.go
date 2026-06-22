// Command becky-tts is becky's local spoken voice: text -> WAV (optionally played).
//
// It is a UNIVERSAL standalone tool and NEVER auto-speaks — nothing in becky calls
// it implicitly (SPEC-BECKY-TTS.md §3/§9). The deterministic Go front handles CLI
// parsing, file-safety, the --selftest offline proof, and degrade-never-crash; the
// only AI step is the NeuTTS Air GGUF synthesis, which is the local-wiring boundary.
// When the runtime/model are absent it PRINTS the text + a plain reason and exits
// non-zero — it NEVER falls back to a Microsoft voice (ACCESSIBILITY.md).
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"becky-go/internal/proc"
	"becky-go/internal/tts"
)

// result is the --json machine-status shape.
type result struct {
	OK       bool   `json:"ok"`
	Mode     string `json:"mode"`               // "synth" | "selftest"
	Out      string `json:"out,omitempty"`      // WAV path written (if any)
	Bytes    int    `json:"bytes,omitempty"`    // WAV size
	Rate     int    `json:"rate,omitempty"`     // sample rate
	Played   bool   `json:"played,omitempty"`   // whether --play succeeded
	Degraded bool   `json:"degraded,omitempty"` // synth degraded (text printed)
	Reason   string `json:"reason,omitempty"`   // degrade/error reason
	Text     string `json:"text,omitempty"`     // the text (echoed on degrade)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(argv []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("becky-tts", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		out      = fs.String("out", "", "output WAV path (mandatory unless --play/--selftest)")
		in       = fs.String("in", "", "read the text to speak from this file")
		play     = fs.Bool("play", false, "play the synthesized WAV (best-effort)")
		voice    = fs.String("voice", tts.DefaultVoice, "preset name or a reference sample .wav (NeuTTS clones it)")
		selftest = fs.Bool("selftest", false, "write a deterministic fixture WAV (no model needed) — offline proof")
		seed     = fs.Int64("seed", 42, "determinism seed")
		model    = fs.String("model", "", "override the NeuTTS model GGUF path")
		bin      = fs.String("bin", "", "override the NeuTTS runtime binary path")
		jsonOut  = fs.Bool("json", false, "emit machine-readable status JSON to stdout")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "becky-tts — becky's local voice (text -> WAV). NeuTTS Air, offline.\n\n")
		fmt.Fprintf(stderr, "Usage:\n")
		fmt.Fprintf(stderr, "  becky-tts \"<text>\" --out speech.wav\n")
		fmt.Fprintf(stderr, "  becky-tts --in answer.txt --out speech.wav\n")
		fmt.Fprintf(stderr, "  becky-tts \"<text>\" --play\n")
		fmt.Fprintf(stderr, "  becky-tts --selftest --out s.wav     # offline proof, no model\n\n")
		fs.PrintDefaults()
	}
	// Go's flag package stops at the first non-flag token, but the spec shows the
	// text FIRST ("becky-tts \"<text>\" --out speech.wav"). So lift a leading
	// positional text token out before parsing, and still honour any trailing
	// positional args (so flags work before OR after the text).
	leading, rest := splitLeadingText(argv)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	positional := fs.Args()
	if leading != "" {
		positional = append([]string{leading}, positional...)
	}

	opts := tts.Options{
		Voice: *voice,
		Seed:  *seed,
		Model: *model,
		Bin:   *bin,
	}

	// ----- selftest: deterministic fixture WAV, no model -----
	if *selftest {
		return runSelfTest(*out, *play, *jsonOut, opts, stdout, stderr)
	}

	// ----- resolve the text to speak (positional arg or --in) -----
	text, terr := resolveText(positional, *in)
	if terr != nil {
		fmt.Fprintf(stderr, "becky-tts: %v\n", terr)
		return 2
	}

	// ----- output-path safety -----
	if *out == "" && !*play {
		fmt.Fprintln(stderr, "becky-tts: --out is required unless --play (or --selftest)")
		return 2
	}
	if *out != "" {
		if err := checkWAVExt(*out); err != nil {
			fmt.Fprintf(stderr, "becky-tts: %v\n", err)
			return 2
		}
		if err := refuseNonWAVOverwrite(*out); err != nil {
			fmt.Fprintf(stderr, "becky-tts: %v\n", err)
			return 2
		}
	}

	// ----- synth (the one AI step; degrades to printed text) -----
	wav, err := tts.NewGGUFSynth().Synthesize(text, opts)
	if err != nil {
		return degrade(text, err, *jsonOut, stdout, stderr)
	}

	info, _ := tts.ValidateWAV(wav)

	// Determine where to write: --out, else a temp file for --play.
	target := *out
	if target == "" {
		f, terr := os.CreateTemp("", "becky-tts-*.wav")
		if terr != nil {
			return degrade(text, &tts.DegradeError{Reason: "could not allocate a temp WAV", Err: terr}, *jsonOut, stdout, stderr)
		}
		target = f.Name()
		f.Close()
	}
	if werr := os.WriteFile(target, wav, 0o644); werr != nil {
		return degrade(text, &tts.DegradeError{Reason: "could not write the WAV", Err: werr}, *jsonOut, stdout, stderr)
	}

	res := result{OK: true, Mode: "synth", Out: target, Bytes: len(wav), Rate: info.SampleRate, Text: text}

	if *play {
		if perr := playWAV(target); perr != nil {
			// Best-effort: the WAV is still written/kept. Note it, don't fail hard.
			res.Played = false
			res.Reason = "playback failed (WAV kept): " + perr.Error()
		} else {
			res.Played = true
		}
	}

	emit(res, *jsonOut, stdout, stderr)
	return 0
}

// runSelfTest writes the deterministic fixture WAV. It needs no model and is the
// offline proof path; it still honours --out safety + best-effort --play.
func runSelfTest(out string, play, jsonOut bool, opts tts.Options, stdout, stderr *os.File) int {
	if out == "" && !play {
		fmt.Fprintln(stderr, "becky-tts: --selftest needs --out (or --play)")
		return 2
	}
	if out != "" {
		if err := checkWAVExt(out); err != nil {
			fmt.Fprintf(stderr, "becky-tts: %v\n", err)
			return 2
		}
		if err := refuseNonWAVOverwrite(out); err != nil {
			fmt.Fprintf(stderr, "becky-tts: %v\n", err)
			return 2
		}
	}
	wav, err := tts.SelfTest(opts)
	if err != nil {
		fmt.Fprintf(stderr, "becky-tts: selftest failed: %v\n", err)
		return 1
	}
	info, _ := tts.ValidateWAV(wav)
	target := out
	if target == "" {
		f, terr := os.CreateTemp("", "becky-tts-selftest-*.wav")
		if terr != nil {
			fmt.Fprintf(stderr, "becky-tts: %v\n", terr)
			return 1
		}
		target = f.Name()
		f.Close()
	}
	if werr := os.WriteFile(target, wav, 0o644); werr != nil {
		fmt.Fprintf(stderr, "becky-tts: could not write %s: %v\n", target, werr)
		return 1
	}
	res := result{OK: true, Mode: "selftest", Out: target, Bytes: len(wav), Rate: info.SampleRate}
	if play {
		if perr := playWAV(target); perr != nil {
			res.Played = false
			res.Reason = "playback failed (WAV kept): " + perr.Error()
		} else {
			res.Played = true
		}
	}
	emit(res, jsonOut, stdout, stderr)
	return 0
}

// splitLeadingText lifts a single leading positional text token (one not starting
// with '-') off the front of argv so flags may follow it, returning that token and
// the remaining argv to hand to the flag parser. If argv starts with a flag, the
// leading text is empty and argv is returned unchanged.
func splitLeadingText(argv []string) (string, []string) {
	if len(argv) == 0 {
		return "", argv
	}
	if strings.HasPrefix(argv[0], "-") {
		return "", argv
	}
	return argv[0], argv[1:]
}

// resolveText returns the text to speak: positional args joined, else --in file.
func resolveText(args []string, in string) (string, error) {
	inline := strings.TrimSpace(strings.Join(args, " "))
	if in != "" {
		if inline != "" {
			return "", errors.New("provide text OR --in, not both")
		}
		b, err := os.ReadFile(in)
		if err != nil {
			return "", fmt.Errorf("could not read --in %s: %w", in, err)
		}
		t := strings.TrimSpace(string(b))
		if t == "" {
			return "", fmt.Errorf("--in %s is empty", in)
		}
		return t, nil
	}
	if inline == "" {
		return "", errors.New("nothing to speak: give text in quotes or use --in")
	}
	return inline, nil
}

// checkWAVExt rejects an --out path that is not a .wav (becky only emits WAV).
func checkWAVExt(path string) error {
	if !strings.EqualFold(filepath.Ext(path), ".wav") {
		return fmt.Errorf("--out must end in .wav (got %q)", path)
	}
	return nil
}

// refuseNonWAVOverwrite never clobbers an existing file that is NOT already a valid
// WAV (the becky sidecar-safety rule — protects a real document with a .wav-looking
// name from being destroyed).
func refuseNonWAVOverwrite(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil // does not exist (or unreadable) — writing is fine/will surface later
	}
	if len(b) == 0 {
		return nil // empty placeholder — safe to overwrite
	}
	if _, verr := tts.ValidateWAV(b); verr != nil {
		return fmt.Errorf("refusing to overwrite %s — it exists and is not a WAV", path)
	}
	return nil
}

// degrade prints the text so the human still gets the content, prints the reason to
// stderr, and returns a non-zero exit code. It NEVER substitutes another voice.
func degrade(text string, err error, jsonOut bool, stdout, stderr *os.File) int {
	reason := err.Error()
	if d, ok := tts.AsDegrade(err); ok {
		reason = d.Error()
	}
	if jsonOut {
		emit(result{OK: false, Mode: "synth", Degraded: true, Reason: reason, Text: text}, true, stdout, stderr)
	} else {
		// The content the human asked to hear — printed so it is never lost.
		fmt.Fprintln(stdout, text)
		fmt.Fprintf(stderr, "becky-tts: could not speak (%s); printed the text instead.\n", reason)
	}
	return 1
}

// emit writes the success result as JSON or a short human line.
func emit(res result, jsonOut bool, stdout, stderr *os.File) {
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
		return
	}
	if res.Out != "" {
		fmt.Fprintf(stdout, "Saved: %s (%d bytes, %d Hz)\n", res.Out, res.Bytes, res.Rate)
	}
	if res.Played {
		fmt.Fprintln(stdout, "Played.")
	} else if res.Reason != "" {
		fmt.Fprintf(stderr, "becky-tts: %s\n", res.Reason)
	}
}

// playWAV plays a WAV best-effort via a per-OS system player. Failure is non-fatal
// (the WAV is already written/kept). It NEVER invokes a TTS engine — it only plays
// the already-rendered file.
func playWAV(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// PowerShell's SoundPlayer plays a WAV synchronously with no extra deps.
		ps := fmt.Sprintf("(New-Object Media.SoundPlayer '%s').PlaySync()", path)
		cmd = exec.Command("powershell", "-NoProfile", "-Command", ps)
	case "darwin":
		cmd = exec.Command("afplay", path)
	default:
		// Linux: try a couple of common players; the first present wins.
		for _, p := range []string{"aplay", "paplay", "ffplay"} {
			if _, err := exec.LookPath(p); err == nil {
				if p == "ffplay" {
					cmd = exec.Command(p, "-nodisp", "-autoexit", "-loglevel", "quiet", path)
				} else {
					cmd = exec.Command(p, path)
				}
				break
			}
		}
		if cmd == nil {
			return errors.New("no audio player found (tried aplay/paplay/ffplay)")
		}
	}
	proc.NoWindow(cmd)
	return cmd.Run()
}
