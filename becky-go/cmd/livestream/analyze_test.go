package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"becky-go/internal/config"
)

// A whole stream must reach the picture helper 30 s at a time, and measured frames never again.
func TestNeedSpansSplitsAndSkipsMeasured(t *testing.T) {
	have := map[int]picFrame{}
	for k := 0; k <= 99; k++ { // 0-9.9 s already measured
		have[k] = picFrame{T: float64(k) / picFPS}
	}
	got := needSpans(have, []span{{0, 75}})
	want := []span{{10, 40}, {40, 70}, {70, 75.1}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if d := got[i].A - want[i].A + got[i].B - want[i].B; d > 1e-9 || d < -1e-9 {
			t.Fatalf("span %d: got %v, want %v", i, got[i], want[i])
		}
	}
	if got := needSpans(have, []span{{2, 9}}); got != nil {
		t.Fatalf("measured stretch asked again: %v", got)
	}
}

// Kept sound labels come back exactly, with every frame marked as heard.
func TestSoundCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.sound-v1.json")
	c := soundFile{Names: []string{"Laughter"}, Breath: []float64{0.1, 0.7}, Voice: []float64{0.9, 0.0}, Vocal: []float64{0, 0.2}, VocalK: []int{0, 0}}
	writeJSON(path, c)
	s, err := soundFrames(config.Config{}, "missing.wav", path) // the labeler is never run: the file is kept
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.breath, c.Breath) || !s.ok(1) || s.ok(2) || s.names[0] != "Laughter" {
		t.Fatalf("got %+v", s)
	}
}
