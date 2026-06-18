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
	case "open_folder":
		return a.OpenFolder(argString(args, "folder"))
	case "transcript":
		return a.Transcript(argString(args, "name"))
	case "search":
		return a.Search(argString(args, "query")), nil
	case "media_url":
		// Resolve a source (or proxy) to a /media URL the <video> can load.
		return a.mediaURLReply(argString(args, "source"))

	// ---- timeline mutation ----
	case "add_clip":
		return a.AddClip(argString(args, "source"), argFloat(args, "in"), argFloat(args, "out"), argString(args, "label"))
	case "remove_clip":
		return a.RemoveClip(argString(args, "id"))
	case "reorder":
		return a.Reorder(argString(args, "id"), argInt(args, "to"))
	case "set_trim":
		return a.SetTrim(argString(args, "id"), argFloat(args, "in"), argFloat(args, "out"))
	case "set_label":
		return a.SetLabel(argString(args, "id"), argString(args, "text"))
	case "set_overlay":
		return a.SetOverlay(argString(args, "field"), argBool(args, "value"), argString(args, "position"))
	case "add_marker":
		return a.AddMarker(argFloat(args, "at"), argString(args, "label")), nil
	case "timeline":
		return a.Timeline(), nil

	// ---- save / load ----
	case "save_reel":
		path, err := a.SaveReel(argString(args, "path"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": path}, nil
	case "load_reel":
		return a.LoadReel(argString(args, "path"))

	// ---- render / export (new files) ----
	case "export":
		return a.ExportReel(argString(args, "output"))
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

	// ---- Underlord assistant ----
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

// askReply runs an Underlord turn with a per-turn deadline and returns the
// Proposal. A backend hang can't wedge the UI — the context bounds it.
func (a *App) askReply(utterance string) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
