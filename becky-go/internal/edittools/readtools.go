package edittools

import (
	"becky-go/internal/editmodel"
)

// readTools are the verbs that READ/PRODUCE rather than mutate the timeline:
// preview a clip, grab a still, run vision over a frame, render the compilation,
// search transcripts, find quotes. They do not change Project state (mutating=
// false), so they never bump Rev. Each EMITS an abstract HostCommand; the bridge
// (cmd/becky-edit) runs the real ffmpeg/avlm/search/render behind it. The
// vision/search/find_quotes/render Data is enriched by the bridge with the real
// output before it goes back to the agent loop.
func readTools() []toolDef {
	return []toolDef{
		{
			verb: "preview_clip", category: "render", mutating: false,
			summary: "Open a clip (by id) or a raw source span in the preview and play it. This is the single-click-a-quote behaviour.",
			params: []ParamSpec{
				{"id", "string", false, "clip id to preview"},
				{"source", "string", false, "source path (if no id)"},
				{"in", "number", false, "in-point seconds (with source)"},
				{"out", "number", false, "out-point seconds (with source)"},
			},
			apply: applyPreviewClip,
		},
		{
			verb: "grab_frame", category: "render", mutating: false,
			summary: "Grab one still frame to the work dir (forensic evidence still). By id (its in-point), or source+at, or the current playhead.",
			params: []ParamSpec{
				{"id", "string", false, "clip id (grabs its in-point)"},
				{"source", "string", false, "source path (with at)"},
				{"at", "number", false, "seconds into the source"},
			},
			apply: applyGrabFrame,
		},
		{
			verb: "vision", category: "vision", mutating: false,
			summary: "Ask the built-in multimodal model (Gemma-4) what is happening in a clip or at a frame. Use to verify who/what is on screen before deciding the next edit.",
			params: []ParamSpec{
				{"question", "string", true, "what to ask about the frame/clip"},
				{"id", "string", false, "clip id to look at"},
				{"source", "string", false, "source path (if no id)"},
				{"in", "number", false, "in-point seconds (with source)"},
				{"out", "number", false, "out-point seconds (with source)"},
			},
			apply: applyVision,
		},
		{
			verb: "render", category: "render", mutating: false,
			summary: "Render the current compilation to an MP4 (forensic export). Writes to the case folder's render/ subdir.",
			params: []ParamSpec{
				{"out", "string", false, "output file path (default render/<name>.mp4)"},
				{"overlay", "bool", false, "burn the forensic lower-third"},
			},
			apply: applyRender,
		},
		{
			verb: "search", category: "search", mutating: false,
			summary: "Search the case folder's transcripts for a phrase. Returns timestamped quote hits.",
			params: []ParamSpec{
				{"query", "string", true, "the phrase/keywords to search for"},
				{"mode", "string", false, "keyword | semantic | hybrid (default hybrid)"},
			},
			apply: applySearch,
		},
		{
			verb: "find_quotes", category: "search", mutating: false,
			summary: "Find the important passages in a transcript by criteria (escalates to the AI quote picker).",
			params: []ParamSpec{
				{"criteria", "string", false, "what makes a quote important, plain English"},
				{"srt", "string", false, "a specific transcript path to scan"},
			},
			apply: applyFindQuotes,
		},
	}
}

func applyPreviewClip(p *editmodel.Project, a Args) (Result, []HostCommand) {
	src, in, out, res, ok := resolveSpan(p, a)
	if !ok {
		return res, nil
	}
	host := HostCommand{Name: "player.open_seek_play", Args: map[string]any{
		"source": src, "in": in, "out": out,
		"frame_in": frame(in, p.FPS), "frame_out": frame(out, p.FPS),
	}}
	return okHost("previewing "+baseName(src)+" ["+f1(in)+"-"+f1(out)+"]", map[string]any{"source": src, "in": in, "out": out}, host)
}

func applyGrabFrame(p *editmodel.Project, a Args) (Result, []HostCommand) {
	args := map[string]any{}
	desc := "current playhead"
	if id, has := argString(a, "id"); has && id != "" {
		_, _, clip, found := p.FindClip(id)
		if !found {
			return failR("no clip %q", id)
		}
		args["source"] = clip.Source
		args["at"] = clip.In
		desc = baseName(clip.Source) + " @ " + f1(clip.In) + "s"
	} else if src, has := argString(a, "source"); has && src != "" {
		at, _ := argFloat(a, "at")
		args["source"] = src
		args["at"] = at
		desc = baseName(src) + " @ " + f1(at) + "s"
	}
	host := HostCommand{Name: "player.grab_frame", Args: args}
	return okHost("grabbed a still ("+desc+")", nil, host)
}

func applyVision(p *editmodel.Project, a Args) (Result, []HostCommand) {
	question, _ := argString(a, "question")
	src, in, out, res, ok := resolveSpan(p, a)
	if !ok {
		return res, nil
	}
	host := HostCommand{Name: "vision.analyze", Args: map[string]any{
		"source": src, "in": in, "out": out, "question": question,
	}}
	// The bridge runs internal/avlm (Gemma-4) and replaces Data["answer"].
	return okHost("vision query queued on "+baseName(src), map[string]any{"question": question, "source": src, "in": in, "out": out}, host)
}

func applyRender(p *editmodel.Project, a Args) (Result, []HostCommand) {
	if p.ClipCount() == 0 {
		return failR("nothing to render — the timeline is empty")
	}
	out, _ := argString(a, "out")
	overlay, hasOverlay := argBool(a, "overlay")
	args := map[string]any{"clips": p.ToReel().Clips}
	if out != "" {
		args["out"] = out
	}
	if hasOverlay {
		args["overlay"] = overlay
	}
	host := HostCommand{Name: "render.export", Args: args}
	r := p.ToReel()
	return okHost("render queued ("+itoa(len(r.Clips))+" clips, "+f1(r.Duration())+"s)", map[string]any{"clips": len(r.Clips), "duration": r.Duration()}, host)
}

func applySearch(p *editmodel.Project, a Args) (Result, []HostCommand) {
	query, _ := argString(a, "query")
	mode, has := argString(a, "mode")
	if !has || mode == "" {
		mode = "hybrid"
	}
	host := HostCommand{Name: "search", Args: map[string]any{"query": query, "mode": mode, "folder": p.Folder}}
	return okHost("searching transcripts for "+quote(query), map[string]any{"query": query, "mode": mode}, host)
}

func applyFindQuotes(p *editmodel.Project, a Args) (Result, []HostCommand) {
	criteria, _ := argString(a, "criteria")
	srt, _ := argString(a, "srt")
	args := map[string]any{"folder": p.Folder}
	if criteria != "" {
		args["criteria"] = criteria
	}
	if srt != "" {
		args["srt"] = srt
	}
	host := HostCommand{Name: "find_quotes", Args: args}
	return okHost("finding quotes", map[string]any{"criteria": criteria}, host)
}

// resolveSpan resolves a (source,in,out) from either a clip id or explicit args.
// On failure it returns a populated Result (ok=false) so the caller can return it.
func resolveSpan(p *editmodel.Project, a Args) (src string, in, out float64, res Result, ok bool) {
	if id, has := argString(a, "id"); has && id != "" {
		_, _, clip, found := p.FindClip(id)
		if !found {
			return "", 0, 0, fail("no clip %q", id), false
		}
		return clip.Source, clip.In, clip.Out, Result{OK: true}, true
	}
	src, hasSrc := argString(a, "source")
	if !hasSrc || src == "" {
		return "", 0, 0, fail("need a clip id or a source path"), false
	}
	in, _ = argFloat(a, "in")
	out, hasOut := argFloat(a, "out")
	if !hasOut || out <= in {
		// Default a short 10s window when only an in-point is given.
		out = in + 10
	}
	return src, in, out, Result{OK: true}, true
}

// baseName returns the last path element (handles both / and \ separators).
func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

func quote(s string) string { return "\"" + s + "\"" }
