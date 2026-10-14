package clippick

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func segs(n int, every float64) []Segment {
	var out []Segment
	for i := range n {
		out = append(out, Segment{Start: float64(i) * every, End: float64(i)*every + every - 0.5, Text: fmt.Sprintf("line %d.", i)})
	}
	return out
}

func TestWindowsAlignToSegmentsAndOverlap(t *testing.T) {
	ws := BuildWindows(segs(60, 5), 300, 90, 30) // 5 s lines over 5 minutes
	if len(ws) < 4 || ws[0].ID != "window_001" || ws[0].Start != 0 {
		t.Fatalf("windows = %+v", ws)
	}
	for i, w := range ws {
		if d := w.End - w.Start; d > 90*1.25 {
			t.Errorf("window %d is %.1f s, over the 1.25x cap", i, d)
		}
		if i > 0 && w.Start >= ws[i-1].End {
			t.Errorf("window %d starts at %.1f, after the previous end %.1f: no overlap", i, w.Start, ws[i-1].End)
		}
	}
	if last := ws[len(ws)-1]; last.End != 299.5 {
		t.Errorf("the last window ends at %.1f, not at the last line", last.End)
	}
}

func TestBatchesAreNearEqual(t *testing.T) {
	ws := BuildWindows(segs(200, 5), 1000, 90, 30)
	ws = ws[:9]
	var sizes []int
	for _, b := range ScoreBatches(ws, 8) {
		sizes = append(sizes, len(b))
	}
	if fmt.Sprint(sizes) != "[5 4]" { // never 8 + a lone 1
		t.Fatalf("batch sizes = %v", sizes)
	}
}

func TestCountsAndShortlist(t *testing.T) {
	if lo, hi := ClipCountTargets(3); lo != 3 || hi != 6 {
		t.Errorf("3 windows: %d-%d, want 3-6", lo, hi)
	}
	if lo, hi := ClipCountTargets(10); lo != 6 || hi != 12 {
		t.Errorf("10 windows: %d-%d, want 6-12", lo, hi)
	}
	if ShortlistTarget(60) != 3 || ShortlistTarget(1470) != 10 || ShortlistTarget(450) != 7 {
		t.Errorf("shortlist targets %d %d %d", ShortlistTarget(60), ShortlistTarget(1470), ShortlistTarget(450))
	}
}

func TestTrimKeepsBestNotEarliest(t *testing.T) {
	cs := []Clip{{Start: 0, Score: 40}, {Start: 100, Score: 90}, {Start: 200, Score: 50}, {Start: 300, Score: 80}}
	got := TrimToBest(cs, 2)
	if len(got) != 2 || got[0].Start != 100 || got[1].Start != 300 {
		t.Fatalf("trim = %+v", got)
	}
}

func TestDedupeDropsAQuarterOverlap(t *testing.T) {
	cs := []Clip{{Start: 0, End: 40, Score: 60}, {Start: 30, End: 70, Score: 80}, {Start: 100, End: 140, Score: 10}}
	got := DedupeOverlapping(cs, 0.25)
	if len(got) != 2 || got[0].Start != 30 || got[1].Start != 100 {
		t.Fatalf("dedupe = %+v", got)
	}
}

func TestSnapLandsOnWordsWithHalfThePause(t *testing.T) {
	var ws []Word
	for i := range 100 { // a word every 0.5 s, 0.3 s long: 0.2 s pauses
		ws = append(ws, Word{Text: "w", Start: float64(i) * 0.5, End: float64(i)*0.5 + 0.3})
	}
	s, e := Snap(10.13, 30.27, ws, 50, 15, 60)
	if s != 9.9 || e != 30.4 { // word at 10.0 minus 0.1; word ending 30.3 plus 0.1
		t.Fatalf("snap = %.3f-%.3f, want 9.900-30.400", s, e)
	}
	if s, e := Snap(10, 12, ws, 50, 15, 60); e-s < 15 {
		t.Fatalf("a short proposal is lengthened on a word end: %.2f-%.2f", s, e)
	}
}

// The whole two-pass flow with a scripted model: the floor call happens when
// the detail pass comes back short, and overlapping clips collapse to one.
func TestPickTwoPassesWithFloor(t *testing.T) {
	ss := segs(120, 5) // 10 minutes
	var calls []string
	chat := func(system, user string, sch map[string]any, n int) (string, error) {
		switch {
		case strings.HasPrefix(user, "RANK"):
			calls = append(calls, "score")
			var ws []Window
			_ = json.Unmarshal([]byte(user[strings.Index(user, "["):]), &ws)
			var out []map[string]any
			for i, w := range ws {
				out = append(out, map[string]any{"id": w.ID, "score": 90 - i*7, "reason": "r"})
			}
			b, _ := json.Marshal(map[string]any{"windows": out})
			return string(b), nil
		default:
			calls = append(calls, "detail")
			var ws []Window
			_ = json.Unmarshal([]byte(user[strings.Index(user, "["):]), &ws)
			w := ws[0]
			b, _ := json.Marshal(map[string]any{"shorts": []map[string]any{
				{"start": w.Start, "end": w.Start + 30, "source_window_id": w.ID, "predicted_score": 70, "viral_hook_text": "h", "video_title_for_youtube_short": "t", "video_description_for_tiktok": "d", "video_description_for_instagram": "d", "why": "y"},
			}})
			return string(b), nil
		}
	}
	clips, err := Pick(chat, ss, nil, 600, Options{MinSec: 15, MaxSec: 60, ScoreBatch: 3})
	if err != nil {
		t.Fatal(err)
	}
	if calls[len(calls)-1] != "detail" || calls[len(calls)-2] != "detail" {
		t.Fatalf("calls = %v, want a second detail call for the floor", calls)
	}
	if len(clips) != 2 {
		t.Fatalf("clips = %+v", clips)
	}
}
