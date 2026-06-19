package main

// transcribe.go wires the existing, working becky-transcribe ASR tool into
// becky-clip so a detective can GENERATE a transcript for raw footage that has
// no .srt sidecar (the showstopper: SPEC-BECKY-CLIP / FIX-PLAN — becky-clip was
// entirely transcript-gated with no way to make one). It runs becky-transcribe
// on a source video, writes the subtitle sidecar (<stem>.srt) NEXT TO the source
// (exactly like the <video>.beckymeta.json sidecar — the original video bytes are
// NEVER written), then re-indexes the open folder so the new transcript is
// immediately searchable.
//
// HARD INVARIANTS (CLAUDE.md §2, FIX-PLAN): originals are sacred — only a NEW
// sidecar beside the video is written, never the video. Degrade-never-crash: a
// missing becky-transcribe binary is a clear typed error, and a failed video in a
// batch is recorded and skipped, never a panic. The real ASR exec sits behind the
// runTranscribe seam (mirroring app.go's pickFolderFn) so `go test` exercises the
// wiring with a fake that writes a canned .srt — no test ever shells the real
// ASR/ffmpeg/GPU.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/footage"
	"becky-go/internal/proc"
)

// transcribeTimeout bounds one becky-transcribe exec. Transcription of a long
// video is slow (audio extraction + Parakeet inference), so the default is
// generous; BECKY_TRANSCRIBE_TIMEOUT (a Go duration like "45m") overrides it.
const transcribeTimeout = 30 * time.Minute

// runTranscribe is the seam over the real ASR exec. It runs
//
//	becky-transcribe <videoPath> --format srt --output <srtOut>
//
// writing the subtitle sidecar to srtOut (which is beside the source video). It
// defaults to the real exec.CommandContext; tests override it with a fake that
// writes a canned .srt so the whole transcribe→re-index flow is exercised
// offline. Production never reassigns it.
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

// transcribeExeName is the becky-transcribe binary's filename for the host OS
// (.exe on Windows).
func transcribeExeName() string {
	if isWindows() {
		return "becky-transcribe.exe"
	}
	return "becky-transcribe"
}

// srtSidecarPath returns the subtitle sidecar path for a video: <stem>.srt in the
// SAME directory as the source (separator-safe). This is the canonical sidecar
// internal/sidecar.FindSubtitle resolves on the next index, so a fresh transcript
// is immediately picked up as HasTranscript.
func srtSidecarPath(videoPath string) string {
	dir := filepath.Dir(videoPath)
	base := filepath.Base(videoPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = base
	}
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

// transcribeOne runs ASR on one indexed video and writes its <stem>.srt sidecar
// beside the source. It is the shared core of Transcribe + TranscribeAll: resolve
// the binary, run the (seamed) exec, and confirm the sidecar was produced. It does
// NOT re-index — the caller re-indexes once after a batch.
func (a *App) transcribeOne(transcribeBin string, v footage.Video) error {
	srtOut := srtSidecarPath(v.Path)
	ctx, cancel := transcribeContext(context.Background())
	defer cancel()
	if err := runTranscribe(ctx, transcribeBin, v.Path, srtOut); err != nil {
		return err
	}
	if !fileExists(srtOut) {
		return fmt.Errorf("becky-transcribe produced no transcript at %s", srtOut)
	}
	return nil
}

// Transcribe runs the real local ASR (becky-transcribe) on the video named name
// (basename) in the open folder, writes the subtitle sidecar (<stem>.srt) NEXT TO
// the source video, then re-indexes the open folder and returns the fresh
// FolderView (so the UI sees has_transcript flip to true). Re-transcribing a video
// that already has a transcript is allowed — it overwrites the sidecar. This is
// synchronous and long-running (the GUI shows a spinner); the exec is bounded by a
// generous timeout. The original video is never modified — only the .srt sidecar.
func (a *App) Transcribe(name string) (FolderView, error) {
	v, ok := a.lookupVideo(name)
	if !ok {
		return FolderView{}, fmt.Errorf("no such video in folder: %s", name)
	}
	bin, err := resolveTranscribeBin()
	if err != nil {
		return FolderView{}, err
	}
	if err := a.transcribeOne(bin, v); err != nil {
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

// TranscribeAll transcribes every indexed video that lacks a transcript, writing
// each <stem>.srt sidecar beside its source, then re-indexes once and returns the
// fresh FolderView with counts. Degrade-never-crash: one video's failure is
// recorded in Errors and the batch continues; a missing becky-transcribe binary is
// a single clear error before any work. Videos that already have a transcript are
// skipped (use per-video Transcribe to force a re-transcribe).
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
		if err := a.transcribeOne(bin, v); err != nil {
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
