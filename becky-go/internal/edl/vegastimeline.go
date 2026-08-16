package edl

// vegastimeline.go is the LIVE Vegas seam: the timeline as a running VEGAS Pro
// hands it over, rather than a file Jordan had to export first.
//
// vegasimport.go already reads an edit Vegas EXPORTED (EDL TXT / FCP7 XML). That
// costs a File > Export round trip and, worse, throws away WHERE each event sits
// on the timeline — the exporters describe a gapless programme, so an edit with
// holes in it comes back butted together. A caption placed from that lands in the
// wrong place on the real timeline.
//
// So a script running INSIDE Vegas writes this instead: for every event it is
// about to caption, the source file, the [in,out] span of that source, and the
// event's own timeline position. That is everything the caption path needs, with
// no export, no guessing, and no lost gaps.
//
// The reel it produces is the usual gapless becky programme (clips butted end to
// end, which is what internal/subs times captions against). MapSpan is the way
// back: it converts an output-timeline span into the VEGAS timeline seconds the
// script must actually drop the text event at.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"becky-go/internal/pathx"
)

// VegasEvent is one event on the Vegas timeline, as the script reports it. All
// times are SECONDS: In/Out index into Source, Timeline is where the event
// starts on the Vegas ruler.
type VegasEvent struct {
	Source   string  `json:"source"`
	In       float64 `json:"in"`
	Out      float64 `json:"out"`
	Timeline float64 `json:"timeline"`
	Track    int     `json:"track,omitempty"`
	Label    string  `json:"label,omitempty"`
}

// Dur is the event's length in seconds, clamped to >= 0.
func (e VegasEvent) Dur() float64 {
	if d := e.Out - e.In; d > 0 {
		return d
	}
	return 0
}

// VegasTimeline is what a script running inside Vegas writes for becky. Events
// arrive in whatever order the script walked the track; LoadVegasTimeline sorts
// them by Timeline so the reel order is the order a viewer actually sees.
type VegasTimeline struct {
	Version string       `json:"version"`
	Project string       `json:"project,omitempty"`
	FPS     float64      `json:"fps,omitempty"`
	Events  []VegasEvent `json:"events"`
}

// LoadVegasTimeline reads and validates a timeline JSON written by a Vegas
// script. Events with no source or no length are dropped (an empty or muted
// placeholder must not shift every caption after it), and the survivors are
// sorted by timeline position. An edit with no usable events is an error — there
// is nothing to caption and silently returning zero cues would look like ASR
// failed.
func LoadVegasTimeline(path string) (VegasTimeline, error) {
	f, err := os.Open(path)
	if err != nil {
		return VegasTimeline{}, err
	}
	defer f.Close()
	return ParseVegasTimeline(f)
}

// ParseVegasTimeline is LoadVegasTimeline's io.Reader half, so tests need no
// temp files.
func ParseVegasTimeline(r io.Reader) (VegasTimeline, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return VegasTimeline{}, err
	}
	var tl VegasTimeline
	if err := json.Unmarshal(b, &tl); err != nil {
		return VegasTimeline{}, fmt.Errorf("parse vegas timeline: %w", err)
	}

	kept := make([]VegasEvent, 0, len(tl.Events))
	for _, e := range tl.Events {
		if e.Source == "" || e.Dur() <= 0 {
			continue
		}
		if e.Timeline < 0 {
			e.Timeline = 0
		}
		kept = append(kept, e)
	}
	// Stable, so two events stacked at the same instant keep the script's order
	// rather than being shuffled between runs — captions must be deterministic.
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Timeline < kept[j].Timeline })
	tl.Events = kept

	if len(tl.Events) == 0 {
		return tl, fmt.Errorf("vegas timeline has no events with media and length")
	}
	return tl, nil
}

// Reel converts the timeline into the gapless becky programme internal/subs
// times captions against: clip i occupies [offset, offset+dur) where offset is
// the sum of every earlier clip's duration. The Vegas positions are NOT carried
// here — MapSpan puts them back afterwards.
func (t VegasTimeline) Reel() Reel {
	clips := make([]Clip, 0, len(t.Events))
	for i, e := range t.Events {
		clips = append(clips, Clip{
			ID:     fmt.Sprintf("v%d", i+1),
			Source: e.Source,
			In:     e.In,
			Out:    e.Out,
			Label:  e.Label,
			Meta:   ClipMeta{SourceFPS: t.FPS},
		})
	}
	name := "vegas-timeline"
	if t.Project != "" {
		name = stem(pathx.Base(t.Project))
	}
	return Reel{Version: "1", Name: name, Clips: clips}
}

// offsets returns each event's start on the gapless output timeline, plus the
// total length. offsets[i] is where Reel clip i begins.
func (t VegasTimeline) offsets() ([]float64, float64) {
	offs := make([]float64, len(t.Events))
	var total float64
	for i, e := range t.Events {
		offs[i] = total
		total += e.Dur()
	}
	return offs, total
}

// spanEps absorbs float noise when deciding which event an output-timeline
// instant belongs to. A caption's start is frequently EXACTLY an event boundary
// (internal/subs snaps the first caption of a cut to the cut's start), and the
// sum-of-durations that produced the boundary and the sum that produced the cue
// differ in their last bits. Without a tolerance a caption lands one event early
// and appears at the wrong place on the timeline.
const spanEps = 1e-6

// MapSpan converts an output-timeline span (the times internal/subs produces)
// into VEGAS timeline seconds. ok is false when the span falls outside the edit
// entirely.
//
// Captions never span a cut (subs.BuildFromChunks, settled 2026-07-24), so a
// span belongs to exactly one event and the conversion is a shift. The one
// deliberate exception is the post-speech hold, which can push the last caption
// of a cut up to the next cut's start; that end is carried to the NEXT event's
// Vegas position rather than being stretched across a gap the viewer can see.
func (t VegasTimeline) MapSpan(start, end float64) (float64, float64, bool) {
	offs, total := t.offsets()
	if len(offs) == 0 || start >= total {
		return 0, 0, false
	}
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}

	// The event this span starts in: the last one that begins at or before it.
	i := 0
	for j := range offs {
		if start >= offs[j]-spanEps {
			i = j
		}
	}
	ev := t.Events[i]
	segEnd := offs[i] + ev.Dur()

	local := start - offs[i]
	if local < 0 {
		local = 0
	}
	if local > ev.Dur() {
		local = ev.Dur()
	}
	tlStart := ev.Timeline + local

	var tlEnd float64
	switch {
	case end > segEnd+spanEps && i+1 < len(t.Events):
		// Post-speech hold across the cut: hand the end to where the next event
		// actually starts on the Vegas ruler, not to a time that assumes the two
		// are butted together.
		tlEnd = t.Events[i+1].Timeline
	default:
		localEnd := end - offs[i]
		if localEnd > ev.Dur() {
			localEnd = ev.Dur()
		}
		tlEnd = ev.Timeline + localEnd
	}
	if tlEnd <= tlStart {
		tlEnd = tlStart
	}
	return tlStart, tlEnd, true
}

// stem drops a file extension from a base name.
func stem(base string) string {
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] == '.' {
			return base[:i]
		}
	}
	return base
}
