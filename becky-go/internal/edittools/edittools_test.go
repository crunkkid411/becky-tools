package edittools

import (
	"testing"

	"becky-go/internal/editmodel"
)

func base() *editmodel.Project {
	p := editmodel.New("case", 30)
	return p
}

// addOne is a helper that applies add_clip and returns the new project + the id.
func addOne(t *testing.T, p *editmodel.Project, src string, in, out float64) (*editmodel.Project, string) {
	t.Helper()
	np, host, res := Apply(p, ToolCall{Verb: "add_clip", Args: Args{"source": src, "in": in, "out": out}})
	if !res.OK {
		t.Fatalf("add_clip failed: %s", res.Message)
	}
	if len(host) == 0 || host[0].Name != "timeline.append" {
		t.Fatalf("add_clip should emit timeline.append, got %+v", host)
	}
	id, _ := res.Data["id"].(string)
	if id == "" {
		t.Fatalf("add_clip returned no id")
	}
	return np, id
}

func TestAllowlistAndMutatingFlags(t *testing.T) {
	if !IsAllowed("add_clip") || !IsAllowed("vision") {
		t.Fatalf("expected add_clip + vision to be allowed")
	}
	if IsAllowed("rm -rf") || IsAllowed("eval") {
		t.Fatalf("unknown verbs must NOT be allowed")
	}
	if !IsMutating("add_clip") {
		t.Fatalf("add_clip must be mutating")
	}
	if IsMutating("preview_clip") || IsMutating("search") || IsMutating("vision") || IsMutating("render") {
		t.Fatalf("read/produce verbs must NOT be mutating")
	}
}

func TestUnknownVerbRejected(t *testing.T) {
	p := base()
	np, host, res := Apply(p, ToolCall{Verb: "delete_everything"})
	if res.OK {
		t.Fatalf("unknown verb should fail")
	}
	if np != p || host != nil {
		t.Fatalf("unknown verb must leave the project unchanged and emit nothing")
	}
}

func TestMissingRequiredArgRejected(t *testing.T) {
	p := base()
	_, _, res := Apply(p, ToolCall{Verb: "add_clip", Args: Args{"source": "x.mp4"}}) // no in/out
	if res.OK {
		t.Fatalf("add_clip without in/out should fail validation")
	}
}

func TestAddClipBumpsRevAndAppends(t *testing.T) {
	p := base()
	if p.Rev != 0 {
		t.Fatalf("fresh project rev should be 0")
	}
	np, id := addOne(t, p, `E:\c\a.mp4`, 10, 18)
	_ = id
	if np.Rev != 1 {
		t.Fatalf("mutating verb should bump rev to 1, got %d", np.Rev)
	}
	if p.ClipCount() != 0 {
		t.Fatalf("original project must be unchanged (copy-on-write); got %d clips", p.ClipCount())
	}
	if np.ClipCount() != 1 {
		t.Fatalf("new project should have 1 clip, got %d", np.ClipCount())
	}
	// Append a second clip — default pos must be the end of the first (10..18 => pos 8).
	np2, _ := addOne(t, np, `E:\c\a.mp4`, 0, 5)
	_, _, c2, _ := np2.FindClip("c2")
	if c2.Pos != 8 {
		t.Fatalf("second clip should append at pos 8, got %g", c2.Pos)
	}
}

func TestReadVerbDoesNotBumpRev(t *testing.T) {
	p, id := addOne(t, base(), `E:\c\a.mp4`, 10, 18)
	rev := p.Rev
	np, host, res := Apply(p, ToolCall{Verb: "preview_clip", Args: Args{"id": id}})
	if !res.OK {
		t.Fatalf("preview_clip failed: %s", res.Message)
	}
	if np.Rev != rev {
		t.Fatalf("read verb must NOT bump rev (was %d, now %d)", rev, np.Rev)
	}
	if host[0].Name != "player.open_seek_play" {
		t.Fatalf("preview should emit player.open_seek_play, got %s", host[0].Name)
	}
	if host[0].Args["in"].(float64) != 10 {
		t.Fatalf("preview in-point should be the clip's in (10), got %v", host[0].Args["in"])
	}
}

func TestTrimSplitRipple(t *testing.T) {
	p, id := addOne(t, base(), `E:\c\a.mp4`, 0, 20) // 20s clip at pos 0
	// trim out to 12 -> dur 12.
	p, _, res := Apply(p, ToolCall{Verb: "trim_clip", Args: Args{"id": id, "out": 12.0}})
	if !res.OK {
		t.Fatalf("trim failed: %s", res.Message)
	}
	_, _, c, _ := p.FindClip(id)
	if c.Dur() != 12 {
		t.Fatalf("after trim dur should be 12, got %g", c.Dur())
	}
	// split at 6 -> left ends at source 6, right new clip from 6.
	np, _, res := Apply(p, ToolCall{Verb: "split_clip", Args: Args{"id": id, "at": 6.0}})
	if !res.OK {
		t.Fatalf("split failed: %s", res.Message)
	}
	if np.ClipCount() != 2 {
		t.Fatalf("after split want 2 clips, got %d", np.ClipCount())
	}
	_, _, left, _ := np.FindClip(id)
	if left.Out != 6 {
		t.Fatalf("left half should end at source 6, got %g", left.Out)
	}
	newID, _ := res.Data["new_id"].(string)
	_, _, right, _ := np.FindClip(newID)
	if right.In != 6 || right.Pos != 6 {
		t.Fatalf("right half should start at source 6 / pos 6, got in=%g pos=%g", right.In, right.Pos)
	}
}

func TestRippleDeleteClosesGap(t *testing.T) {
	p := base()
	p, c1 := addOne(t, p, `E:\c\a.mp4`, 0, 10) // pos 0, dur 10
	p, _ = addOne(t, p, `E:\c\a.mp4`, 0, 10)   // c2 pos 10
	p, _, res := Apply(p, ToolCall{Verb: "ripple_delete", Args: Args{"id": c1}})
	if !res.OK {
		t.Fatalf("ripple_delete failed: %s", res.Message)
	}
	if p.ClipCount() != 1 {
		t.Fatalf("ripple should leave 1 clip, got %d", p.ClipCount())
	}
	_, _, c2, _ := p.FindClip("c2")
	if c2.Pos != 0 {
		t.Fatalf("ripple should slide c2 to pos 0, got %g", c2.Pos)
	}
}

func TestFailureLeavesProjectUnchanged(t *testing.T) {
	p, _ := addOne(t, base(), `E:\c\a.mp4`, 0, 10)
	before := p.Rev
	np, host, res := Apply(p, ToolCall{Verb: "remove_clip", Args: Args{"id": "ghost"}})
	if res.OK {
		t.Fatalf("removing a missing clip should fail")
	}
	if np != p || host != nil || np.Rev != before {
		t.Fatalf("a failed op must return the original project unchanged")
	}
}

func TestEffectsAllowlistAndClamp(t *testing.T) {
	p, id := addOne(t, base(), `E:\c\a.mp4`, 0, 10)
	// Unknown effect rejected.
	if _, _, res := Apply(p, ToolCall{Verb: "add_effect", Args: Args{"id": id, "name": "deepfake"}}); res.OK {
		t.Fatalf("unknown effect must be rejected")
	}
	// Unknown param rejected.
	if _, _, res := Apply(p, ToolCall{Verb: "add_effect", Args: Args{"id": id, "name": "brightness", "bogus": 1.0}}); res.OK {
		t.Fatalf("unknown effect param must be rejected")
	}
	// Over-range value clamped (level max is 2).
	np, host, res := Apply(p, ToolCall{Verb: "add_effect", Args: Args{"id": id, "name": "brightness", "level": 99.0}})
	if !res.OK {
		t.Fatalf("add_effect should succeed: %s", res.Message)
	}
	_, _, c, _ := np.FindClip(id)
	if len(c.Effects) != 1 || c.Effects[0].Params["level"] != 2 {
		t.Fatalf("brightness level should clamp to 2, got %+v", c.Effects)
	}
	if host[0].Name != "filter.add" {
		t.Fatalf("add_effect should emit filter.add, got %s", host[0].Name)
	}
}

func TestVideoEffectRejectedOnAudioTrack(t *testing.T) {
	p := base() // track 1 is audio
	np, _, res := Apply(p, ToolCall{Verb: "add_clip", Args: Args{"source": "a.wav", "in": 0.0, "out": 5.0, "track": 1.0}})
	if !res.OK {
		t.Fatalf("add audio clip failed: %s", res.Message)
	}
	if _, _, r := Apply(np, ToolCall{Verb: "add_effect", Args: Args{"id": "c1", "name": "brightness"}}); r.OK {
		t.Fatalf("a video-only effect must be rejected on an audio track")
	}
}

func TestSetVolumeAddsThenUpdates(t *testing.T) {
	p := base()
	np, _, res := Apply(p, ToolCall{Verb: "add_clip", Args: Args{"source": "a.wav", "in": 0.0, "out": 5.0, "track": 1.0}})
	if !res.OK {
		t.Fatalf("add audio clip failed")
	}
	np, _, res = Apply(np, ToolCall{Verb: "set_volume", Args: Args{"id": "c1", "db": -6.0}})
	if !res.OK {
		t.Fatalf("set_volume failed: %s", res.Message)
	}
	_, _, c, _ := np.FindClip("c1")
	if len(c.Effects) != 1 || c.Effects[0].Params["db"] != -6 {
		t.Fatalf("volume should be -6 dB, got %+v", c.Effects)
	}
	// Setting again must UPDATE, not add a second volume effect.
	np, _, _ = Apply(np, ToolCall{Verb: "set_volume", Args: Args{"id": "c1", "db": 3.0}})
	_, _, c, _ = np.FindClip("c1")
	if len(c.Effects) != 1 || c.Effects[0].Params["db"] != 3 {
		t.Fatalf("volume should update in place to 3, got %+v", c.Effects)
	}
}

func TestOverlayAndPlayheadAndSelection(t *testing.T) {
	p, id := addOne(t, base(), `E:\c\a.mp4`, 0, 10)
	p, _, _ = Apply(p, ToolCall{Verb: "set_overlay", Args: Args{"field": "timecode", "on": true}})
	if !p.Overlay.Enabled || !p.Overlay.ShowTimecode {
		t.Fatalf("set_overlay timecode on should enable overlay + timecode")
	}
	p, host, _ := Apply(p, ToolCall{Verb: "set_playhead", Args: Args{"at": 4.0}})
	if p.Playhead != 4 || host[0].Args["frame"].(int) != 120 {
		t.Fatalf("playhead should be 4s / frame 120 at 30fps, got %g / %v", p.Playhead, host[0].Args["frame"])
	}
	p, _, res := Apply(p, ToolCall{Verb: "select_clip", Args: Args{"id": id}})
	if !res.OK || len(p.Selection) != 1 || p.Selection[0] != id {
		t.Fatalf("select_clip should set selection to [%s], got %v", id, p.Selection)
	}
	// Selecting a missing clip fails.
	if _, _, r := Apply(p, ToolCall{Verb: "select_clip", Args: Args{"id": "ghost"}}); r.OK {
		t.Fatalf("selecting a missing clip must fail")
	}
}

func TestRenderEmitsReelAndRejectsEmpty(t *testing.T) {
	p := base()
	if _, _, res := Apply(p, ToolCall{Verb: "render"}); res.OK {
		t.Fatalf("render on empty timeline must fail")
	}
	p, _ = addOne(t, p, `E:\c\a.mp4`, 0, 10)
	_, host, res := Apply(p, ToolCall{Verb: "render", Args: Args{"overlay": true}})
	if !res.OK {
		t.Fatalf("render should succeed: %s", res.Message)
	}
	if host[0].Name != "render.export" {
		t.Fatalf("render should emit render.export, got %s", host[0].Name)
	}
	if res.Data["clips"].(int) != 1 {
		t.Fatalf("render data should report 1 clip, got %v", res.Data["clips"])
	}
}

func TestDescribeAndToolListCoverEveryVerb(t *testing.T) {
	specs := Describe()
	if len(specs) != len(Verbs()) {
		t.Fatalf("Describe should cover every verb: %d vs %d", len(specs), len(Verbs()))
	}
	list := ToolList()
	for _, v := range Verbs() {
		if indexOf(list, string(v)+"(") < 0 {
			t.Fatalf("ToolList missing a signature for %q", v)
		}
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
