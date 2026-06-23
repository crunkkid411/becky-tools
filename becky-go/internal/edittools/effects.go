package edittools

import (
	"fmt"
	"sort"

	"becky-go/internal/editmodel"
)

// paramRange bounds one effect parameter. Values are clamped into [min,max]
// (degrade-never-crash: a wild value from the model is corrected, not crashed).
type paramRange struct {
	min, max, def float64
}

func (r paramRange) clamp(v float64) (float64, bool) {
	if v < r.min {
		return r.min, true
	}
	if v > r.max {
		return r.max, true
	}
	return v, false
}

// effectSpec is one allowlisted effect: its legal scalar params + which track
// kinds it applies to. The model can ONLY attach effects in this allowlist — it
// can never invent an arbitrary MLT/frei0r filter (default-deny).
type effectSpec struct {
	kind   string // "video" | "audio" | "both"
	params map[string]paramRange
}

// effectAllowlist maps an effect name to its spec. These names are the abstract
// becky vocabulary; the forked Shotcut dock maps each to the concrete MLT/frei0r
// service (e.g. brightness -> "brightness", volume -> "volume"/"avfilter.volume").
var effectAllowlist = map[string]effectSpec{
	"brightness": {kind: "video", params: map[string]paramRange{"level": {0, 2, 1}}},
	"contrast":   {kind: "video", params: map[string]paramRange{"level": {0, 2, 1}}},
	"saturation": {kind: "video", params: map[string]paramRange{"level": {0, 2, 1}}},
	"opacity":    {kind: "video", params: map[string]paramRange{"level": {0, 1, 1}}},
	"speed":      {kind: "both", params: map[string]paramRange{"factor": {0.25, 4, 1}}},
	"crop":       {kind: "video", params: map[string]paramRange{"top": {0, 0.5, 0}, "bottom": {0, 0.5, 0}, "left": {0, 0.5, 0}, "right": {0, 0.5, 0}}},
	"volume":     {kind: "audio", params: map[string]paramRange{"db": {-60, 12, 0}}},
	"fadeIn":     {kind: "both", params: map[string]paramRange{"seconds": {0, 10, 1}}},
	"fadeOut":    {kind: "both", params: map[string]paramRange{"seconds": {0, 10, 1}}},
}

// AllowedEffects returns the allowlisted effect names, sorted (for the model
// prompt + tests).
func AllowedEffects() []string {
	out := make([]string, 0, len(effectAllowlist))
	for n := range effectAllowlist {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func effectTools() []toolDef {
	return []toolDef{
		{
			verb: "add_effect", category: "effects", mutating: true,
			summary: "Attach an effect to a clip. Allowed: brightness, contrast, saturation, opacity, speed, crop, volume, fadeIn, fadeOut. Pass params as extra args.",
			params: []ParamSpec{
				{"id", "string", true, "clip id"},
				{"name", "string", true, "effect name from the allowlist"},
			},
			apply: applyAddEffect,
		},
		{
			verb: "set_effect_param", category: "effects", mutating: true,
			summary: "Change one parameter of an effect already on a clip.",
			params: []ParamSpec{
				{"id", "string", true, "clip id"},
				{"name", "string", true, "effect name (or pass effect:<fxid>)"},
				{"param", "string", true, "parameter name"},
				{"value", "number", true, "new value (clamped to the legal range)"},
			},
			apply: applySetEffectParam,
		},
		{
			verb: "remove_effect", category: "effects", mutating: true,
			summary: "Remove an effect from a clip by name (or effect id).",
			params: []ParamSpec{
				{"id", "string", true, "clip id"},
				{"name", "string", true, "effect name (or pass effect:<fxid>)"},
			},
			apply: applyRemoveEffect,
		},
	}
}

func applyAddEffect(p *editmodel.Project, a Args) (Result, []HostCommand) {
	clipID, _ := argString(a, "id")
	name, _ := argString(a, "name")
	spec, ok := effectAllowlist[name]
	if !ok {
		return failR("effect %q is not allowed (choose from %v)", name, AllowedEffects())
	}
	ti, ci, clip, found := p.FindClip(clipID)
	if !found {
		return failR("no clip %q", clipID)
	}
	if !effectFitsKind(p, ti, spec.kind) {
		return failR("effect %q does not apply to that track kind", name)
	}
	// Build params: every allowlisted param defaults; provided ones are clamped;
	// an unknown provided param is rejected (default-deny on args).
	params := map[string]float64{}
	for pn, pr := range spec.params {
		params[pn] = pr.def
	}
	var clamped []string
	for k, v := range a {
		if k == "id" || k == "name" {
			continue
		}
		pr, known := spec.params[k]
		if !known {
			return failR("effect %q has no parameter %q (allowed: %v)", name, k, paramNames(spec))
		}
		f, okF := toFloat(v)
		if !okF {
			return failR("parameter %q must be a number", k)
		}
		cv, wasClamped := pr.clamp(f)
		params[k] = cv
		if wasClamped {
			clamped = append(clamped, k)
		}
	}
	fxID := nextEffectID(clip)
	t := p.TrackByIndex(ti)
	t.Clips[ci].Effects = append(t.Clips[ci].Effects, editmodel.Effect{ID: fxID, Name: name, Params: params})
	msg := "added effect " + name + " to " + clipID
	if len(clamped) > 0 {
		msg += " (clamped " + join(clamped) + ")"
	}
	host := HostCommand{Name: "filter.add", Args: map[string]any{"clip": clipID, "fx_id": fxID, "name": name, "params": params}}
	return okHost(msg, map[string]any{"fx_id": fxID, "params": params}, host)
}

func applySetEffectParam(p *editmodel.Project, a Args) (Result, []HostCommand) {
	clipID, _ := argString(a, "id")
	sel, _ := argString(a, "name")
	param, _ := argString(a, "param")
	value, _ := argFloat(a, "value")
	ti, ci, clip, found := p.FindClip(clipID)
	if !found {
		return failR("no clip %q", clipID)
	}
	ei := findEffect(clip, sel)
	if ei < 0 {
		return failR("clip %q has no effect %q", clipID, sel)
	}
	name := clip.Effects[ei].Name
	pr, known := effectAllowlist[name].params[param]
	if !known {
		return failR("effect %q has no parameter %q", name, param)
	}
	cv, _ := pr.clamp(value)
	t := p.TrackByIndex(ti)
	if t.Clips[ci].Effects[ei].Params == nil {
		t.Clips[ci].Effects[ei].Params = map[string]float64{}
	}
	t.Clips[ci].Effects[ei].Params[param] = cv
	host := HostCommand{Name: "filter.set", Args: map[string]any{"clip": clipID, "fx_id": clip.Effects[ei].ID, "param": param, "value": cv}}
	return okHost(fmt.Sprintf("%s.%s = %g on %s", name, param, cv, clipID), map[string]any{"value": cv}, host)
}

func applyRemoveEffect(p *editmodel.Project, a Args) (Result, []HostCommand) {
	clipID, _ := argString(a, "id")
	sel, _ := argString(a, "name")
	ti, ci, clip, found := p.FindClip(clipID)
	if !found {
		return failR("no clip %q", clipID)
	}
	ei := findEffect(clip, sel)
	if ei < 0 {
		return failR("clip %q has no effect %q", clipID, sel)
	}
	fxID := clip.Effects[ei].ID
	name := clip.Effects[ei].Name
	t := p.TrackByIndex(ti)
	t.Clips[ci].Effects = append(t.Clips[ci].Effects[:ei], t.Clips[ci].Effects[ei+1:]...)
	host := HostCommand{Name: "filter.remove", Args: map[string]any{"clip": clipID, "fx_id": fxID}}
	return okHost("removed effect "+name+" from "+clipID, nil, host)
}

// --- audio: per-clip volume, per-track mute/gain, fades ----------------------

func audioTools() []toolDef {
	return []toolDef{
		{
			verb: "set_volume", category: "audio", mutating: true,
			summary: "Set a clip's volume in dB (adds/updates a volume effect; 0 = unity, negative = quieter).",
			params: []ParamSpec{
				{"id", "string", true, "clip id"},
				{"db", "number", true, "gain in dB (-60..12)"},
			},
			apply: applySetVolume,
		},
		{
			verb: "add_fade", category: "audio", mutating: true,
			summary: "Add a fade in or out to a clip (audio + video).",
			params: []ParamSpec{
				{"id", "string", true, "clip id"},
				{"kind", "string", true, "\"in\" or \"out\""},
				{"seconds", "number", false, "fade length in seconds (default 1)"},
			},
			apply: applyAddFade,
		},
		{
			verb: "mute_track", category: "audio", mutating: true,
			summary: "Mute or unmute a whole track.",
			params: []ParamSpec{
				{"track", "number", true, "track index"},
				{"on", "bool", true, "true to mute, false to unmute"},
			},
			apply: applyMuteTrack,
		},
		{
			verb: "set_track_gain", category: "audio", mutating: true,
			summary: "Set a track's gain in dB (0 = unity).",
			params: []ParamSpec{
				{"track", "number", true, "track index"},
				{"db", "number", true, "gain in dB (-60..12)"},
			},
			apply: applySetTrackGain,
		},
	}
}

func applySetVolume(p *editmodel.Project, a Args) (Result, []HostCommand) {
	clipID, _ := argString(a, "id")
	db, _ := argFloat(a, "db")
	cv, _ := effectAllowlist["volume"].params["db"].clamp(db)
	ti, ci, clip, found := p.FindClip(clipID)
	if !found {
		return failR("no clip %q", clipID)
	}
	t := p.TrackByIndex(ti)
	if ei := findEffect(clip, "volume"); ei >= 0 {
		t.Clips[ci].Effects[ei].Params["db"] = cv
	} else {
		fxID := nextEffectID(clip)
		t.Clips[ci].Effects = append(t.Clips[ci].Effects, editmodel.Effect{ID: fxID, Name: "volume", Params: map[string]float64{"db": cv}})
	}
	host := HostCommand{Name: "filter.set", Args: map[string]any{"clip": clipID, "name": "volume", "param": "db", "value": cv}}
	return okHost(fmt.Sprintf("volume of %s = %g dB", clipID, cv), map[string]any{"db": cv}, host)
}

func applyAddFade(p *editmodel.Project, a Args) (Result, []HostCommand) {
	clipID, _ := argString(a, "id")
	kind, _ := argString(a, "kind")
	var name string
	switch kind {
	case "in":
		name = "fadeIn"
	case "out":
		name = "fadeOut"
	default:
		return failR("fade kind must be \"in\" or \"out\", got %q", kind)
	}
	seconds := 1.0
	if v, has := argFloat(a, "seconds"); has {
		seconds = v
	}
	cv, _ := effectAllowlist[name].params["seconds"].clamp(seconds)
	ti, ci, clip, found := p.FindClip(clipID)
	if !found {
		return failR("no clip %q", clipID)
	}
	t := p.TrackByIndex(ti)
	if ei := findEffect(clip, name); ei >= 0 {
		t.Clips[ci].Effects[ei].Params["seconds"] = cv
	} else {
		fxID := nextEffectID(clip)
		t.Clips[ci].Effects = append(t.Clips[ci].Effects, editmodel.Effect{ID: fxID, Name: name, Params: map[string]float64{"seconds": cv}})
	}
	host := HostCommand{Name: "filter.add", Args: map[string]any{"clip": clipID, "name": name, "params": map[string]float64{"seconds": cv}}}
	return okHost(fmt.Sprintf("%s on %s (%gs)", name, clipID, cv), map[string]any{"seconds": cv}, host)
}

func applyMuteTrack(p *editmodel.Project, a Args) (Result, []HostCommand) {
	idx, _ := argInt(a, "track")
	on, _ := argBool(a, "on")
	t := p.TrackByIndex(idx)
	if t == nil {
		return failR("no track with index %d", idx)
	}
	t.Mute = on
	host := HostCommand{Name: "track.mute", Args: map[string]any{"track": idx, "on": on}}
	return okHost(fmt.Sprintf("track %d mute = %v", idx, on), nil, host)
}

func applySetTrackGain(p *editmodel.Project, a Args) (Result, []HostCommand) {
	idx, _ := argInt(a, "track")
	db, _ := argFloat(a, "db")
	cv, _ := paramRange{-60, 12, 0}.clamp(db)
	t := p.TrackByIndex(idx)
	if t == nil {
		return failR("no track with index %d", idx)
	}
	t.Gain = cv
	host := HostCommand{Name: "track.gain", Args: map[string]any{"track": idx, "db": cv}}
	return okHost(fmt.Sprintf("track %d gain = %g dB", idx, cv), map[string]any{"db": cv}, host)
}

// --- effect helpers ---------------------------------------------------------

// findEffect locates an effect on a clip by its name OR by "effect:<fxid>".
// Returns the index or -1.
func findEffect(clip editmodel.Clip, sel string) int {
	if len(sel) > 7 && sel[:7] == "effect:" {
		want := sel[7:]
		for i, e := range clip.Effects {
			if e.ID == want {
				return i
			}
		}
		return -1
	}
	for i, e := range clip.Effects {
		if e.Name == sel || e.ID == sel {
			return i
		}
	}
	return -1
}

// nextEffectID generates a stable per-clip effect id, e.g. "c2fx1".
func nextEffectID(clip editmodel.Clip) string {
	return clip.ID + "fx" + itoa(len(clip.Effects)+1)
}

// effectFitsKind reports whether an effect's kind is legal on the given track.
func effectFitsKind(p *editmodel.Project, trackIdx int, kind string) bool {
	if kind == "both" {
		return true
	}
	t := p.TrackByIndex(trackIdx)
	if t == nil {
		return false
	}
	return string(t.Kind) == kind
}

func paramNames(spec effectSpec) []string {
	out := make([]string, 0, len(spec.params))
	for n := range spec.params {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}
