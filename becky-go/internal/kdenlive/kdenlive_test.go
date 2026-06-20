package kdenlive

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reparse round-trips the marshalled XML back through encoding/xml so tests
// assert on structure, not on string formatting.
func reparse(t *testing.T, p *Project) mltDoc {
	t.Helper()
	data, err := p.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var doc mltDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("re-unmarshal produced invalid XML: %v\n%s", err, data)
	}
	return doc
}

func newTwoCutProject() *Project {
	src := `E:/footage/clip.mp4`
	p := NewProject("case")
	p.Width, p.Height, p.FPS = 1920, 1080, 30
	p.SetSource(Source{Path: src, LengthSec: 60, FPS: 30})
	p.AddClip(Clip{Source: src, In: 0, Out: 10, Name: "intro"})
	p.AddClip(Clip{Source: src, In: 30, Out: 40})
	return p
}

func TestMarshal_BasicStructure(t *testing.T) {
	doc := reparse(t, newTwoCutProject())

	if doc.Version != meltVersion {
		t.Errorf("mlt version = %q, want %q", doc.Version, meltVersion)
	}
	if doc.Producer != "main_bin" {
		t.Errorf("mlt producer attr = %q, want main_bin", doc.Producer)
	}
	if len(doc.Producers) != 1 {
		t.Fatalf("producers = %d, want 1 (single distinct source)", len(doc.Producers))
	}
	if len(doc.Playlists) != 1 || len(doc.Playlists[0].Entries) != 2 {
		t.Fatalf("want 1 playlist with 2 entries, got %d playlists", len(doc.Playlists))
	}
	if len(doc.Tractor.Tracks) != 1 || doc.Tractor.Tracks[0].Producer != "playlist0" {
		t.Errorf("tractor must bind playlist0 as the single track, got %+v", doc.Tractor.Tracks)
	}
}

// TestMarshal_NamespaceDeclared guards the load-bearing gotcha: strict Gen-2
// kdenlive parsers reject a project missing xmlns:kdenlive on <mlt>.
func TestMarshal_NamespaceDeclared(t *testing.T) {
	data, err := newTwoCutProject().Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `xmlns:kdenlive="https://kdenlive.org/projectfile"`) {
		t.Errorf("output missing xmlns:kdenlive namespace declaration:\n%s", s)
	}
}

// TestMarshal_ValidatingAvformat guards the other gotcha: producers must use the
// VALIDATING avformat reader (not avformat-novalidate) or cuts collapse to 1 frame.
func TestMarshal_ValidatingAvformat(t *testing.T) {
	doc := reparse(t, newTwoCutProject())
	prod := doc.Producers[0]

	var service, resource, length string
	for _, pr := range prod.Properties {
		switch pr.Name {
		case "mlt_service":
			service = pr.Value
		case "resource":
			resource = pr.Value
		case "length":
			length = pr.Value
		}
	}
	if service != "avformat" {
		t.Errorf("mlt_service = %q, want avformat (validating reader)", service)
	}
	if strings.Contains(service, "novalidate") {
		t.Errorf("mlt_service must NOT be avformat-novalidate (collapses cuts to 1 frame)")
	}
	if resource == "" {
		t.Errorf("producer missing a resource property")
	}
	if length == "" || length == "0" {
		t.Errorf("producer length must be a real frame count, got %q", length)
	}
}

func TestMarshal_FrameMath(t *testing.T) {
	doc := reparse(t, newTwoCutProject())
	got := doc.Playlists[0].Entries

	// Clip 1: 0..10s @30 -> in 0, out 299 (inclusive last frame of a 300-frame cut).
	if got[0].In != 0 || got[0].Out != 299 {
		t.Errorf("entry0 = in %d out %d, want in 0 out 299", got[0].In, got[0].Out)
	}
	// Clip 2: 30..40s @30 -> in 900, out 1199.
	if got[1].In != 900 || got[1].Out != 1199 {
		t.Errorf("entry1 = in %d out %d, want in 900 out 1199", got[1].In, got[1].Out)
	}
	// Producer length: 60s @30 -> 1800 frames; out is inclusive last (1799).
	if doc.Producers[0].Out != 1799 {
		t.Errorf("producer out = %d, want 1799 (1800 frames - 1)", doc.Producers[0].Out)
	}
}

func TestMarshal_MultipleSourcesDistinctProducers(t *testing.T) {
	p := NewProject("multi")
	p.Width, p.Height, p.FPS = 1280, 720, 25
	a, b := `X:/a.mp4`, `X:/b.mp4`
	p.SetSource(Source{Path: a, LengthSec: 20, FPS: 25})
	p.SetSource(Source{Path: b, LengthSec: 20, FPS: 25})
	p.AddClip(Clip{Source: a, In: 1, Out: 3})
	p.AddClip(Clip{Source: b, In: 2, Out: 5})
	p.AddClip(Clip{Source: a, In: 10, Out: 12}) // reuse a -> still producer0

	doc := reparse(t, p)
	if len(doc.Producers) != 2 {
		t.Fatalf("distinct sources should yield 2 producers, got %d", len(doc.Producers))
	}
	if doc.Producers[0].ID != "producer0" || doc.Producers[1].ID != "producer1" {
		t.Errorf("producer ids not deterministic first-seen: %q,%q", doc.Producers[0].ID, doc.Producers[1].ID)
	}
	ents := doc.Playlists[0].Entries
	if ents[0].Producer != "producer0" || ents[1].Producer != "producer1" || ents[2].Producer != "producer0" {
		t.Errorf("entry producer refs wrong: %q,%q,%q", ents[0].Producer, ents[1].Producer, ents[2].Producer)
	}
}

func TestMarshal_Deterministic(t *testing.T) {
	a, err1 := newTwoCutProject().Marshal()
	b, err2 := newTwoCutProject().Marshal()
	if err1 != nil || err2 != nil {
		t.Fatalf("marshal errors: %v %v", err1, err2)
	}
	if string(a) != string(b) {
		t.Errorf("Marshal is not deterministic for identical input")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		build   func() *Project
		wantErr bool
	}{
		{"no clips", func() *Project { return NewProject("empty") }, true},
		{"empty source", func() *Project {
			p := NewProject("x")
			p.AddClip(Clip{Source: "  ", In: 0, Out: 1})
			return p
		}, true},
		{"out <= in", func() *Project {
			p := NewProject("x")
			p.AddClip(Clip{Source: "a.mp4", In: 5, Out: 5})
			return p
		}, true},
		{"valid", func() *Project {
			p := NewProject("x")
			p.AddClip(Clip{Source: "a.mp4", In: 0, Out: 1})
			return p
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.build().Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestMarshal_RejectsBadProject(t *testing.T) {
	if _, err := NewProject("empty").Marshal(); err == nil {
		t.Errorf("Marshal should reject an empty project")
	}
}

func TestSecToFrame(t *testing.T) {
	cases := []struct {
		sec  float64
		fps  float64
		want int
	}{
		{0, 30, 0},
		{1, 30, 30},
		{10, 30, 300},
		{1.0 / 30.0, 30, 1},
		{-5, 30, 0},   // negative clamps to 0
		{2, 0, 60},    // fps<=0 -> DefaultFPS 30
		{1.5, 25, 38}, // 37.5 rounds to 38
	}
	for _, c := range cases {
		if got := secToFrame(c.sec, c.fps); got != c.want {
			t.Errorf("secToFrame(%v,%v) = %d, want %d", c.sec, c.fps, got, c.want)
		}
	}
}

func TestProducerLengthFrames_FallbackWhenUnknown(t *testing.T) {
	// No SetSource -> unknown length -> cover farthest cut (40s) + 1s pad = 41s @30.
	p := NewProject("x")
	src := "a.mp4"
	p.FPS = 30
	p.AddClip(Clip{Source: src, In: 0, Out: 10})
	p.AddClip(Clip{Source: src, In: 35, Out: 40})
	got := p.producerLengthFrames(src, 30)
	want := secToFrame(41, 30) // 1230
	if got != want {
		t.Errorf("producerLengthFrames fallback = %d, want %d", got, want)
	}
}

func TestResourcePath_ForwardSlashes(t *testing.T) {
	if got := resourcePath(`E:\footage\clip.mp4`); got != `E:/footage/clip.mp4` {
		t.Errorf("resourcePath backslash = %q, want forward slashes", got)
	}
	if got := resourcePath(`  /home/u/clip.mp4  `); got != `/home/u/clip.mp4` {
		t.Errorf("resourcePath should trim + keep POSIX path, got %q", got)
	}
}

func TestClipNameForSource(t *testing.T) {
	clips := []Clip{
		{Source: `X:/dir/a.mp4`, In: 0, Out: 1},
		{Source: `X:/dir/a.mp4`, In: 2, Out: 3, Name: "named"},
	}
	if got := clipNameForSource(clips, `X:/dir/a.mp4`); got != "named" {
		t.Errorf("clipNameForSource should prefer an explicit Name, got %q", got)
	}
	if got := clipNameForSource([]Clip{{Source: `X:/dir/b.mp4`}}, `X:/dir/b.mp4`); got != "b.mp4" {
		t.Errorf("clipNameForSource fallback to basename = %q, want b.mp4", got)
	}
}

func TestMeltArgs(t *testing.T) {
	args := meltArgs("proj.kdenlive", "out.mp4", "", "")
	want := []string{"proj.kdenlive", "-consumer", "avformat:out.mp4", "vcodec=h264_nvenc", "acodec=aac"}
	if len(args) != len(want) {
		t.Fatalf("meltArgs len = %d, want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("meltArgs[%d] = %q, want %q", i, args[i], want[i])
		}
	}
	// Explicit codecs honored.
	got := meltArgs("p", "o", "libx264", "pcm_s16le")
	if got[3] != "vcodec=libx264" || got[4] != "acodec=pcm_s16le" {
		t.Errorf("meltArgs did not honor explicit codecs: %v", got)
	}
}

func TestWriteProject_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "p.kdenlive")
	if err := WriteProject(newTwoCutProject(), out); err != nil {
		t.Fatalf("WriteProject: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var doc mltDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("written file is not valid XML: %v", err)
	}
	if len(doc.Producers) != 1 {
		t.Errorf("round-tripped file lost producers")
	}
}

func TestFindMelt_EnvOverride(t *testing.T) {
	// Point BECKY_MELT at a real existing file (this test binary) so FindMelt
	// resolves it without needing melt installed in CI.
	self, err := os.Executable()
	if err != nil {
		t.Skip("cannot resolve test executable")
	}
	t.Setenv("BECKY_MELT", self)
	got, err := FindMelt()
	if err != nil {
		t.Fatalf("FindMelt with BECKY_MELT set: %v", err)
	}
	if got != self {
		t.Errorf("FindMelt = %q, want the BECKY_MELT override %q", got, self)
	}
}

func TestFindKdenlive_EnvOverride(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip("cannot resolve test executable")
	}
	t.Setenv("BECKY_KDENLIVE", self)
	got, err := FindKdenlive()
	if err != nil {
		t.Fatalf("FindKdenlive with BECKY_KDENLIVE set: %v", err)
	}
	if got != self {
		t.Errorf("FindKdenlive = %q, want override %q", got, self)
	}
}
