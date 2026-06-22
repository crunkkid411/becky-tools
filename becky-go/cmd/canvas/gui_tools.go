//go:build gui

// gui_tools.go — the curated becky tool list the canvas shows, plus the runner that
// launches a tool's real .exe against a target. This is the heart of "Jordan clicks a
// tool and it runs": the catalog mirrors cmd/ask/catalog.go (which is package main and
// not importable), grouped into sections Jordan can scan, in plain language.
//
// degrade-never-crash (CLAUDE.md §2): a missing exe / missing target / a tool that
// exits non-zero all surface as a friendly message in the output panel — the window
// never panics. The runner always runs off the UI goroutine.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/pathx"
	"becky-go/internal/proc"
)

// toolItem is one clickable becky tool: a binary name (becky-<name>.exe), a plain
// one-line description, the group it belongs to, and keywords for command routing.
type toolItem struct {
	// Exe is the tool binary's base name WITHOUT the becky- prefix or .exe suffix
	// (e.g. "transcribe" -> becky-transcribe.exe). Resolved next to becky-canvas.exe.
	Exe string
	// Label is what Jordan reads in the list (the friendly name).
	Label string
	// Desc is a one-line, plain-English description (clarity over jargon).
	Desc string
	// Group buckets the tool in the list (Audio/Video, Music/DAW, Research/OSINT, Utility).
	Group string
	// NeedsTarget is false for tools that work without a file/folder.
	NeedsTarget bool
	// Keywords route a typed command/phrase to this tool (lowercase, simple contains).
	Keywords []string
	// OutFlag is the tool's output flag (e.g. "--output", "--out", "--output-dir"), or
	// "" when the tool doesn't write a sidecar. When set, the canvas passes
	//   OutFlag <sidecar>
	// so the result lands NEXT TO THE SOURCE (matching becky-ask), not just stdout.
	OutFlag string
	// OutKind says whether OutFlag wants a FILE path (outFile) or a DIRECTORY (outDir).
	OutKind outKind
	// OutExt is the sidecar file extension for outFile tools, e.g. ".transcribe.json".
	OutExt string
}

// outKind distinguishes a tool whose output flag takes a file vs a directory.
type outKind int

const (
	outNone outKind = iota // no sidecar
	outFile                // OutFlag wants a file path
	outDir                 // OutFlag wants a directory
)

// Tool groups — kept as catalog metadata (they describe what each tool is for) even
// though the icon dock no longer renders a grouped text list. A future "more tools"
// overflow can lean on these without re-deriving them.
const (
	groupAudioVideo = "Audio / Video"
	groupMusicDAW   = "Music / DAW"
	groupResearch   = "Research / OSINT"
	groupUtility    = "Utility"
)

// catalog is the curated tool set. Mirrors cmd/ask/catalog.go's toolCatalog plus the
// creative tools (compose/daw/hum/vox/mix) and the canvas's sibling tools. Kept short
// and friendly — this is Jordan's menu, not the full op switch.
func catalog() []toolItem {
	return []toolItem{
		// --- Audio / Video ---
		{Exe: "transcribe", Label: "Transcribe", Desc: "Turn speech into text with timestamps (captions/subtitles).", Group: groupAudioVideo, NeedsTarget: true, Keywords: []string{"transcribe", "subtitles", "captions", "speech", "srt", "text"}, OutFlag: "--output", OutKind: outFile, OutExt: ".transcribe.json"},
		{Exe: "diarize", Label: "Diarize speakers", Desc: "Tell how many people speak and when each one talks.", Group: groupAudioVideo, NeedsTarget: true, Keywords: []string{"diarize", "speakers", "who spoke", "voices"}, OutFlag: "--output", OutKind: outFile, OutExt: ".diarize.json"},
		{Exe: "identify", Label: "Identify people", Desc: "Match known people in a video by voice and face.", Group: groupAudioVideo, NeedsTarget: true, Keywords: []string{"identify", "recognize", "who is in", "face", "voice match"}, OutFlag: "--output", OutKind: outFile, OutExt: ".identify.json"},
		{Exe: "validate", Label: "Describe actions", Desc: "Plain-language description of what happens on screen.", Group: groupAudioVideo, NeedsTarget: true, Keywords: []string{"validate", "describe", "what happens", "actions", "on-screen"}, OutFlag: "--output", OutKind: outFile, OutExt: ".validate.json"},
		{Exe: "events", Label: "Find events", Desc: "Surface the notable moments in a video for review.", Group: groupAudioVideo, NeedsTarget: true, Keywords: []string{"events", "notable", "moments", "highlights"}, OutFlag: "--output", OutKind: outFile, OutExt: ".events.json"},
		{Exe: "cut", Label: "Cut silence", Desc: "Trim dead air / silence out of a video.", Group: groupAudioVideo, NeedsTarget: true, Keywords: []string{"cut", "trim", "silence", "dead air", "edit"}},
		{Exe: "pipeline", Label: "Full pass", Desc: "Run the whole forensic pass over a video or folder.", Group: groupAudioVideo, NeedsTarget: true, Keywords: []string{"pipeline", "full pass", "everything", "all steps"}},

		// --- Music / DAW ---
		{Exe: "compose", Label: "Compose music", Desc: "Make genre-aware multi-track MIDI stems from a style.", Group: groupMusicDAW, NeedsTarget: false, Keywords: []string{"compose", "music", "midi", "song", "beat", "genre"}},
		{Exe: "hum", Label: "Hum to MIDI", Desc: "Turn a sung/hummed clip into key, tempo and MIDI.", Group: groupMusicDAW, NeedsTarget: true, Keywords: []string{"hum", "sing", "melody", "tune", "whistle"}},
		{Exe: "vox", Label: "Align vocals", Desc: "Line up and comp multiple vocal takes by timing and pitch.", Group: groupMusicDAW, NeedsTarget: true, Keywords: []string{"vox", "vocal", "align", "comp", "takes", "tuning"}},
		{Exe: "mix", Label: "Mix (JST)", Desc: "Build a deterministic mix plan with sidechain and FX buses.", Group: groupMusicDAW, NeedsTarget: true, Keywords: []string{"mix", "mixdown", "sidechain", "bus", "master"}, OutFlag: "--out", OutKind: outFile, OutExt: ".mix.json"},
		{Exe: "daw", Label: "DAW scene", Desc: "Load a song project into the DAW scene model.", Group: groupMusicDAW, NeedsTarget: true, Keywords: []string{"daw", "project", "scene", "tracks", "arrange"}},

		// --- Research / OSINT ---
		{Exe: "research", Label: "Deep research", Desc: "Plan, search, verify and write a cited research answer.", Group: groupResearch, NeedsTarget: false, Keywords: []string{"research", "investigate", "deep dive", "report"}},
		{Exe: "osint", Label: "OSINT signals", Desc: "Pull on-screen text, places and identifiers from frames.", Group: groupResearch, NeedsTarget: true, Keywords: []string{"osint", "location", "address", "signs", "identifiers"}, OutFlag: "--output", OutKind: outFile, OutExt: ".osint.json"},
		{Exe: "ocr", Label: "Read on-screen text", Desc: "Read signs, documents and captions that appear on screen.", Group: groupResearch, NeedsTarget: true, Keywords: []string{"ocr", "read text", "document", "sign", "caption"}, OutFlag: "--output", OutKind: outFile, OutExt: ".ocr.json"},
		{Exe: "palantir", Label: "Entity graph", Desc: "Build a cross-evidence entity/relationship graph.", Group: groupResearch, NeedsTarget: true, Keywords: []string{"palantir", "graph", "entities", "links", "network"}},
		{Exe: "search", Label: "Search corpus", Desc: "Keyword + meaning search across the transcribed corpus.", Group: groupResearch, NeedsTarget: false, Keywords: []string{"search", "find", "look for", "query", "mentions"}},
		{Exe: "radar", Label: "Update radar", Desc: "Surface models/tools you flagged vs becky's dependencies.", Group: groupResearch, NeedsTarget: false, Keywords: []string{"radar", "updates", "flagged", "history"}},

		// --- Utility ---
		{Exe: "freshness", Label: "Check for updates", Desc: "Report which of becky's models/tools have newer versions.", Group: groupUtility, NeedsTarget: false, Keywords: []string{"freshness", "updates", "newer", "upstream", "versions"}},
		{Exe: "framematch", Label: "Match frames", Desc: "Find same-place / same-subject frame pairs for an exhibit.", Group: groupUtility, NeedsTarget: true, Keywords: []string{"framematch", "same place", "compare frames", "exhibit"}, OutFlag: "--output", OutKind: outFile, OutExt: ".framematch.json"},
		{Exe: "review", Label: "Summarize findings", Desc: "Reason over collected findings (the LLM step).", Group: groupUtility, NeedsTarget: true, Keywords: []string{"review", "summarize", "analysis", "reason"}, OutFlag: "--output", OutKind: outFile, OutExt: ".review.json"},
		{Exe: "export", Label: "Export package", Desc: "Bundle findings and clips into a shareable package.", Group: groupUtility, NeedsTarget: true, Keywords: []string{"export", "package", "report out", "share"}},
		{Exe: "web2md", Label: "Web to markdown", Desc: "Save a web page as clean markdown for the case wiki.", Group: groupUtility, NeedsTarget: false, Keywords: []string{"web2md", "web to markdown", "scrape", "article", "url"}},
	}
}

// matchTool routes a typed command/phrase to the best catalog tool by keyword. It
// returns the matched tool and true, or a zero tool and false when nothing matches.
// Matching is case-insensitive and prefers an exact tool-name hit, then a keyword hit.
func matchTool(phrase string) (toolItem, bool) {
	q := strings.ToLower(strings.TrimSpace(phrase))
	if q == "" {
		return toolItem{}, false
	}
	// Exact tool-name / label match first (most predictable for Jordan).
	for _, t := range catalog() {
		if q == t.Exe || q == "becky-"+t.Exe || q == strings.ToLower(t.Label) {
			return t, true
		}
	}
	// Then a keyword contains-match, longest keyword first so "speech to text"
	// beats a stray "text".
	best := toolItem{}
	bestLen := 0
	found := false
	for _, t := range catalog() {
		for _, kw := range t.Keywords {
			if strings.Contains(q, kw) && len(kw) > bestLen {
				best, bestLen, found = t, len(kw), true
			}
		}
	}
	return best, found
}

// exeName returns the platform binary name for a tool (becky-<exe>.exe on Windows).
func (t toolItem) exeName() string {
	name := "becky-" + t.Exe
	if isWindows() {
		name += ".exe"
	}
	return name
}

// resolveToolPath finds the tool's binary (becky-<exe>.exe) next to becky-canvas, etc.
func resolveToolPath(t toolItem) (string, error) {
	return resolveExe(t.exeName())
}

// resolveExe finds an executable by base name next to the running becky-canvas executable
// (the bin/ folder). Falls back to the current working directory and a sibling bin/, then
// to PATH lookup, so the GUI still works when launched from an IDE or `go run`. Returns
// the resolved path, or an error describing what was tried (friendly, no panic).
func resolveExe(name string) (string, error) {
	var tried []string

	if self, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(self), name)
		if fileExists(cand) {
			return cand, nil
		}
		tried = append(tried, cand)
	}
	if wd, err := os.Getwd(); err == nil {
		cand := filepath.Join(wd, name)
		if fileExists(cand) {
			return cand, nil
		}
		candBin := filepath.Join(wd, "bin", name)
		if fileExists(candBin) {
			return candBin, nil
		}
		tried = append(tried, cand, candBin)
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("couldn't find %s. Looked next to becky-canvas and in:\n  %s",
		name, strings.Join(tried, "\n  "))
}

// runResult is one line of streamed output (or a terminal status) from a tool run.
type runResult struct {
	// Line is a chunk of text to append to the output panel.
	Line string
	// Done marks the final message of a run (success or failure summary).
	Done bool
}

// toolRunTimeout caps how long a single tool run may take before it is cancelled, so a
// hung tool can never wedge the canvas. Forensic passes can be long; this is generous.
const toolRunTimeout = 30 * time.Minute

// runTool launches the tool against target and streams stdout+stderr line-by-line onto
// out. It is meant to run in its OWN goroutine (never the UI thread). It guards every
// failure mode — missing exe, missing target, non-zero exit, timeout — and reports it
// as a friendly line, then a Done marker. It never panics and always closes out.
func runTool(t toolItem, target string, out chan<- runResult) {
	defer close(out)

	if t.NeedsTarget && strings.TrimSpace(target) == "" {
		out <- runResult{Line: fmt.Sprintf("%s needs a file or folder. Paste a path in the Target box (or click Browse), then click the tool again.", t.Label)}
		out <- runResult{Done: true}
		return
	}

	exePath, err := resolveToolPath(t)
	if err != nil {
		out <- runResult{Line: "Couldn't start this tool:\n" + err.Error()}
		out <- runResult{Line: "Tip: build the tools with build-all-tools.bat so the .exe files sit next to becky-canvas."}
		out <- runResult{Done: true}
		return
	}

	args, sidecar := t.commandArgs(target)
	out <- runResult{Line: fmt.Sprintf("> %s %s", t.exeName(), strings.Join(args, " "))}

	ctx, cancel := context.WithTimeout(context.Background(), toolRunTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exePath, args...)
	proc.NoWindow(cmd) // no console-window flash over the GUI when a tool runs
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		out <- runResult{Line: "Couldn't open the tool's output: " + err.Error()}
		out <- runResult{Done: true}
		return
	}
	cmd.Stderr = cmd.Stdout // fold stderr into the same stream (one panel)

	if err := cmd.Start(); err != nil {
		out <- runResult{Line: "Couldn't run the tool: " + err.Error()}
		out <- runResult{Done: true}
		return
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // big JSON lines are fine
	for scanner.Scan() {
		out <- runResult{Line: scanner.Text()}
	}

	err = cmd.Wait()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		out <- runResult{Line: fmt.Sprintf("%s ran longer than %s and was stopped.", t.Label, toolRunTimeout)}
	case err != nil:
		out <- runResult{Line: fmt.Sprintf("%s finished with a problem: %v", t.Label, err)}
	default:
		out <- runResult{Line: fmt.Sprintf("%s finished.", t.Label)}
		// Confirm the sidecar landed next to the source (the becky save protocol).
		if sidecar != "" && fileExists(sidecar) {
			out <- runResult{Line: "Saved: " + sidecar}
		}
	}
	out <- runResult{Done: true}
}

// commandArgs builds the argument list for a tool run plus the sidecar path the tool was
// told to write (or "" when it writes none). The target is the first positional argument;
// when the tool declares an output flag, the canvas appends
//
//	OutFlag <sidecar>
//
// so the result lands NEXT TO THE SOURCE FILE — matching how becky-ask saves — instead of
// only streaming to stdout. outFile tools get "<dir>/<base><OutExt>"; outDir tools get the
// source's directory. Tools with no target and no out flag get no args.
func (t toolItem) commandArgs(target string) (args []string, sidecar string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, ""
	}
	args = []string{target}
	switch t.OutKind {
	case outFile:
		sidecar = sidecarPath(target, t.OutExt)
		args = append(args, t.OutFlag, sidecar)
	case outDir:
		dir := pathx.Dir(target)
		args = append(args, t.OutFlag, dir)
		// outDir tools name their own files inside dir; we don't predict the path, so
		// no single "Saved:" line — the tool's own output reports what it wrote.
	}
	return args, sidecar
}

// sidecarPath builds "<dir><sep><base-without-ext><ext>" for a source file, so e.g.
// C:\clips\interview.mp4 + ".transcribe.json" -> C:\clips\interview.transcribe.json.
// It preserves the source's own separator (Windows '\' or POSIX '/') by splitting on the
// last separator in place, because targets are often Windows paths even on CI.
func sidecarPath(src, ext string) string {
	sep := strings.LastIndexAny(src, `/\`)
	dirWithSep := ""
	base := src
	if sep >= 0 {
		dirWithSep = src[:sep+1] // keep the separator
		base = src[sep+1:]
	}
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	return dirWithSep + base + ext
}

// fileExists reports whether path exists and is a regular file (not a directory).
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// --- record (mic) ----------------------------------------------------------------
//
// The RECORD dock button drives becky-daw-engine's real-audio backend:
//
//	becky-daw-engine --record <seconds> <out.wav>
//
// That subcommand only exists in the audio build of becky-daw-engine (built with
// `-tags audio`). If the exe is missing OR it's the default no-audio build (the flag is
// unknown / nothing is recorded), the run degrades to a friendly neon line — never a
// crash. On success the canvas adopts the new WAV as the target (see drainRecord).

// recordSeconds is how long the RECORD button captures for (a short take; the canvas is a
// quick-capture surface, not a full session recorder).
const recordSeconds = 8

// recordExe is the audio-engine binary that performs the recording.
const recordExe = "becky-daw-engine"

// recordOutPath returns where a recording should be written: next to becky-canvas (or the
// working dir) as a uniquely-named WAV so successive takes don't clobber each other.
func recordOutPath() string {
	name := fmt.Sprintf("becky-rec-%d.wav", time.Now().Unix())
	if self, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(self), name)
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, name)
	}
	return name
}

// runRecord launches becky-daw-engine --record and streams its output. It runs in its OWN
// goroutine (never the UI thread), guards every failure (missing exe, no-audio build,
// non-zero exit, timeout) with a friendly line, and always closes out with a Done marker.
func runRecord(outPath string, seconds int, out chan<- runResult) {
	defer close(out)

	exeName := recordExe
	if isWindows() {
		exeName += ".exe"
	}
	exePath, err := resolveExe(exeName)
	if err != nil {
		out <- runResult{Line: "Recording needs the audio engine (becky-daw-engine) next to becky-canvas."}
		out <- runResult{Line: "Tip: build the tools with build-all-tools.bat (the audio build), then try Record again."}
		out <- runResult{Done: true}
		return
	}

	args := []string{"--record", fmt.Sprintf("%d", seconds), outPath}
	out <- runResult{Line: fmt.Sprintf("> %s %s", exeName, strings.Join(args, " "))}
	out <- runResult{Line: fmt.Sprintf("Recording %d seconds from the mic…", seconds)}

	ctx, cancel := context.WithTimeout(context.Background(), toolRunTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exePath, args...)
	proc.NoWindow(cmd) // no console-window flash over the GUI while recording
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		out <- runResult{Line: "Couldn't open the recorder's output: " + err.Error()}
		out <- runResult{Done: true}
		return
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		out <- runResult{Line: "Couldn't start the recorder: " + err.Error()}
		out <- runResult{Done: true}
		return
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		out <- runResult{Line: scanner.Text()}
	}

	err = cmd.Wait()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		out <- runResult{Line: "Recording timed out and was stopped."}
	case err != nil:
		out <- runResult{Line: fmt.Sprintf("Recording finished with a problem: %v", err)}
		out <- runResult{Line: "If nothing recorded, this becky-daw-engine may be the no-audio build — rebuild with the audio backend."}
	case fileExists(outPath):
		out <- runResult{Line: "Saved: " + outPath}
	default:
		out <- runResult{Line: "Recorder finished but no WAV appeared (likely the no-audio build)."}
	}
	out <- runResult{Done: true}
}
