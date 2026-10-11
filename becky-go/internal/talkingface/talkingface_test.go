package talkingface

import (
	"os"
	"os/exec"
	"testing"
)

func TestPickBoxNeedsAClearTalker(t *testing.T) {
	id := 1
	var out speakingOut
	out.OK, out.TrackID, out.Confidence = true, &id, "ambiguous"
	out.Tracks = make([]struct {
		ID       int  `json:"id"`
		Speaking bool `json:"speaking"`
		Boxes    []struct {
			T    float64    `json:"t"`
			BBox [4]float64 `json:"bbox"`
		} `json:"boxes"`
	}, 2)
	out.Tracks[0].ID, out.Tracks[1].ID = 1, 2
	out.Tracks[0].Boxes = append(out.Tracks[0].Boxes, struct {
		T    float64    `json:"t"`
		BBox [4]float64 `json:"bbox"`
	}{T: 3, BBox: [4]float64{10, 10, 50, 60}})
	if _, _, ok := pickBox(out, 3); ok {
		t.Fatal("two faces and no clear winner must not pick one")
	}
	out.Confidence = "conclusion"
	if tt, b, ok := pickBox(out, 3); !ok || tt != 3 || b[2] != 50 {
		t.Fatalf("a clear winner's box: %v %v %v", tt, b, ok)
	}
}

// The crop filter must be one ffmpeg accepts (run only where ffmpeg is installed).
func TestCropFilterRuns(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	out := t.TempDir() + "/c.jpg"
	for _, b := range [][4]float64{{525, 282, 895, 782}, {0.1, 0.1, 0.4, 0.5}, {0, 0, 640, 360}} {
		cmd := exec.Command(ff, "-f", "lavfi", "-i", "color=c=red:s=640x360", "-frames:v", "1",
			"-vf", cropFilter(b), "-y", "-loglevel", "error", out)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("box %v: %v %s", b, err, msg)
		}
		os.Remove(out)
	}
}
