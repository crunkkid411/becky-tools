package audioengine

import (
	"math"
	"testing"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

func songArr(t *testing.T) *dawmodel.Arrangement {
	t.Helper()
	a := dawmodel.New()
	a.BPM = 120
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	a = a.AddTrack("bass", dawmodel.KindMIDI)
	a.Tracks[1].Clips = append(a.Tracks[1].Clips, dawmodel.Clip{Name: "b", Channel: 0, Program: 38})
	for _, s := range []int{0, 4, 8, 12} {
		a, _, _ = a.AddNote("drums", "beat", dawmodel.Note{Start: s * music.StepTicks, Dur: music.StepTicks, Pitch: 36, Vel: 110, Ch: 9})
	}
	a, _, _ = a.AddNote("bass", "b", dawmodel.Note{Start: 0, Dur: 480, Pitch: 45, Vel: 100, Ch: 0})
	return a
}

func TestRenderArrangementBuf_audibleAndNoClip(t *testing.T) {
	buf, err := RenderArrangementBuf(songArr(t), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) == 0 {
		t.Fatal("no audio rendered")
	}
	var peak float64
	for _, s := range buf {
		if a := math.Abs(float64(s)); a > peak {
			peak = a
		}
	}
	if peak < 0.05 {
		t.Errorf("render is too quiet (peak %.3f) — likely silent", peak)
	}
	if peak > 0.95 {
		t.Errorf("render clips (peak %.3f) — gain staging failed", peak)
	}
}

func TestRenderArrangementBuf_emptyErrors(t *testing.T) {
	if _, err := RenderArrangementBuf(dawmodel.New(), 48000, 1); err == nil {
		t.Error("an empty arrangement should error, not render silence")
	}
}

func TestRenderArrangementBuf_loopsTile(t *testing.T) {
	one, _ := RenderArrangementBuf(songArr(t), 48000, 1)
	three, _ := RenderArrangementBuf(songArr(t), 48000, 3)
	if len(three) != len(one)*3 {
		t.Errorf("loops=3 should tile 3x: got %d, want %d", len(three), len(one)*3)
	}
}
