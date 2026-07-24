package main

// transcribe.go wires becky's caption pipeline into becky-clip so a detective can
// GENERATE a usable transcript for raw footage — WITHOUT ever altering the
// original files (the load-bearing forensic invariant of this whole project).
//
// The sequence for one video (Jordan's exact requirements):
//  1. Ask becky-captions whether a TRUSTWORTHY official transcript already exists
//     beside the video or can be cheaply fetched from YouTube — and crucially,
//     whether the official captions COVER the full downloaded video (he edits
//     incriminating segments out of his livestreams with YouTube's "edit"
//     feature, which shortens the captions; a 2-hour .srt for a 3-hour video means
//     the stream was edited and the official transcript is NOT trustworthy).
//  2. If becky-captions says use_official → the official .srt is already in the
//     folder (present or just fetched). We do NOTHING but re-index so it's picked
//     up. We never re-transcribe over a complete official transcript.
//  3. If it says local_needed (no official transcript, none available online, or
//     the official one is short because the stream was edited) — OR becky-captions
//     is not installed — we run the real local ASR (becky-transcribe), writing to
//     a SEPARATE "<stem>_parakeet_transcription.srt" sidecar. We NEVER overwrite an
//     official ".srt"/".en.srt"; the original transcript stays byte-for-byte intact,
//     and we keep two versions exactly as he asked.
//
// HARD INVARIANTS (CLAUDE.md §2, Jordan's project conditions): the original video
// AND any original/official .srt are NEVER written — local ASR only ever produces
// the becky-owned "<stem>_parakeet_transcription.srt". Degrade-never-crash: a missing
// becky-transcribe binary is a typed error; a missing becky-captions binary is not
// fatal (we just go straight to local ASR); one failed video in a batch is
// recorded and skipped. The real execs (becky-captions, becky-transcribe) sit
// behind the runCaptions / runTranscribe seams so `go test` exercises the wiring
// with fakes — no test ever shells the real ASR/yt-dlp/ffmpeg/GPU.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/captions"
	"becky-go/internal/footage"
	"becky-go/internal/proc"
	"becky-go/internal/qmdindex"
)

// transcribeTimeout bounds one becky-transcribe exec. Transcription of a long
// video is slow (audio extraction + Parakeet inference), so the default is
// generous; BECKY_TRANSCRIBE_TIMEOUT (a Go duration like "45m") overrides it.
const transcribeTimeout = 30 * time.Minute

// captionsTimeout bounds one becky-captions exec. It probes the video and may do
// a single yt-dlp subtitle fetch (no media download), so it is far quicker than
// ASR; BECKY_CAPTIONS_TIMEOUT overrides it.
const captionsTimeout = 10 * time.Minute

// runTranscribe is the seam over the real ASR exec. It runs
//
//	becky-transcribe <videoPath> --format srt --output <srtOut>
//
// writing the subtitle sidecar to srtOut (which is the becky-owned
// "<stem>_parakeet_transcription.srt" beside the source video). It defaults to the real
// exec.CommandContext; tests override it with a fake that writes a canned .srt so
// the whole transcribe→re-index flow is exercised offline. Production never
// reassigns it.
var runTranscribe = func(ctx context.Context, transcribeBin, videoPath, srtOut string) error {
	cmd := exec.CommandContext(ctx, transcribeBin, videoPath, "--format", "srt", "--output", srtOut)
	proc.NoWindow(cmd) // becky-transcribe is a console app; no flash when the GUI spawns it
	// Diagnostics (Parakeet/ffmpeg progress) go to the clip's stderr; stdout is
	// unused for --output runs. Capture stderr so a failure carries a readable tail.
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("becky-transcribe failed: %w%s", err, transcribeErrTail(errBuf.String()))
	}
	return nil
}

// runCaptions is the seam over the real becky-captions exec. It runs
//
//	becky-captions <videoPath> --json [--offline]
//
// and parses the captions.Decision from stdout. It defaults to the real exec;
// tests override it with a fake that returns a canned Decision so the
// captions→ASR routing is exercised offline. Production never reassigns it. A
// non-zero exit or unparseable output is an error (the caller then degrades to
// local ASR — a missing/edited transcript must never block making one).
var runCaptions = func(ctx context.Context, captionsBin, videoPath string, offline bool) (captions.Decision, error) {
	args := []string{videoPath, "--json"}
	if offline {
		args = append(args, "--offline")
	}
	cmd := exec.CommandContext(ctx, captionsBin, args...)
	proc.NoWindow(cmd)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		return captions.Decision{}, fmt.Errorf("becky-captions failed: %w%s", err, transcribeErrTail(errBuf.String()))
	}
	var d captions.Decision
	if err := json.Unmarshal(out, &d); err != nil {
		return captions.Decision{}, fmt.Errorf("becky-captions returned unparseable JSON: %w", err)
	}
	return d, nil
}

// transcribeErrTail formats the last of a captured stderr for an error message
// (compact, prefixed with a newline only when there is content).
func transcribeErrTail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 600 {
		s = s[len(s)-600:]
	}
	return "\n" + s
}

// TranscribeError is the typed error returned when becky-transcribe can't be
// located, so the GUI can show Jordan a plain-language fix rather than a panic.
type TranscribeError struct{ msg string }

func (e *TranscribeError) Error() string { return e.msg }

// resolveTranscribeBin finds the becky-transcribe executable, in order:
//
//	(a) the BECKY_TRANSCRIBE env var (an explicit path),
//	(b) next to the running becky-clip executable (os.Executable dir),
//	(c) on PATH (exec.LookPath).
//
// On none-found it returns a *TranscribeError with a plain-language message
// (never a panic). Used for both single and batch transcription.
func resolveTranscribeBin() (string, error) {
	// (a) explicit override.
	if p := strings.TrimSpace(os.Getenv("BECKY_TRANSCRIBE")); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if fileExists(p) {
			return p, nil
		}
		return "", &TranscribeError{msg: fmt.Sprintf("becky-transcribe not found at BECKY_TRANSCRIBE=%q", p)}
	}

	// (b) next to the running executable (how becky-clip.exe + becky-transcribe.exe
	// ship side by side via build-all-tools.bat).
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), transcribeExeName())
		if fileExists(cand) {
			return cand, nil
		}
	}

	// (c) on PATH.
	if p, err := exec.LookPath("becky-transcribe"); err == nil {
		return p, nil
	}

	return "", &TranscribeError{msg: "becky-transcribe not found — build it with build-all-tools.bat (or set BECKY_TRANSCRIBE to its path)"}
}

// resolveCaptionsBin finds the becky-captions executable, in the same order as
// resolveTranscribeBin: BECKY_CAPTIONS → next to the running exe → PATH. Unlike
// becky-transcribe, becky-captions is OPTIONAL: if it can't be located we return
// ("", false) and the caller goes straight to local ASR (the original behaviour),
// so an install without becky-captions still works. A BECKY_CAPTIONS that points
// at a missing file also yields ("", false) — degrade, don't fail.
func resolveCaptionsBin() (string, bool) {
	if p := strings.TrimSpace(os.Getenv("BECKY_CAPTIONS")); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if fileExists(p) {
			return p, true
		}
		return "", false
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), captionsExeName())
		if fileExists(cand) {
			return cand, true
		}
	}
	if p, err := exec.LookPath("becky-captions"); err == nil {
		return p, true
	}
	return "", false
}

// transcribeExeName / captionsExeName are the sibling binaries' filenames for the
// host OS (.exe on Windows).
func transcribeExeName() string {
	if isWindows() {
		return "becky-transcribe.exe"
	}
	return "becky-transcribe"
}

func captionsExeName() string {
	if isWindows() {
		return "becky-captions.exe"
	}
	return "becky-captions"
}

// localSrtSidecarPath returns the LOCAL-ASR (Parakeet) subtitle sidecar path for a
// video: "<stem>_parakeet_transcription.srt" in the SAME directory as the source
// (separator-safe). The "_parakeet_transcription" suffix is the forensic guarantee:
// local re-transcription NEVER touches an original/official "<stem>.srt" or
// "<stem>.en.srt" — it writes a clearly becky-generated, separate file that ALWAYS
// names itself, so the case always has both versions and the original transcript is
// provably unaltered. The suffix is the single shared constant
// footage.LocalTranscriptMarker, so the writer here and the indexer
// (footage.discover) can never drift apart.
func localSrtSidecarPath(videoPath string) string {
	dir := filepath.Dir(videoPath)
	base := filepath.Base(videoPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = base
	}
	return filepath.Join(dir, stem+footage.LocalTranscriptMarker+".srt")
}

// officialSrtExists reports whether an ORIGINAL/official subtitle (<stem>.en.srt
// or <stem>.srt — NOT a _parakeet_transcription one) sits next to the video. It is
// the safety interlock for the forensic invariant: before local ASR writes
// anything, we confirm we are not about to clobber an original. (We only ever write
// to the _parakeet_transcription path, but this guard documents and enforces
// "originals are sacred" even if a future change altered the output path.)
func officialSrtExists(videoPath string) bool {
	return fileExists(captions.OfficialSRTPath(videoPath)) ||
		fileExists(bareSrtPath(videoPath))
}

// bareSrtPath returns "<stem>.srt" beside the video.
func bareSrtPath(videoPath string) string {
	dir := filepath.Dir(videoPath)
	base := filepath.Base(videoPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join(dir, stem+".srt")
}

// transcribeContext builds a per-exec context with the (overridable) timeout.
// The caller must defer the returned cancel.
func transcribeContext(parent context.Context) (context.Context, context.CancelFunc) {
	to := transcribeTimeout
	if d := strings.TrimSpace(os.Getenv("BECKY_TRANSCRIBE_TIMEOUT")); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil && parsed > 0 {
			to = parsed
		}
	}
	return context.WithTimeout(parent, to)
}

// captionsContext builds a per-exec context with the (overridable) becky-captions
// timeout. The caller must defer the returned cancel.
func captionsContext(parent context.Context) (context.Context, context.CancelFunc) {
	to := captionsTimeout
	if d := strings.TrimSpace(os.Getenv("BECKY_CAPTIONS_TIMEOUT")); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil && parsed > 0 {
			to = parsed
		}
	}
	return context.WithTimeout(parent, to)
}

// transcribeOne runs the caption sequence for one indexed video:
//
//  1. forceLocal (the "↻ re-transcribe" intent) skips the caption check entirely
//     and goes straight to local ASR — the user explicitly wants a fresh Parakeet
//     pass even when an official transcript already exists, written to the SEPARATE
//     "<stem>_parakeet_transcription.srt" so the original is never touched.
//  2. Otherwise, if becky-captions is available, ask it. On "use_official" the
//     official .srt is already in place — return nil (the caller re-indexes;
//     nothing was made, nothing was overwritten).
//  3. Otherwise (local_needed, or becky-captions absent / errored) run local ASR
//     to "<stem>_parakeet_transcription.srt" via the runTranscribe seam — NEVER
//     over an official ".srt"/".en.srt". transcribeBin is the pre-resolved
//     becky-transcribe path.
//
// It does NOT re-index — the caller re-indexes once after a batch.
func (a *App) transcribeOne(transcribeBin string, v footage.Video, forceLocal bool) error {
	// (1) Caption decision (optional tool — absence is not fatal). Skipped for a
	// forced re-transcribe, which always wants a fresh local Parakeet secondary.
	if !forceLocal {
		if capBin, ok := resolveCaptionsBin(); ok {
			cctx, ccancel := captionsContext(context.Background())
			dec, err := runCaptions(cctx, capBin, v.Path, false)
			ccancel()
			if err == nil && dec.Action == captions.ActionUseOfficial {
				// A complete official transcript already exists / was fetched beside the
				// video. Do not run local ASR; do not touch the original. Done.
				return nil
			}
			// On err or local_needed we fall through to local ASR. (becky-captions
			// already logged its reasoning to stderr.)
		}
	}

	// (2) Local ASR → the becky-owned _parakeet_transcription sidecar. Never
	// overwrite an official.
	srtOut := localSrtSidecarPath(v.Path)
	if officialSrtExists(v.Path) && filepath.Clean(srtOut) == filepath.Clean(captions.OfficialSRTPath(v.Path)) {
		// Defensive: localSrtSidecarPath is always "_parakeet_transcription", so this
		// can never be an official path — but guard anyway so the invariant can't be
		// broken silently.
		return fmt.Errorf("refusing to overwrite an original transcript at %s", srtOut)
	}
	ctx, cancel := transcribeContext(context.Background())
	defer cancel()
	if err := runTranscribe(ctx, transcribeBin, v.Path, srtOut); err != nil {
		return err
	}
	if !fileExists(srtOut) {
		return fmt.Errorf("becky-transcribe produced no transcript at %s", srtOut)
	}

	// Keep the forensic search index in step with the transcript just
	// produced — nothing else did this, and the index went 3 weeks stale
	// once because of it (2026-07-22 backfill). Convert is fast, pure-Go and
	// best-effort: a failure here must never fail the transcribe operation
	// that already succeeded. The qmd re-index talks to an external binary,
	// so it runs in the background, fire-and-forget (WarmTranscriptCache's
	// pattern).
	a.mu.Lock()
	folder := a.folder
	a.mu.Unlock()
	if folder != "" {
		if _, cerr := qmdindex.Convert(srtOut, qmdindex.MDDir(folder)); cerr == nil {
			go func() { _ = runQmdUpdate() }()
		}
	}
	return nil
}

// Transcribe runs the caption sequence (official-first, local fallback to
// "<stem>_parakeet_transcription.srt") for the video named name (basename) in the
// open folder, then re-indexes and returns the fresh FolderView (so the UI sees
// has_transcript flip to true). When the video already has a transcript it is a
// re-transcribe and a fresh Parakeet pass is forced into that separate sidecar.
// It NEVER overwrites an original/official transcript. This is
// synchronous and long-running (the GUI shows a spinner). becky-transcribe must be
// resolvable for the local-ASR path; becky-captions is optional (its absence just
// skips the official check and goes straight to local ASR).
func (a *App) Transcribe(name string) (FolderView, error) {
	v, ok := a.lookupVideo(name)
	if !ok {
		return FolderView{}, fmt.Errorf("no such video in folder: %s", name)
	}
	bin, err := resolveTranscribeBin()
	if err != nil {
		return FolderView{}, err
	}
	// A video that already indexes as having a transcript means this is the GUI's
	// "↻ re-transcribe" action (the "+" button shows only for untranscribed videos),
	// so force a fresh Parakeet pass into the SEPARATE _parakeet_transcription
	// sidecar instead of short-circuiting on an existing official transcript.
	if err := a.transcribeOne(bin, v, v.HasTranscript); err != nil {
		return FolderView{}, err
	}
	return a.Reindex(), nil
}

// TranscribeAllResult is the reply for transcribe_all: the re-indexed folder plus
// how many videos were transcribed/failed and the per-video failures (so the GUI
// can report "9 done, 1 failed: …" without aborting the whole batch).
type TranscribeAllResult struct {
	Folder      FolderView          `json:"folder"`
	Transcribed int                 `json:"transcribed"`
	Failed      int                 `json:"failed"`
	Errors      []TranscribeFailure `json:"errors,omitempty"`
}

// TranscribeFailure is one video that failed to transcribe in a batch.
type TranscribeFailure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// TranscribeAll runs the per-video caption sequence for every indexed video that
// lacks a transcript, then re-indexes once and returns the fresh FolderView with
// counts. Each video either keeps/fetches its complete official .srt
// (use_official) or gets a becky-owned "<stem>_parakeet_transcription.srt"
// (local_needed) — never an overwritten original. Degrade-never-crash: one video's
// failure is recorded in
// Errors and the batch continues; a missing becky-transcribe binary is a single
// clear error before any work. Videos that already have a transcript are skipped
// (use per-video Transcribe to force a re-transcribe).
func (a *App) TranscribeAll() (TranscribeAllResult, error) {
	a.mu.Lock()
	pending := make([]footage.Video, 0, len(a.index.Videos))
	for _, v := range a.index.Videos {
		if !v.HasTranscript {
			pending = append(pending, v)
		}
	}
	a.mu.Unlock()

	bin, err := resolveTranscribeBin()
	if err != nil {
		return TranscribeAllResult{Folder: a.folderView()}, err
	}

	res := TranscribeAllResult{}
	for _, v := range pending {
		// Batch only fills in MISSING transcripts (pending = !HasTranscript), so the
		// caption-first path is always correct here — never a forced re-transcribe.
		if err := a.transcribeOne(bin, v, false); err != nil {
			res.Failed++
			res.Errors = append(res.Errors, TranscribeFailure{Name: v.Name, Error: firstLine(err)})
			continue
		}
		res.Transcribed++
	}
	res.Folder = a.Reindex()
	return res, nil
}

// Reindex re-walks the open folder via footage.Index and returns the fresh
// FolderView. It is a no-op (empty FolderView) when no folder is open, and safe to
// call after external changes (e.g. a newly written transcript sidecar). On an
// index error the previous index is left intact and the current view returned.
func (a *App) Reindex() FolderView {
	a.mu.Lock()
	folder := a.folder
	a.mu.Unlock()
	if folder == "" {
		return a.folderView() // nothing open — empty view, not a crash
	}
	idx, err := footage.Index(folder)
	if err != nil {
		// Keep the prior index; surfacing the stale view is better than wiping it.
		return a.folderView()
	}
	a.mu.Lock()
	a.index = idx
	a.mu.Unlock()
	return a.folderView()
}

// IndexStatusResult is the reply for both index_status and index_source:
// whether source's transcript is (now) present in the qmd smart-search
// index (internal/qmdindex's "_md" locator folder).
type IndexStatusResult struct {
	Indexed bool `json:"indexed"`
}

// IndexStatus reports whether source's transcript already has a qmd search
// locator, for becky-review's "not yet indexed" icon (mirrors the "+"
// button's has_transcript check). A source with no transcript at all, or
// not resolvable in the open folder, reports NOT indexed rather than
// erroring — there is nothing to find either way and the icon treats both
// the same. Read-only; never writes a locator (see IndexSource for that).
func (a *App) IndexStatus(source string) IndexStatusResult {
	v, ok := a.resolveSourceForRead(source)
	if !ok || !v.HasTranscript {
		return IndexStatusResult{Indexed: false}
	}
	a.mu.Lock()
	folder := a.folder
	a.mu.Unlock()
	if folder == "" {
		return IndexStatusResult{Indexed: false}
	}
	return IndexStatusResult{Indexed: qmdindex.IsIndexed(v.TranscriptPath, qmdindex.MDDir(folder))}
}

// IndexSource converts source's transcript into a qmd locator right now —
// the click-to-fix half of the "not yet indexed" icon, the same "act on
// what the icon is telling you" shape as the "+" button's requestTranscribe.
// A source with no transcript can't be indexed (nothing to convert), which
// is reported as a plain error so the icon's click can surface why.
func (a *App) IndexSource(source string) (IndexStatusResult, error) {
	v, ok := a.resolveSourceForRead(source)
	if !ok || !v.HasTranscript {
		return IndexStatusResult{}, fmt.Errorf("no transcript to index for %s", source)
	}
	a.mu.Lock()
	folder := a.folder
	a.mu.Unlock()
	if folder == "" {
		return IndexStatusResult{}, fmt.Errorf("no folder open")
	}
	if _, err := qmdindex.Convert(v.TranscriptPath, qmdindex.MDDir(folder)); err != nil {
		return IndexStatusResult{}, err
	}
	go func() { _ = runQmdUpdate() }()
	return IndexStatusResult{Indexed: true}, nil
}
