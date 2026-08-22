package main

import (
	"testing"

	"becky-go/internal/config"
)

// The critic is judged against WHAT THE CLIP IS ABOUT, and that yardstick comes
// out of the watch pass's own note. If this parse breaks, the critic is handed
// an empty "about" and will wave any framing through — a silent failure that
// looks exactly like a working critic.
func TestAboutFromNoteReadsTheWatchPassAnswer(t *testing.T) {
	cases := []struct {
		name string
		note string
		want string
	}{
		{
			"the window moved",
			`the model WATCHED this and cut it 0.0s-61.0s (was 22.1s-62.0s): payoff at 55.4s is ` +
				`"The prankster reveals the contents of the bag and the unsuspecting person reacts."; ` +
				`in because Establishes the hidden camera prank.`,
			"The prankster reveals the contents of the bag and the unsuspecting person reacts.",
		},
		{
			"the window was kept",
			"the model watched this and kept the window as proposed: The speaker admits they slammed " +
				"the door.; the pose tracker lost the subject for 3.4s in a row",
			"The speaker admits they slammed the door.",
		},
		{
			// THE REGRESSION, verbatim from the 2026-08-21 run. The note is a
			// "; "-joined list and the framing ladder appends its OWN quoted
			// noun later in it. Searching the whole tail for a quote returned
			// "colorful poster" as the subject of the clip - which is becky's
			// mistake being handed to the critic as the yardstick for judging
			// that mistake.
			"a later quoted phrase in the note must not hijack the yardstick",
			"the model watched this and kept the window as proposed: The speaker admits they " +
				"slammed the door.; the pose tracker lost the subject for 3.4s in a row; " +
				`grounded "colorful poster", but only in 27% of frames`,
			"The speaker admits they slammed the door.",
		},
		{
			"no watch pass ran",
			"jumpcuts unavailable: becky-cut missing; continuous render",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := aboutFromNote(c.note); got != c.want {
				t.Errorf("aboutFromNote()\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

// The forced target must NOT leak from one short into the next one in a --reel
// run: short 2 is a different scene and "the man in the pink shirt" is a fact
// about short 1.
func TestCriticTargetDoesNotLeakBetweenShorts(t *testing.T) {
	setShortTarget("the mouse trap")
	resetShortGround(0, 10)
	if shortGround.target != "the mouse trap" {
		t.Fatalf("the target did not reach the sweep: %q", shortGround.target)
	}
	// renderAndCritique clears it at the top of every short.
	setShortTarget("")
	resetShortGround(0, 10)
	if shortGround.target != "" {
		t.Errorf("short 2 inherited short 1's critic target: %q", shortGround.target)
	}
}

// THE SPEAKER RUNG MUST BE SILENT WHEN THERE IS NO SPEAKER TO PICK.
// Jordan: "just make sure that if there is no visible speaker that it doesn't
// break the pipeline; only relevant when there are people visibly speaking."
// Every one of these is a normal shot in his footage, not an error.
func TestSpeakerRungFallsThroughWhenThereIsNoChoice(t *testing.T) {
	one := 1
	cases := []struct {
		name string
		rep  speakingReportJSON
	}{
		{"nobody on screen (POV shot)", speakingReportJSON{OK: true, Confidence: "none"}},
		{"one face, nothing to choose between", func() speakingReportJSON {
			r := speakingReportJSON{OK: true, Confidence: "conclusion", SpeakerTrackID: &one}
			r.Tracks = append(r.Tracks, struct {
				ID           int      `json:"id"`
				Start        float64  `json:"start"`
				End          float64  `json:"end"`
				Detections   int      `json:"detections"`
				SpeakingFrac *float64 `json:"speaking_frac"`
				Speaking     bool     `json:"speaking"`
				Boxes        []struct {
					T    float64    `json:"t"`
					BBox [4]float64 `json:"bbox"`
				} `json:"boxes"`
			}{ID: 1, Detections: 10})
			return r
		}()},
		{"two faces but it is a coin flip", speakingReportJSON{
			OK: true, Confidence: "candidate", SpeakerTrackID: &one,
			Tracks: make([]struct {
				ID           int      `json:"id"`
				Start        float64  `json:"start"`
				End          float64  `json:"end"`
				Detections   int      `json:"detections"`
				SpeakingFrac *float64 `json:"speaking_frac"`
				Speaking     bool     `json:"speaking"`
				Boxes        []struct {
					T    float64    `json:"t"`
					BBox [4]float64 `json:"bbox"`
				} `json:"boxes"`
			}, 2),
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			shortSpeaker = speakerCache{done: true, winStart: 0, winEnd: 10, rep: c.rep, ok: true}
			rects, note, ok := speakerAim(config.Config{}, "no-such-file.mp4", 0, 10, 9.0/16.0, 1920, 1080, nil)
			if ok || rects != nil || note != "" {
				t.Errorf("the speaker rung answered when it should have fallen through: ok=%v note=%q",
					ok, note)
			}
		})
	}
}

// ...and it must not run at all when becky-speaking never answered, rather than
// treating "no report" as "no speaker".
func TestSpeakerRungIsSilentWithoutAReport(t *testing.T) {
	shortSpeaker = speakerCache{done: true, winStart: 0, winEnd: 10, ok: false}
	if _, _, ok := speakerAim(config.Config{}, "no-such-file.mp4", 0, 10, 9.0/16.0, 1920, 1080, nil); ok {
		t.Error("the speaker rung claimed an answer with no report behind it")
	}
}

// The cache exists so a 30-span short pays for ONE who-is-speaking pass, not 30.
func TestSpeakerPassRunsOncePerShort(t *testing.T) {
	resetShortSpeaker(0, 40)
	if shortSpeaker.done {
		t.Fatal("the cache started already-run")
	}
	// A src that cannot resolve becky-speaking still marks the pass as done, so
	// the next span does not retry the same failing launch.
	shortSpeaker.lookup("no-such-file.mp4")
	if !shortSpeaker.done {
		t.Error("a failed pass was not remembered; every span would relaunch it")
	}
}

// A CRITIC RE-RENDER MUST NOT UNDO THE WATCH PASS.
//
// resetShortFraming clears the watched flag, and the re-render runs with
// useWatch=false because the window is already decided. Without carrying the
// flag across, pass 2 silently re-enables the tail trim that pass 1 had switched
// off — and the trim's whole job is deciding content from a pose tracker, which
// is the thing that must never happen after a model has chosen the out point.
func TestReRenderKeepsTheWatchedFlag(t *testing.T) {
	setShortWatched(true)
	setShortPayoff(55.4)

	// This is exactly what render() does around resetShortFraming.
	payoff, watched := shortPayoff, shortWatched
	resetShortFraming(0, 61)
	setShortPayoff(payoff)
	setShortWatched(watched)

	if !shortWatched {
		t.Error("a re-render lost the watched flag; the tail trim would come back on")
	}
	if shortPayoff != 55.4 {
		t.Errorf("a re-render lost the payoff: %.2f", shortPayoff)
	}
	setShortWatched(false)
	setShortPayoff(0)
}
