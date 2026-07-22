package main

// bridge.go is the ONE control surface between the WebView2 page and the Go
// backend (SPEC-BECKY-CLIP §9): JS calls window.beckyCall(verb, argsJSON) and
// gets back a JSON envelope. Every verb maps to exactly one App method — a
// default-deny dispatch table, so the page can never invoke anything outside this
// allowlist. Pure data-in/data-out (no window dependency), so it is unit-tested
// directly with a faked App folder.
//
// Envelope: the reply is always {ok, data, error}. ok=false carries a
// plain-language error the chat/status line shows; ok=true carries the verb's
// typed result under data. This keeps the JS side trivial: call, check ok, render.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// callReply is the uniform JSON envelope returned to JS for every beckyCall.
type callReply struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// Call is the bound function the page invokes as window.beckyCall(verb, argsJSON).
// argsJSON is a JSON object string (or "" for none). It dispatches verb to the
// matching App method and returns a marshalled callReply string. It never panics:
// an unknown verb, a bad args payload, or a handler error all become ok=false
// with a readable message.
func (a *App) Call(verb string, argsJSON string) string {
	args, err := decodeArgs(argsJSON)
	if err != nil {
		return encodeReply(callReply{OK: false, Error: "bad args: " + err.Error()})
	}
	data, err := a.dispatch(verb, args)
	if err != nil {
		return encodeReply(callReply{OK: false, Error: err.Error()})
	}
	return encodeReply(callReply{OK: true, Data: data})
}

// dispatch is the default-deny verb table. Each case reads typed args and calls
// one App method. Returns the verb's result (marshalled into Data) or an error.
func (a *App) dispatch(verb string, args map[string]any) (any, error) {
	switch verb {
	// ---- folder + transcript + search (read) ----
	case "pick_folder":
		// Native OS folder dialog → index the chosen folder (no-op if cancelled).
		return a.PickFolder()
	case "open_folder":
		return a.OpenFolder(argString(args, "folder"))
	case "transcript":
		return a.Transcript(argString(args, "name"))
	case "search":
		return a.Search(argString(args, "query")), nil
	case "qmd_search":
		// Smart (hybrid BM25+vector) transcript search via the local qmd engine, with a
		// keyword fallback. Resolves each hit to the precise .srt cue. {results,mode,note}.
		return a.QmdSearch(argString(args, "query")), nil
	case "media_url":
		// Resolve a source (or proxy) to a /media URL the <video> can load.
		return a.mediaURLReply(argString(args, "source"))
	case "probe":
		// Report a source's true duration (seconds) so the UI can clamp timeline
		// trim/extend. Degrades to {duration:0} when not probe-able (no ffprobe).
		return a.Probe(argString(args, "source")), nil
	case "thumb":
		// Tiny CACHED first-frame thumbnail (base64 data: URI) for a timeline clip.
		// Degrades to {data:""} when not grab-able (no ffmpeg) — never an error.
		return a.Thumb(argString(args, "source"), argFloat(args, "t")), nil
	case "scrub_segment":
		// Windowed scrub proxy for ONLY a timeline clip's [in,out) span (cheaper
		// than a whole-file scrub proxy for a long source with one short clip).
		// Degrades to {path:""} when ffmpeg is absent / source unresolved.
		return a.ScrubSegment(argString(args, "source"), argFloat(args, "in"), argFloat(args, "out")), nil
	case "peaks":
		// Normalized 0..1 waveform amplitude buckets for ONLY a clip's [in,out)
		// window, for drawing a per-clip waveform on the timeline track.
		// Degrades to {peaks:[],count:0} when ffmpeg/audio is unavailable.
		return a.Peaks(argString(args, "source"), argFloat(args, "in"), argFloat(args, "out"), argInt(args, "buckets")), nil
	case "peaks2":
		// ACCURATE waveform: true min/max per column at ABSOLUTE scale
		// (1.0 = digital full scale), backed by the shared .bpk peak cache
		// the native timeline also uses. Degrades to {min:[],max:[],count:0}.
		return a.Peaks2(argString(args, "source"), argFloat(args, "in"), argFloat(args, "out"), argInt(args, "columns")), nil
	case "autocut_silence":
		// Run becky-cut's existing silence/VAD detector (dry-run: decide only,
		// never render) and return its KEEP segments as {in,out} seconds, ready
		// to feed into add_clip for each span. Degrades to {segments:[],note}.
		return a.AutoCutSilence(argString(args, "name")), nil
	case "timeline_edl":
		// Write an mpv EDL of the whole timeline so the UI can play it as ONE
		// seamless (gapless) virtual source. Returns {path,duration}; "" if empty.
		return a.TimelineEDL()

	// ---- transcription (caption pipeline: official-first, local fallback) ----
	case "transcribe":
		// Run the caption sequence on one video: prefer a complete official .srt
		// (present or fetched via becky-captions); else local ASR to
		// <stem>_parakeet_transcription.srt (never overwriting an original) →
		// re-index. A video that already has a transcript forces a fresh local pass
		// (the "↻ re-transcribe" intent). Long-running (the GUI shows a spinner).
		return a.Transcribe(argString(args, "name"))
	case "transcribe_all":
		// Transcribe every indexed video lacking a transcript (degrade per video).
		return a.TranscribeAll()
	case "reindex":
		// Re-walk the open folder after external changes (no-op if none open).
		return a.Reindex(), nil

	// ---- timeline mutation ----
	case "add_clip":
		// Optional "at": insert index (a clip added at the playhead lands after that clip);
		// omitted -> append.
		return a.AddClipAt(argString(args, "source"), argFloat(args, "in"), argFloat(args, "out"), argString(args, "label"), argIntDefault(args, "at", -1))
	case "add_external":
		// Add a whole video dragged in from OUTSIDE the case folder (item 21): authorize
		// the exact file, probe its duration, insert it (optional "at", else append).
		return a.AddExternalClip(argString(args, "path"), argIntDefault(args, "at", -1))
	case "remove_clip":
		return a.RemoveClip(argString(args, "id"))
	case "reorder":
		return a.Reorder(argString(args, "id"), argInt(args, "to"))
	case "reorder_many":
		// Move a SET of clips as one block (dragging a multi-selection), one undoable edit.
		return a.ReorderMany(argStringSlice(args, "ids"), argInt(args, "to"))
	case "set_clips":
		// Replace the whole clip list (the "trim to the loud parts" action), one undoable edit.
		return a.SetClips(argClipSpecs(args, "clips"))
	case "set_trim":
		return a.SetTrim(argString(args, "id"), argFloat(args, "in"), argFloat(args, "out"))
	case "split":
		// Cut one clip into two at a source time, as ONE undoable edit (so Ctrl+Z
		// reverses the whole split at once). {timeline, new_id}.
		tl, newID, err := a.Split(argString(args, "id"), argFloat(args, "at"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"timeline": tl, "new_id": newID}, nil
	case "set_label":
		return a.SetLabel(argString(args, "id"), argString(args, "text"))
	case "set_overlay":
		return a.SetOverlay(argString(args, "field"), argBool(args, "value"), argString(args, "position"))
	case "add_marker":
		return a.AddMarker(argFloat(args, "at"), argString(args, "label")), nil
	case "timeline":
		return a.Timeline(), nil
	case "undo":
		// Revert the last clip edit. changed=false ⇒ nothing to undo (UI no-ops quietly).
		tl, changed := a.Undo()
		return map[string]any{"timeline": tl, "changed": changed}, nil
	case "redo":
		tl, changed := a.Redo()
		return map[string]any{"timeline": tl, "changed": changed}, nil

	// ---- H-1 shared state (UI → engine telemetry) ----
	// The C++ app fires these from worker threads on every scrub, selection
	// change and threshold drag. Before they existed here, all three fell
	// through to default: → ok:false and the reply was discarded — so the
	// engine (and through it the AI) never learned the playhead, selection or
	// threshold. Telemetry only: none of them mutate the reel or the undo
	// history; they feed assistant.Context.Timeline so "this clip"/"here"
	// resolve against where Jordan actually is.
	case "seek":
		// "quiet" marks a scrub echo vs a deliberate seek; the stored state is
		// the same either way, so it is accepted and ignored.
		return a.SetPlayhead(argFloat(args, "t")), nil
	case "set_select":
		return a.SetSelection(argStringSlice(args, "ids")), nil
	case "set_threshold":
		return a.SetThreshold(argBool(args, "on"), argFloat(args, "level")), nil

	// ---- save / load ----
	case "save_reel":
		path, err := a.SaveReel(argString(args, "path"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": path}, nil
	case "load_reel":
		return a.LoadReel(argString(args, "path"))

	// ---- human-review Q&A (questions.go) ----
	case "questions":
		return map[string]any{"questions": a.Questions()}, nil
	case "save_answer":
		qs, err := a.SaveAnswer(argString(args, "id"), argString(args, "question"), argString(args, "answer"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"questions": qs}, nil

	// ---- render / export (new files) ----
	case "export":
		return a.ExportReel(argString(args, "output"))
	case "export_selection":
		// Render only the selected clips (their IDs) to a separate compilation MP4.
		return a.ExportSelection(argStringSlice(args, "ids"), argString(args, "output"))
	case "write_edl":
		path, err := a.WriteEDLOnly(argString(args, "output"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": path}, nil
	case "write_srt":
		path, err := a.WriteSRTOnly(argString(args, "output"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": path}, nil
	case "grab_frame":
		path, err := a.GrabFrame(argString(args, "source"), argFloat(args, "t"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": path, "url": a.frameURL(path)}, nil

	// ---- becky assistant ----
	case "status":
		// Which AI backend is powering the chat (claude CLI / API key / local) +
		// the online toggle — so the UI can SHOW the user it's really on Claude.
		return a.BeckyStatus(), nil
	case "set_online":
		a.SetOnline(argBool(args, "on"))
		return map[string]any{"online": argBool(args, "on")}, nil
	case "ask":
		return a.askReply(argString(args, "utterance"))
	case "apply_proposal":
		tl, execs, err := a.ApplyProposal(argString(args, "id"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"timeline": tl, "exec_commands": execs}, nil
	case "reject_proposal":
		a.RejectProposal(argString(args, "id"))
		return map[string]any{"rejected": argString(args, "id")}, nil
	case "forensic_query":
		// H-7: the forensic path in-app. Runs qmd recall + the becky-judge
		// LLM pass + becky-hits against the OPEN folder and lands the
		// resulting reel on the timeline as one undo span. Long-running —
		// H-5 events narrate progress in the activity panel. Optional
		// rubric/aliases override the case guide; a bare {query} resolves
		// the guide from the case folder / BECKY_JUDGE_GUIDE / the wiki.
		return a.ForensicQuery(argString(args, "query"), argString(args, "rubric"), argString(args, "aliases"))
	case "apply_edit_batch":
		// H-4: a list of existing edit ops (add_clip/remove_clip/reorder/
		// set_trim/split/set_label) applied as ONE atomic undo span — Ctrl+Z
		// reverses the whole batch in one press. A malformed op is skipped
		// and reported in "results", never a crash.
		tl, results, err := a.ApplyEditBatch(argEditOps(args, "ops"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"timeline": tl, "results": results}, nil

	default:
		return nil, fmt.Errorf("unknown command %q", verb)
	}
}

// mediaURLReply resolves a source to a playable /media URL. If the source codec
// is web-safe it returns the original; otherwise it asks the engine for a proxy
// (best-effort: if ffmpeg is absent it falls back to the original URL and notes
// it, so preview still attempts to play rather than hard-failing).
func (a *App) mediaURLReply(source string) (any, error) {
	if _, ok := a.resolveSource(source); !ok {
		return nil, fmt.Errorf("source is not in the open folder: %s", source)
	}
	playable := source
	note := ""
	if proxy, err := a.ProxyFor(source); err == nil {
		playable = proxy
	} else {
		note = "preview uses the original (proxy unavailable: " + firstLine(err) + ")"
	}
	return map[string]any{"url": a.mediaURL(playable), "note": note}, nil
}

// askReply runs a becky turn with a per-turn deadline and returns the
// Proposal. A backend hang can't wedge the UI — the context bounds it.
func (a *App) askReply(utterance string) (any, error) {
	// Generous: an agentic "investigate my notes/vault" turn reads files over
	// several steps (~1-2 min). The async bridge keeps the window responsive
	// throughout (a "thinking…" spinner shows), so a longer cap costs nothing.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	p, err := a.Ask(ctx, utterance)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ---- envelope helpers -----------------------------------------------------

// decodeArgs parses the JS args payload. "" / "null" → empty map (a verb with no
// args). A non-object payload is an error.
func decodeArgs(argsJSON string) (map[string]any, error) {
	s := argsJSON
	if s == "" || s == "null" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	if m == nil {
		return map[string]any{}, nil
	}
	return m, nil
}

// encodeReply marshals a callReply to a JSON string (never errors in practice;
// on the impossible marshal failure it returns a hand-built error envelope).
func encodeReply(r callReply) string {
	b, err := json.Marshal(r)
	if err != nil {
		return `{"ok":false,"error":"internal: reply encode failed"}`
	}
	return string(b)
}

// argString reads a string arg from the decoded map (numbers/bools coerced).
func argString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	case bool:
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// argFloat reads a float arg (JSON number or numeric string). Missing → 0.
func argFloat(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return t
	case string:
		return tcOrSeconds(t) // tolerate "12.4" or a timecode string
	default:
		return 0
	}
}

// argInt reads an int arg (JSON number or numeric string). Missing → 0.
func argInt(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		return atoiSafe(t)
	default:
		return 0
	}
}

// argIntDefault reads an int arg, returning def when the key is ABSENT (so a caller can
// distinguish "not given" from an explicit 0 — e.g. an insert index where -1 = append).
func argIntDefault(m map[string]any, key string, def int) int {
	if _, ok := m[key]; !ok {
		return def
	}
	return argInt(m, key)
}

// argBool reads a bool arg (JSON bool, or a truthy string/number). Missing →
// false.
func argBool(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return truthy(t)
	case float64:
		return t != 0
	default:
		return false
	}
}

// argStringSlice reads a string-array arg (a JSON array of strings, e.g. clip IDs).
// Missing / non-array / non-string elements are skipped, yielding a (possibly
// empty) slice — never an error, so a malformed payload degrades to "no ids".
func argStringSlice(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// argClipSpecs parses a JSON array of {source,in,out,label} objects (the "trim to the
// loud parts" clip list). Missing / malformed entries are skipped, never an error.
func argClipSpecs(m map[string]any, key string) []ClipSpec {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]ClipSpec, 0, len(arr))
	for _, e := range arr {
		o, ok := e.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, ClipSpec{
			Source: argString(o, "source"),
			In:     argFloat(o, "in"),
			Out:    argFloat(o, "out"),
			Label:  argString(o, "label"),
		})
	}
	return out
}

// argEditOps parses an apply_edit_batch payload: a JSON array of
// {verb,args} objects. A malformed entry (missing verb, non-object) is
// skipped, never an error — H-2's "malformed op = logged + ignored" applies
// at parse time too, before ApplyEditBatch even sees the op.
func argEditOps(m map[string]any, key string) []EditOp {
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]EditOp, 0, len(arr))
	for _, e := range arr {
		o, ok := e.(map[string]any)
		if !ok {
			continue
		}
		verb := argString(o, "verb")
		if verb == "" {
			continue
		}
		opArgs, _ := o["args"].(map[string]any)
		out = append(out, EditOp{Verb: verb, Args: opArgs})
	}
	return out
}

// firstLine returns the first line of an error message (for compact notes).
func firstLine(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
