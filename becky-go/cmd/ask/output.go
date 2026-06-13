// output.go — write each tool's result to disk NEXT TO THE INPUT, deterministically,
// and never touch the source. This is the behavior a human dragging a file expects:
//
//	video.mp4  --transcribe-->  video.srt          (same folder, same name, new ext)
//	video.mp4  --diarize---->   video.diarize.json
//	video.mp4  --identify--->   video.identify.json
//	video.mp4  --describe--->   video.describe.json
//	frames\    --ocr-------->   frames.ocr.json
//
// Derived MEDIA (a cut/edit) keeps the source untouched and writes a descriptor
// copy (video_jumpcut.mp4) — handled by the tool itself, so we never auto-save over
// a media file here. The hard rule: we ONLY ever write a NEW sidecar; we refuse to
// write to any media extension, so a source video can never be clobbered by this path.
//
// Pure, table-driven, and unit-tested headless (output_test.go) so the file-naming
// rule is verifiable without a terminal or a real tool run.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// opOutputExt maps an action to the sidecar extension it should write, and whether
// it produces a savable stdout artifact at all. transcribe → a real .srt (human
// readable); the JSON tools → a .<op>.json sidecar; cut produces media via the tool
// itself, so it is NOT auto-saved here (save=false).
func opOutputExt(op actionID) (ext string, save bool) {
	switch op {
	case actTranscribe:
		return ".srt", true
	case actDiarize:
		return ".diarize.json", true
	case actIdentify:
		return ".identify.json", true
	case actDescribe:
		return ".describe.json", true
	case actOCR:
		return ".ocr.json", true
	default: // actCut and anything else: the tool writes its own media; no sidecar.
		return "", false
	}
}

// outputPathFor returns the deterministic sidecar path for an input + op, and
// whether anything should be saved. The path is <dir>/<base-without-ext><ext> — the
// input's own folder, the input's own name, a new extension.
func outputPathFor(input string, op actionID) (path string, save bool) {
	ext, save := opOutputExt(op)
	if !save || strings.TrimSpace(input) == "" {
		return "", false
	}
	base := strings.TrimSuffix(input, filepath.Ext(input))
	return base + ext, true
}

// opForTool maps a becky-<tool> command name back to the action id, so the save
// layer works whether the run came from a quick-action button or a typed request
// (which produces a raw command). Returns ok=false for tools we don't auto-save.
func opForTool(cmd0 string) (actionID, bool) {
	switch strings.TrimPrefix(cmd0, "becky-") {
	case "transcribe":
		return actTranscribe, true
	case "diarize":
		return actDiarize, true
	case "identify":
		return actIdentify, true
	case "validate":
		return actDescribe, true
	case "ocr":
		return actOCR, true
	case "cut":
		return actCut, true
	}
	return "", false
}

// execArgs returns the argv to actually RUN for a built command. It is the only
// place the executed command differs from the displayed/asserted builder output:
// transcribe is run with `--format srt` so the saved sidecar is a real .srt (the
// builder stays `becky-transcribe <path>` for the assertable quick-action mapping).
func execArgs(cmd []string) []string {
	if len(cmd) == 0 {
		return cmd
	}
	if op, ok := opForTool(cmd[0]); ok && op == actTranscribe {
		if !containsFlag(cmd, "--format") {
			out := make([]string, 0, len(cmd)+2)
			out = append(out, cmd...)
			out = append(out, "--format", "srt")
			return out
		}
	}
	return cmd
}

func containsFlag(cmd []string, flag string) bool {
	for _, c := range cmd {
		if c == flag {
			return true
		}
	}
	return false
}

// saveOutput writes tool stdout to a sidecar path and returns the path actually
// written. Safety rails, in order:
//   - never an empty path;
//   - NEVER a media extension (so a source video/audio can never be overwritten by
//     this path — derived media is the tool's job, with its own descriptor name);
//   - NEVER clobber an EXISTING sidecar — a `video.srt` next to the clip may be a
//     human-VERIFIED transcript; if the target already exists we write
//     `video.becky.srt` instead and report it, so verified work is never destroyed;
//   - the parent dir already exists (it's the input's own folder).
func saveOutput(path, content string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("empty output path")
	}
	ext := filepath.Ext(path)
	if isMediaExt(ext) {
		return "", fmt.Errorf("refusing to write a media extension (%s) — sidecars only", ext)
	}
	final := path
	if _, err := os.Stat(path); err == nil {
		// Something already lives here (possibly a verified file). Do not overwrite —
		// write becky's own copy alongside it instead.
		final = strings.TrimSuffix(path, ext) + ".becky" + ext
	}
	if err := os.WriteFile(final, []byte(content), 0o644); err != nil {
		return "", err
	}
	return final, nil
}

// runAndSave runs ONE built command (augmented by execArgs) and, on success, writes
// its stdout to the deterministic sidecar next to the input. The returned runResult
// carries Saved notes (a saved path, or a human-readable reason it wasn't saved) so
// the chat can report exactly what landed on disk.
func runAndSave(ctx context.Context, target Target, cmd []string) runResult {
	res := runCommand(ctx, execArgs(cmd))
	res.Command = cmd // show the clean builder command, not the --format augmentation
	if res.Err != nil {
		return res
	}
	op, ok := opForTool(cmd[0])
	if !ok {
		return res
	}
	path, save := outputPathFor(target.Primary(), op)
	if !save {
		return res // e.g. cut: the tool wrote its own media; nothing to sidecar
	}
	written, err := saveOutput(path, res.Stdout)
	if err != nil {
		res.Saved = append(res.Saved, fmt.Sprintf("(could not save %s: %v)", path, err))
		return res
	}
	if written != path {
		res.Saved = append(res.Saved,
			fmt.Sprintf("%s  (kept your existing %s untouched)", written, filepath.Base(path)))
	} else {
		res.Saved = append(res.Saved, written)
	}
	return res
}

// runOps runs a list of actions against the target in sequence, saving each, and
// folds them into ONE result for the chat. Used by both the single quick-action
// keypress and the multi-op typed request ("transcribe and diarize"). One op
// failing does not abort the rest; every outcome is reported.
func runOps(ctx context.Context, target Target, ops []actionID) runResult {
	var combined runResult
	var blocks []string
	var errs []string
	for _, op := range ops {
		a, ok := actionByID(op)
		if !ok {
			continue
		}
		cmd := commandFor(a, target)
		if cmd == nil {
			combined.Saved = append(combined.Saved,
				fmt.Sprintf("(%s doesn't fit this target — skipped)", a.Label))
			continue
		}
		if combined.Command == nil {
			combined.Command = cmd // representative headline for the chat
		}
		res := runAndSave(ctx, target, cmd)
		combined.Saved = append(combined.Saved, res.Saved...)
		if res.Err != nil {
			errs = append(errs, res.Err.Error())
			continue
		}
		if out := strings.TrimSpace(res.Stdout); out != "" {
			blocks = append(blocks, commandString(res.Command)+"\n"+out)
		}
	}
	combined.Stdout = strings.Join(blocks, "\n\n")
	if len(errs) > 0 {
		combined.Err = errors.New(strings.Join(errs, " | "))
	}
	return combined
}

// parseRunSelection turns a typed line into an ordered, de-duplicated list of
// actions to run — so a human can type "1,2" or "transcribe and diarize" to run
// several at once. It only fires when the line is UNAMBIGUOUSLY a run request:
// it returns ops only when at least one number token is present OR two or more op
// words are named. A single bare op word (e.g. "transcribe this") returns nil so
// the normal act-vs-discuss router (with its y/n confirm) still handles it.
func parseRunSelection(line string, actions []quickAction) []actionID {
	if len(actions) == 0 {
		return nil
	}
	fields := strings.FieldsFunc(strings.ToLower(line), func(r rune) bool {
		return r == ' ' || r == '\t' || r == ',' || r == '+' || r == '/' || r == '&'
	})
	var ops []actionID
	seen := map[actionID]bool{}
	numbers := 0
	words := 0
	add := func(id actionID) {
		for _, a := range actions { // only offer ops valid for THIS target
			if a.ID == id && !seen[id] {
				seen[id] = true
				ops = append(ops, id)
				return
			}
		}
	}
	for _, f := range fields {
		switch f {
		case "and", "then", "also", "please", "the", "it", "this", "that", "run":
			continue
		}
		if len(f) == 1 && f[0] >= '1' && f[0] <= '9' {
			idx := int(f[0] - '1')
			if idx < len(actions) {
				numbers++
				add(actions[idx].ID)
			}
			continue
		}
		if id, ok := opWord(f); ok {
			words++
			add(id)
		}
	}
	if numbers == 0 && words < 2 {
		return nil // ambiguous/single — let the normal router handle it
	}
	return ops
}

// opWord maps a plain word (and a couple of synonyms) to an action id.
func opWord(w string) (actionID, bool) {
	switch w {
	case "transcribe", "transcript", "transcription", "srt":
		return actTranscribe, true
	case "diarize", "diarise", "speakers", "diarization":
		return actDiarize, true
	case "identify", "id", "who", "identity":
		return actIdentify, true
	case "describe", "description", "validate", "scene":
		return actDescribe, true
	case "ocr", "text":
		return actOCR, true
	case "cut", "jumpcut", "trim":
		return actCut, true
	}
	return "", false
}
