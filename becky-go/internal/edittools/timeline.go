package edittools

import (
	"math"
	"strconv"

	"becky-go/internal/editmodel"
)

// timelineTools are the clip-arrangement verbs: add/remove/move/trim/split/
// ripple-delete clips and add tracks. All mutate the timeline → mutating=true.
func timelineTools() []toolDef {
	return []toolDef{
		{
			verb: "add_clip", category: "timeline", mutating: true,
			summary: "Append a clip (a span of a source video) to a timeline track. Defaults to the end of track 0.",
			params: []ParamSpec{
				{"source", "string", true, "absolute path to the source video"},
				{"in", "number", true, "in-point in seconds into the source"},
				{"out", "number", true, "out-point in seconds into the source (must be > in)"},
				{"track", "number", false, "track index (default 0)"},
				{"pos", "number", false, "timeline position in seconds (default: end of the track)"},
				{"label", "string", false, "clip label, e.g. the quote text"},
			},
			apply: applyAddClip,
		},
		{
			verb: "remove_clip", category: "timeline", mutating: true,
			summary: "Remove a clip from the timeline by id (leaves a gap; use ripple_delete to close it).",
			params:  []ParamSpec{{"id", "string", true, "clip id, e.g. c2"}},
			apply:   applyRemoveClip,
		},
		{
			verb: "ripple_delete", category: "timeline", mutating: true,
			summary: "Remove a clip and slide every later clip on its track left to close the gap.",
			params:  []ParamSpec{{"id", "string", true, "clip id"}},
			apply:   applyRippleDelete,
		},
		{
			verb: "move_clip", category: "timeline", mutating: true,
			summary: "Move a clip to a new timeline position and/or a different track.",
			params: []ParamSpec{
				{"id", "string", true, "clip id"},
				{"pos", "number", false, "new timeline position in seconds"},
				{"track", "number", false, "new track index"},
			},
			apply: applyMoveClip,
		},
		{
			verb: "trim_clip", category: "timeline", mutating: true,
			summary: "Change a clip's source in/out points (its length on the timeline) without moving it.",
			params: []ParamSpec{
				{"id", "string", true, "clip id"},
				{"in", "number", false, "new source in-point in seconds"},
				{"out", "number", false, "new source out-point in seconds"},
			},
			apply: applyTrimClip,
		},
		{
			verb: "split_clip", category: "timeline", mutating: true,
			summary: "Split a clip into two at a timeline position; the right half becomes a new clip.",
			params: []ParamSpec{
				{"id", "string", true, "clip id"},
				{"at", "number", true, "timeline position in seconds, strictly inside the clip"},
			},
			apply: applySplitClip,
		},
		{
			verb: "add_track", category: "timeline", mutating: true,
			summary: "Add a new video or audio track.",
			params: []ParamSpec{
				{"kind", "string", true, "\"video\" or \"audio\""},
				{"name", "string", false, "track name (default V<n>/A<n>)"},
			},
			apply: applyAddTrack,
		},
	}
}

func applyAddClip(p *editmodel.Project, a Args) (Result, []HostCommand) {
	src, _ := argString(a, "source")
	in, _ := argFloat(a, "in")
	out, _ := argFloat(a, "out")
	if out <= in {
		return failR("out (%g) must be greater than in (%g)", out, in)
	}
	if in < 0 {
		return failR("in must be >= 0, got %g", in)
	}
	track := argIntOr(a, "track", 0)
	t := p.TrackByIndex(track)
	if t == nil {
		return failR("no track with index %d", track)
	}
	pos, hasPos := argFloat(a, "pos")
	if !hasPos {
		pos = trackEnd(t)
	}
	if pos < 0 {
		return failR("pos must be >= 0, got %g", pos)
	}
	label, _ := argString(a, "label")
	id := p.NewClipID()
	clip := editmodel.Clip{ID: id, Source: src, In: in, Out: out, Pos: pos, Label: label}
	t.Clips = append(t.Clips, clip)
	host := HostCommand{Name: "timeline.append", Args: map[string]any{
		"id": id, "track": track, "source": src,
		"in": in, "out": out, "pos": pos,
		"frame_in": frame(in, p.FPS), "frame_out": frame(out, p.FPS), "frame_pos": frame(pos, p.FPS),
	}}
	return okHost("added clip "+id, map[string]any{"id": id, "pos": pos, "dur": clip.Dur()}, host)
}

func applyRemoveClip(p *editmodel.Project, a Args) (Result, []HostCommand) {
	id, _ := argString(a, "id")
	ti, ci, _, ok := p.FindClip(id)
	if !ok {
		return failR("no clip %q", id)
	}
	t := p.TrackByIndex(ti)
	t.Clips = append(t.Clips[:ci], t.Clips[ci+1:]...)
	dropFromSelection(p, id)
	host := HostCommand{Name: "timeline.remove", Args: map[string]any{"id": id, "track": ti}}
	return okHost("removed clip "+id, nil, host)
}

func applyRippleDelete(p *editmodel.Project, a Args) (Result, []HostCommand) {
	id, _ := argString(a, "id")
	ti, ci, clip, ok := p.FindClip(id)
	if !ok {
		return failR("no clip %q", id)
	}
	t := p.TrackByIndex(ti)
	gap := clip.Dur()
	t.Clips = append(t.Clips[:ci], t.Clips[ci+1:]...)
	for i := range t.Clips {
		if t.Clips[i].Pos > clip.Pos {
			t.Clips[i].Pos -= gap
			if t.Clips[i].Pos < 0 {
				t.Clips[i].Pos = 0
			}
		}
	}
	dropFromSelection(p, id)
	host := HostCommand{Name: "timeline.ripple_delete", Args: map[string]any{"id": id, "track": ti}}
	return okHost("ripple-deleted clip "+id+" (closed a "+f1(gap)+"s gap)", nil, host)
}

func applyMoveClip(p *editmodel.Project, a Args) (Result, []HostCommand) {
	id, _ := argString(a, "id")
	ti, ci, clip, ok := p.FindClip(id)
	if !ok {
		return failR("no clip %q", id)
	}
	if pos, has := argFloat(a, "pos"); has {
		if pos < 0 {
			return failR("pos must be >= 0, got %g", pos)
		}
		clip.Pos = pos
	}
	dstTrack := ti
	if tk, has := argInt(a, "track"); has {
		if p.TrackByIndex(tk) == nil {
			return failR("no track with index %d", tk)
		}
		dstTrack = tk
	}
	// Remove from the source track, add to the destination track.
	src := p.TrackByIndex(ti)
	src.Clips = append(src.Clips[:ci], src.Clips[ci+1:]...)
	dst := p.TrackByIndex(dstTrack)
	dst.Clips = append(dst.Clips, clip)
	host := HostCommand{Name: "timeline.move", Args: map[string]any{
		"id": id, "from_track": ti, "to_track": dstTrack, "pos": clip.Pos, "frame_pos": frame(clip.Pos, p.FPS),
	}}
	return okHost("moved clip "+id, map[string]any{"track": dstTrack, "pos": clip.Pos}, host)
}

func applyTrimClip(p *editmodel.Project, a Args) (Result, []HostCommand) {
	id, _ := argString(a, "id")
	ti, ci, _, ok := p.FindClip(id)
	if !ok {
		return failR("no clip %q", id)
	}
	t := p.TrackByIndex(ti)
	c := &t.Clips[ci]
	newIn, newOut := c.In, c.Out
	if v, has := argFloat(a, "in"); has {
		newIn = v
	}
	if v, has := argFloat(a, "out"); has {
		newOut = v
	}
	if newIn < 0 {
		return failR("in must be >= 0, got %g", newIn)
	}
	if newOut <= newIn {
		return failR("out (%g) must be greater than in (%g)", newOut, newIn)
	}
	c.In, c.Out = newIn, newOut
	host := HostCommand{Name: "timeline.trim", Args: map[string]any{
		"id": id, "in": newIn, "out": newOut, "frame_in": frame(newIn, p.FPS), "frame_out": frame(newOut, p.FPS),
	}}
	return okHost("trimmed clip "+id+" to "+f1(c.Dur())+"s", map[string]any{"dur": c.Dur()}, host)
}

func applySplitClip(p *editmodel.Project, a Args) (Result, []HostCommand) {
	id, _ := argString(a, "id")
	at, _ := argFloat(a, "at")
	ti, ci, clip, ok := p.FindClip(id)
	if !ok {
		return failR("no clip %q", id)
	}
	if at <= clip.Pos || at >= clip.End() {
		return failR("split point %gs must be strictly inside clip %s (%.1f-%.1f)", at, id, clip.Pos, clip.End())
	}
	cut := clip.In + (at - clip.Pos) // source offset of the cut
	t := p.TrackByIndex(ti)
	// Left half: shorten the original to end at the cut.
	t.Clips[ci].Out = cut
	// Right half: a new clip from cut to the original out, placed at `at`.
	rightID := p.NewClipID()
	right := clip.Clone()
	right.ID = rightID
	right.In = cut
	right.Out = clip.Out
	right.Pos = at
	// Insert right after the left half to keep visual order.
	t.Clips = append(t.Clips, editmodel.Clip{})
	copy(t.Clips[ci+2:], t.Clips[ci+1:])
	t.Clips[ci+1] = right
	host := HostCommand{Name: "timeline.split", Args: map[string]any{
		"id": id, "new_id": rightID, "at": at, "frame_at": frame(at, p.FPS),
	}}
	return okHost("split "+id+" into "+id+" + "+rightID, map[string]any{"new_id": rightID}, host)
}

func applyAddTrack(p *editmodel.Project, a Args) (Result, []HostCommand) {
	kind, _ := argString(a, "kind")
	var k editmodel.Kind
	switch kind {
	case "video":
		k = editmodel.KindVideo
	case "audio":
		k = editmodel.KindAudio
	default:
		return failR("kind must be \"video\" or \"audio\", got %q", kind)
	}
	idx := nextTrackIndex(p)
	name, has := argString(a, "name")
	if !has || name == "" {
		name = defaultTrackName(p, k)
	}
	p.Tracks = append(p.Tracks, editmodel.Track{Index: idx, Name: name, Kind: k, Clips: []editmodel.Clip{}})
	host := HostCommand{Name: "timeline.add_track", Args: map[string]any{"index": idx, "kind": kind, "name": name}}
	return okHost("added "+kind+" track "+name, map[string]any{"index": idx}, host)
}

// --- controls: playhead, selection, markers, forensic overlay ---------------

func controlTools() []toolDef {
	return []toolDef{
		{
			verb: "set_playhead", category: "controls", mutating: true,
			summary: "Move the timeline playhead to a position in seconds.",
			params:  []ParamSpec{{"at", "number", true, "timeline position in seconds"}},
			apply:   applySetPlayhead,
		},
		{
			verb: "select_clip", category: "controls", mutating: true,
			summary: "Select a clip by id, or clear the selection.",
			params: []ParamSpec{
				{"id", "string", false, "clip id to select"},
				{"clear", "bool", false, "true to clear the selection"},
			},
			apply: applySelectClip,
		},
		{
			verb: "set_marker", category: "controls", mutating: true,
			summary: "Drop a labelled marker at a timeline position (a forensic note).",
			params: []ParamSpec{
				{"at", "number", true, "timeline position in seconds"},
				{"label", "string", false, "marker label"},
			},
			apply: applySetMarker,
		},
		{
			verb: "set_overlay", category: "controls", mutating: true,
			summary: "Toggle a forensic lower-third line (filename, timecode, date, person, location, link, or enabled).",
			params: []ParamSpec{
				{"field", "string", true, "one of: enabled, filename, timecode, date, person, location, link"},
				{"on", "bool", true, "true to show, false to hide"},
			},
			apply: applySetOverlay,
		},
	}
}

func applySetPlayhead(p *editmodel.Project, a Args) (Result, []HostCommand) {
	at, _ := argFloat(a, "at")
	if at < 0 {
		at = 0
	}
	p.Playhead = at
	host := HostCommand{Name: "player.seek", Args: map[string]any{"seconds": at, "frame": frame(at, p.FPS)}}
	return okHost("playhead -> "+f1(at)+"s", map[string]any{"playhead": at}, host)
}

func applySelectClip(p *editmodel.Project, a Args) (Result, []HostCommand) {
	if clr, _ := argBool(a, "clear"); clr {
		p.Selection = nil
		return okHost("cleared selection", nil, HostCommand{Name: "timeline.select", Args: map[string]any{"ids": []string{}}})
	}
	id, has := argString(a, "id")
	if !has || id == "" {
		return failR("select_clip needs an id (or clear:true)")
	}
	if !p.HasClip(id) {
		return failR("no clip %q", id)
	}
	p.Selection = []string{id}
	return okHost("selected "+id, map[string]any{"selection": []string{id}},
		HostCommand{Name: "timeline.select", Args: map[string]any{"ids": []string{id}}})
}

func applySetMarker(p *editmodel.Project, a Args) (Result, []HostCommand) {
	at, _ := argFloat(a, "at")
	if at < 0 {
		return failR("marker position must be >= 0, got %g", at)
	}
	label, _ := argString(a, "label")
	id := nextMarkerID(p)
	p.Markers = append(p.Markers, editmodel.Marker{ID: id, At: at, Label: label})
	host := HostCommand{Name: "timeline.marker", Args: map[string]any{"id": id, "at": at, "label": label, "frame": frame(at, p.FPS)}}
	return okHost("marker "+id+" @ "+f1(at)+"s", map[string]any{"id": id}, host)
}

func applySetOverlay(p *editmodel.Project, a Args) (Result, []HostCommand) {
	field, _ := argString(a, "field")
	on, _ := argBool(a, "on")
	switch field {
	case "enabled":
		p.Overlay.Enabled = on
	case "filename":
		p.Overlay.ShowFilename = on
	case "timecode":
		p.Overlay.ShowTimecode = on
	case "date":
		p.Overlay.ShowDate = on
	case "person":
		p.Overlay.ShowPerson = on
	case "location":
		p.Overlay.ShowLocation = on
	case "link":
		p.Overlay.ShowLink = on
	default:
		return failR("unknown overlay field %q", field)
	}
	// Turning any specific line on implies the overlay is enabled so it renders.
	if on && field != "enabled" {
		p.Overlay.Enabled = true
	}
	host := HostCommand{Name: "overlay.set", Args: map[string]any{"field": field, "on": on}}
	return okHost("overlay "+field+" = "+boolStr(on), nil, host)
}

// --- small helpers ----------------------------------------------------------

// trackEnd is the timeline end of the last clip on a track (0 for empty).
func trackEnd(t *editmodel.Track) float64 {
	var max float64
	for _, c := range t.Clips {
		if e := c.End(); e > max {
			max = e
		}
	}
	return max
}

func argIntOr(a Args, key string, def int) int {
	if v, ok := argInt(a, key); ok {
		return v
	}
	return def
}

func nextTrackIndex(p *editmodel.Project) int {
	max := -1
	for _, t := range p.Tracks {
		if t.Index > max {
			max = t.Index
		}
	}
	return max + 1
}

func defaultTrackName(p *editmodel.Project, k editmodel.Kind) string {
	n := 0
	for _, t := range p.Tracks {
		if t.Kind == k {
			n++
		}
	}
	if k == editmodel.KindVideo {
		return "V" + itoa(n+1)
	}
	return "A" + itoa(n+1)
}

func nextMarkerID(p *editmodel.Project) string {
	return "m" + itoa(len(p.Markers)+1)
}

func dropFromSelection(p *editmodel.Project, id string) {
	out := p.Selection[:0]
	for _, s := range p.Selection {
		if s != id {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		p.Selection = nil
	} else {
		p.Selection = out
	}
}

// frame converts seconds to a frame number at the given fps (rounded).
func frame(seconds, fps float64) int {
	if fps <= 0 {
		fps = 30
	}
	return int(math.Round(seconds * fps))
}

func boolStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func itoa(n int) string { return strconv.Itoa(n) }

func f1(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }
