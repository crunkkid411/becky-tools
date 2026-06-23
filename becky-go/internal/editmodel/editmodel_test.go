package editmodel

import (
	"strings"
	"testing"

	"becky-go/internal/edl"
)

// sample builds a small two-clip project used across tests.
func sample() *Project {
	p := New("TakingBack2007", 30)
	vt := p.TrackByIndex(0)
	vt.Clips = []Clip{
		{ID: "c1", Source: `E:\case\bounty.mp4`, In: 60, Out: 68, Pos: 0, Label: "every penguin in the building"},
		{ID: "c2", Source: `E:\case\bounty.mp4`, In: 120.5, Out: 136.5, Pos: 8, Label: "i'll pay you"},
	}
	p.ClipSeq = 2
	return p
}

func TestNewHasVideoAndAudioTracks(t *testing.T) {
	p := New("x", 0)
	if got := len(p.Tracks); got != 2 {
		t.Fatalf("New: want 2 tracks, got %d", got)
	}
	if p.Tracks[0].Kind != KindVideo || p.Tracks[1].Kind != KindAudio {
		t.Fatalf("New: want video then audio, got %s,%s", p.Tracks[0].Kind, p.Tracks[1].Kind)
	}
	if p.FPS != edl.DefaultFPS {
		t.Fatalf("New: fps<=0 should default to %g, got %g", edl.DefaultFPS, p.FPS)
	}
}

func TestCloneIsDeepAndIndependent(t *testing.T) {
	p := sample()
	c := p.Clone()
	// Mutate the clone's clip + add an effect; the original must not change.
	c.Tracks[0].Clips[0].Label = "CHANGED"
	c.Tracks[0].Clips[0].Effects = append(c.Tracks[0].Clips[0].Effects, Effect{ID: "fx1", Name: "brightness", Params: map[string]float64{"level": 1.2}})
	if p.Tracks[0].Clips[0].Label != "every penguin in the building" {
		t.Fatalf("Clone leaked label mutation: %q", p.Tracks[0].Clips[0].Label)
	}
	if len(p.Tracks[0].Clips[0].Effects) != 0 {
		t.Fatalf("Clone leaked effect mutation: %d effects on original", len(p.Tracks[0].Clips[0].Effects))
	}
}

func TestClipDurAndEnd(t *testing.T) {
	c := Clip{In: 10, Out: 18, Pos: 4}
	if c.Dur() != 8 {
		t.Fatalf("Dur: want 8, got %g", c.Dur())
	}
	if c.End() != 12 {
		t.Fatalf("End: want 12, got %g", c.End())
	}
	// Out<In must clamp to 0, never negative.
	if (Clip{In: 5, Out: 2}).Dur() != 0 {
		t.Fatalf("Dur should clamp negative to 0")
	}
}

func TestDurationIsTimelineEnd(t *testing.T) {
	p := sample()
	// c2 ends at pos 8 + dur 16 = 24.
	if got := p.Duration(); got != 24 {
		t.Fatalf("Duration: want 24, got %g", got)
	}
}

func TestFindClipAndCounts(t *testing.T) {
	p := sample()
	ti, pos, clip, ok := p.FindClip("c2")
	if !ok || ti != 0 || pos != 1 || clip.In != 120.5 {
		t.Fatalf("FindClip(c2): got track=%d pos=%d in=%g ok=%v", ti, pos, clip.In, ok)
	}
	if _, _, _, ok := p.FindClip("nope"); ok {
		t.Fatalf("FindClip(nope) should be false")
	}
	if p.ClipCount() != 2 {
		t.Fatalf("ClipCount: want 2, got %d", p.ClipCount())
	}
}

func TestNewClipIDAdvancesAndIsUnique(t *testing.T) {
	p := sample()
	id := p.NewClipID()
	if id != "c3" {
		t.Fatalf("NewClipID after seq=2: want c3, got %s", id)
	}
	if p.NewClipID() != "c4" {
		t.Fatalf("NewClipID should advance to c4")
	}
}

func TestDigestIsCompactAndInformative(t *testing.T) {
	p := sample()
	p.Playhead = 12
	p.Selection = []string{"c2"}
	p.Rev = 7
	d := p.Digest()
	// Must name the project, rev, the cursors, and each clip by id + basename.
	for _, want := range []string{`project "TakingBack2007"`, "rev=7", "playhead=12.0s", "selection=[c2]",
		"c1  bounty.mp4 [60.0-68.0]", "c2  bounty.mp4 [120.5-136.5]", "(selected)"} {
		if !strings.Contains(d, want) {
			t.Fatalf("Digest missing %q in:\n%s", want, d)
		}
	}
	// Must NOT leak absolute paths (forensic source paths stay out of the model ctx bloat).
	if strings.Contains(d, `E:\case`) {
		t.Fatalf("Digest leaked an absolute source path:\n%s", d)
	}
}

func TestValidateCatchesProblems(t *testing.T) {
	p := sample()
	if err := p.Validate(); err != nil {
		t.Fatalf("valid project rejected: %v", err)
	}
	// Duplicate id.
	bad := sample()
	bad.Tracks[0].Clips[1].ID = "c1"
	if err := bad.Validate(); err == nil {
		t.Fatalf("duplicate id should fail Validate")
	}
	// out<in.
	bad2 := sample()
	bad2.Tracks[0].Clips[0].Out = 1
	if err := bad2.Validate(); err == nil {
		t.Fatalf("out<in should fail Validate")
	}
	// selection referencing a missing clip.
	bad3 := sample()
	bad3.Selection = []string{"ghost"}
	if err := bad3.Validate(); err == nil {
		t.Fatalf("dangling selection should fail Validate")
	}
}

func TestToReelOrdersByPosAndSkipsEmpty(t *testing.T) {
	p := sample()
	// Add an out-of-order, zero-length clip — it must be dropped, and order is by Pos.
	vt := p.TrackByIndex(0)
	vt.Clips = append(vt.Clips, Clip{ID: "c3", Source: `E:\case\x.mp4`, In: 5, Out: 5, Pos: 2})
	r := p.ToReel()
	if r.Name != "TakingBack2007" {
		t.Fatalf("ToReel name: %q", r.Name)
	}
	if len(r.Clips) != 2 {
		t.Fatalf("ToReel should drop the zero-length clip; got %d clips", len(r.Clips))
	}
	if r.Clips[0].ID != "c1" || r.Clips[1].ID != "c2" {
		t.Fatalf("ToReel order: got %s,%s", r.Clips[0].ID, r.Clips[1].ID)
	}
}

func TestReelRoundTripPreservesClips(t *testing.T) {
	p := sample()
	r := p.ToReel()
	back := FromReel(r, 30)
	if back.ClipCount() != 2 {
		t.Fatalf("FromReel clip count: want 2, got %d", back.ClipCount())
	}
	// ClipSeq must be advanced past imported ids so a new id does not collide.
	if got := back.NewClipID(); got != "c3" {
		t.Fatalf("FromReel should advance ClipSeq past c2; NewClipID got %s", got)
	}
	// Positions are running offsets: c1 at 0 (dur 8), c2 at 8.
	_, _, c2, _ := back.FindClip("c2")
	if c2.Pos != 8 {
		t.Fatalf("FromReel c2 pos: want 8, got %g", c2.Pos)
	}
}
