package mediainfo

import "testing"

func parseProbeJSON(t *testing.T, js string) Info {
	t.Helper()
	info, err := infoFromProbeJSON([]byte(js))
	if err != nil {
		t.Fatalf("infoFromProbeJSON: %v", err)
	}
	return info
}

// A phone portrait clip is stored LANDSCAPE with a display matrix. Rendering it
// at the coded size letterboxes the upright picture into a horizontal frame —
// the "my vertical video came out horizontal" bug, seen for real on
// IMG_9624.MP4 (1920x1080, rotation -90) which rendered 1920x1080.
func TestDisplayDimensionsSwapOnAQuarterTurn(t *testing.T) {
	cases := []struct {
		name         string
		w, h, rot    int
		wantW, wantH int
	}{
		{"iphone portrait (-90 normalised to 270)", 1920, 1080, 270, 1080, 1920},
		{"portrait the other way", 1920, 1080, 90, 1080, 1920},
		{"upside down keeps orientation", 1920, 1080, 180, 1920, 1080},
		{"no rotation", 1920, 1080, 0, 1920, 1080},
		{"already-portrait source untouched", 1080, 1920, 0, 1080, 1920},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			i := Info{Width: c.w, Height: c.h, Rotation: c.rot}
			if got := i.DisplayWidth(); got != c.wantW {
				t.Fatalf("DisplayWidth() = %d, want %d", got, c.wantW)
			}
			if got := i.DisplayHeight(); got != c.wantH {
				t.Fatalf("DisplayHeight() = %d, want %d", got, c.wantH)
			}
			// the coded numbers must survive untouched for callers that mean them
			if i.Width != c.w || i.Height != c.h {
				t.Fatalf("coded dims mutated: %dx%d, want %dx%d", i.Width, i.Height, c.w, c.h)
			}
		})
	}
}

func TestNormalizeRotation(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{
		{0, 0},
		{-90, 270}, // what an iPhone actually reports
		{90, 90},
		{180, 180},
		{-180, 180},
		{270, 270},
		{360, 0},
		{-360, 0},
		{450, 90},
		{-90.000001, 270}, // float matrix noise must not fall through to 0
		{89.6, 90},
	}
	for _, c := range cases {
		if got := normalizeRotation(c.in); got != c.want {
			t.Errorf("normalizeRotation(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// The rotation must survive ffprobe's two spellings: a NUMBER in side_data_list
// (current builds) and a STRING in stream tags (older ones).
func TestProbeParsesRotationFromEitherSpelling(t *testing.T) {
	t.Run("side_data_list number", func(t *testing.T) {
		info := parseProbeJSON(t, `{
			"streams":[{"codec_type":"video","width":1920,"height":1080,
			  "r_frame_rate":"30000/1001",
			  "side_data_list":[{"rotation":-90}]}],
			"format":{"duration":"10.0"}}`)
		if info.Rotation != 270 {
			t.Fatalf("Rotation = %d, want 270", info.Rotation)
		}
		if info.DisplayWidth() != 1080 || info.DisplayHeight() != 1920 {
			t.Fatalf("display = %dx%d, want 1080x1920", info.DisplayWidth(), info.DisplayHeight())
		}
	})

	t.Run("tags rotate string", func(t *testing.T) {
		info := parseProbeJSON(t, `{
			"streams":[{"codec_type":"video","width":1920,"height":1080,
			  "r_frame_rate":"30000/1001",
			  "tags":{"rotate":"270"}}],
			"format":{"duration":"10.0"}}`)
		if info.Rotation != 270 {
			t.Fatalf("Rotation = %d, want 270", info.Rotation)
		}
	})

	t.Run("no rotation at all", func(t *testing.T) {
		info := parseProbeJSON(t, `{
			"streams":[{"codec_type":"video","width":1080,"height":1920,
			  "r_frame_rate":"30/1"}],
			"format":{"duration":"10.0"}}`)
		if info.Rotation != 0 {
			t.Fatalf("Rotation = %d, want 0", info.Rotation)
		}
		if info.DisplayWidth() != 1080 || info.DisplayHeight() != 1920 {
			t.Fatalf("display = %dx%d, want 1080x1920", info.DisplayWidth(), info.DisplayHeight())
		}
	})
}
