package edl

import (
	"math"
	"strings"
	"testing"
)

const eps = 1e-9

func nearly(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s = %.9f, want %.9f", name, got, want)
	}
}

// A timeline with a REAL gap between the two events: event 1 ends at 4.0 on the
// Vegas ruler, event 2 starts at 10.0. This is the shape the EDL exporters throw
// away, and the reason this seam exists.
func gappyTimeline() VegasTimeline {
	return VegasTimeline{
		Version: "1",
		FPS:     30,
		Events: []VegasEvent{
			{Source: `C:\v\a.mp4`, In: 10, Out: 14, Timeline: 0},  // 4s -> output [0,4)
			{Source: `C:\v\a.mp4`, In: 40, Out: 42, Timeline: 10}, // 2s -> output [4,6)
		},
	}
}

func TestParseVegasTimelineSortsAndDropsEmpties(t *testing.T) {
	in := `{
	  "version":"1","fps":29.97,
	  "events":[
	    {"source":"C:\\v\\b.mp4","in":5,"out":7,"timeline":12},
	    {"source":"C:\\v\\a.mp4","in":0,"out":3,"timeline":2},
	    {"source":"","in":0,"out":5,"timeline":30},
	    {"source":"C:\\v\\c.mp4","in":9,"out":9,"timeline":40}
	  ]}`
	tl, err := ParseVegasTimeline(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(tl.Events) != 2 {
		t.Fatalf("kept %d events, want 2 (sourceless and zero-length dropped)", len(tl.Events))
	}
	if tl.Events[0].Timeline != 2 || tl.Events[1].Timeline != 12 {
		t.Errorf("events not sorted by timeline: %v", tl.Events)
	}
}

func TestParseVegasTimelineRejectsEmptyEdit(t *testing.T) {
	_, err := ParseVegasTimeline(strings.NewReader(`{"version":"1","events":[]}`))
	if err == nil {
		t.Fatal("want an error for a timeline with no usable events, got nil")
	}
}

func TestReelIsGaplessInTimelineOrder(t *testing.T) {
	r := gappyTimeline().Reel()
	if len(r.Clips) != 2 {
		t.Fatalf("got %d clips, want 2", len(r.Clips))
	}
	nearly(t, "clip0 dur", r.Clips[0].Dur(), 4)
	nearly(t, "clip1 dur", r.Clips[1].Dur(), 2)
	// The reel is the RENDERED programme: 6s, not the 12s the Vegas ruler spans.
	nearly(t, "reel duration", r.Duration(), 6)
}

// The load-bearing case: a caption inside the SECOND event must come back at its
// real Vegas position (10s+), not at the gapless 4s the reel says.
func TestMapSpanRestoresTheGap(t *testing.T) {
	tl := gappyTimeline()

	start, end, ok := tl.MapSpan(4.0, 5.0)
	if !ok {
		t.Fatal("MapSpan(4,5) reported out of range")
	}
	nearly(t, "start", start, 10.0)
	nearly(t, "end", end, 11.0)

	// And the last caption of the second event ends where that event ends: 12.0.
	start, end, ok = tl.MapSpan(5.5, 6.0)
	if !ok {
		t.Fatal("MapSpan(5.5,6) reported out of range")
	}
	nearly(t, "tail start", start, 11.5)
	nearly(t, "tail end", end, 12.0)
}

func TestMapSpanFirstEventIsIdentityWhenItStartsAtZero(t *testing.T) {
	tl := gappyTimeline()
	start, end, ok := tl.MapSpan(0, 1.25)
	if !ok {
		t.Fatal("out of range")
	}
	nearly(t, "start", start, 0)
	nearly(t, "end", end, 1.25)
}

// A caption whose start sits EXACTLY on an event boundary must land in the
// event that begins there, not the one that ended there. subs snaps the first
// caption of every cut to the cut start, so this is the common case, and float
// noise in the boundary is why spanEps exists.
func TestMapSpanBoundaryBelongsToTheFollowingEvent(t *testing.T) {
	tl := gappyTimeline()
	// 4.0 is simultaneously event 0's end and event 1's start.
	start, _, ok := tl.MapSpan(4.0-eps, 4.5)
	if !ok {
		t.Fatal("out of range")
	}
	nearly(t, "start", start, 10.0)
}

// The post-speech hold pushes a caption's end past its own event. It must be
// carried to where the NEXT event really starts, never stretched across the gap.
func TestMapSpanPostSpeechHoldLandsOnTheNextEvent(t *testing.T) {
	tl := gappyTimeline()
	start, end, ok := tl.MapSpan(3.5, 4.0+0.2) // ends past event 0's 4.0
	if !ok {
		t.Fatal("out of range")
	}
	nearly(t, "start", start, 3.5)
	nearly(t, "end", end, 10.0) // event 1's Vegas position, not 4.2
}

// A hold on the LAST event has no next event to reach; it must clamp to that
// event's own end rather than running off the edit.
func TestMapSpanHoldOnLastEventClampsToItsEnd(t *testing.T) {
	tl := gappyTimeline()
	_, end, ok := tl.MapSpan(5.8, 6.9)
	if !ok {
		t.Fatal("out of range")
	}
	nearly(t, "end", end, 12.0)
}

func TestMapSpanOutsideTheEditIsNotOK(t *testing.T) {
	tl := gappyTimeline()
	if _, _, ok := tl.MapSpan(6.0, 6.5); ok {
		t.Error("a span at/after the end of the edit should report ok=false")
	}
}

// Events butted end to end (the ordinary cuts-only edit) must map identically to
// the reel — the gap handling must not perturb the common case.
func TestMapSpanButtedEventsAreAShift(t *testing.T) {
	tl := VegasTimeline{
		Version: "1",
		Events: []VegasEvent{
			{Source: `C:\v\a.mp4`, In: 10, Out: 14, Timeline: 30},
			{Source: `C:\v\a.mp4`, In: 40, Out: 42, Timeline: 34},
		},
	}
	start, end, ok := tl.MapSpan(4.25, 5.0)
	if !ok {
		t.Fatal("out of range")
	}
	nearly(t, "start", start, 34.25)
	nearly(t, "end", end, 35.0)
}

// SourceHits answers "where on MY timeline is this line from the source file?" -
// the reverse of MapSpan. Each case is a real editing situation.
func TestSourceHits(t *testing.T) {
	tl := VegasTimeline{Events: []VegasEvent{
		{Source: `C:\v\a.mp4`, In: 10, Out: 14, Timeline: 0, Track: 1},
		{Source: `C:\v\a.mp4`, In: 40, Out: 42, Timeline: 10, Track: 1},
		{Source: `C:\v\a.mp4`, In: 10, Out: 14, Timeline: 30, Track: 3}, // same shot used again later
		{Source: `C:\v\fast.mp4`, In: 0, Out: 10, Timeline: 100, Rate: 2},
		// picture and sound of one clip: two events, one place on the ruler
		{Source: `C:\v\pair.mp4`, In: 0, Out: 8, Timeline: 200, Track: 0},
		{Source: `C:\v\pair.mp4`, In: 0, Out: 8, Timeline: 200, Track: 1},
	}}
	type span struct{ start, end float64 }
	cases := []struct {
		name   string
		source string
		cue    span
		want   []TimelineHit
	}{
		{"inside first event, and the reuse", `C:\v\a.mp4`, span{11, 12},
			[]TimelineHit{{Timeline: 1, TimelineEnd: 2, Track: 1}, {Timeline: 31, TimelineEnd: 32, Track: 3}}},
		{"cut out of the edit", `C:\v\a.mp4`, span{20, 21}, nil},
		{"starts before the in-point: only the kept part", `C:\v\a.mp4`, span{39, 41},
			[]TimelineHit{{Timeline: 10, TimelineEnd: 11, Track: 1}}},
		{"ends exactly at an in-point is not a hit", `C:\v\a.mp4`, span{38, 40}, nil},
		{"sped-up event divides by the rate", `C:\v\fast.mp4`, span{4, 6},
			[]TimelineHit{{Timeline: 102, TimelineEnd: 103}}},
		{"Windows path compare ignores case and slashes", `c:/V/A.MP4`, span{41, 41.5},
			[]TimelineHit{{Timeline: 11, TimelineEnd: 11.5, Track: 1}}},
		{"zero-length cue inside an event", `C:\v\a.mp4`, span{13, 13},
			[]TimelineHit{{Timeline: 3, TimelineEnd: 3, Track: 1}, {Timeline: 33, TimelineEnd: 33, Track: 3}}},
		{"other file never matches", `C:\v\b.mp4`, span{11, 12}, nil},
		{"video+audio of one clip is ONE hit, not two", `C:\v\pair.mp4`, span{2, 3},
			[]TimelineHit{{Timeline: 202, TimelineEnd: 203, Track: 0}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tl.SourceHits(tc.source, tc.cue.start, tc.cue.end)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d hits %+v, want %d %+v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				nearly(t, "timeline", got[i].Timeline, tc.want[i].Timeline)
				nearly(t, "timeline_end", got[i].TimelineEnd, tc.want[i].TimelineEnd)
				if got[i].Track != tc.want[i].Track {
					t.Errorf("hit %d track = %d, want %d", i, got[i].Track, tc.want[i].Track)
				}
			}
		})
	}
}
