package edl

import (
	"strings"
	"testing"
)

const impEps = 1e-6

func impClose(a, b float64) bool { return a-b < impEps && b-a < impEps }

// Real header + rows from a Vegas Pro "EDL TXT" export (post_constantly.txt),
// trimmed to the columns that matter plus an out-of-order and an AUDIO row.
const vegasFixture = `"ID";"Track";"StartTime";"Length";"PlayRate";"Locked";"Normalized";"StretchMethod";"Looped";"OnRuler";"MediaType";"FileName";"Stream";"StreamStart";"StreamLength"
1; 1; 9442.7763; 9142.4760; 1.000000; FALSE; FALSE; 0; TRUE; FALSE; VIDEO; "X:\v\a.mp4"; 0; 834.1675; 299299.0991
3; 1; 19719.7201; 2902.9030; 1.000000; FALSE; FALSE; 0; TRUE; FALSE; VIDEO; "X:\v\a.mp4"; 0; 16983.6507; 283149.6159
2; 1; 18585.2523; 1134.4678; 1.000000; FALSE; FALSE; 0; TRUE; FALSE; VIDEO; "X:\v\a.mp4"; 0; 12779.4464; 287353.8202
1; 2; 9442.7763; 9142.4760; 1.000000; FALSE; FALSE; 0; TRUE; FALSE; AUDIO; "X:\v\a.mp4"; 0; 834.1675; 299299.0991
`

func TestParseVegasTXTConvertsMillisecondsAndOrders(t *testing.T) {
	clips, fps, err := parseVegasTXT(strings.NewReader(vegasFixture))
	if err != nil {
		t.Fatalf("parseVegasTXT: %v", err)
	}
	if fps != 0 {
		t.Errorf("fps = %v, want 0 (Vegas EDL TXT carries no rate)", fps)
	}
	// AUDIO row must be dropped; the 3 VIDEO rows kept and sorted by StartTime.
	if len(clips) != 3 {
		t.Fatalf("clips = %d, want 3 (AUDIO row must be filtered out)", len(clips))
	}

	// Row 1: StreamStart 834.1675ms -> 0.8341675s; out = (834.1675+9142.4760)/1000.
	if !impClose(clips[0].In, 0.8341675) {
		t.Errorf("clip 0 In = %.7f, want 0.8341675 (ms -> s)", clips[0].In)
	}
	if !impClose(clips[0].Out, 9.9766435) {
		t.Errorf("clip 0 Out = %.7f, want 9.9766435 (StreamStart+Length, NOT StreamLength)", clips[0].Out)
	}
	// Sorted by timeline StartTime, so the ID=2 row lands between 1 and 3.
	if !impClose(clips[1].In, 12.7794464) {
		t.Errorf("clip 1 In = %.7f, want 12.7794464 — rows must sort by StartTime, not file order", clips[1].In)
	}
	if !impClose(clips[2].In, 16.9836507) {
		t.Errorf("clip 2 In = %.7f, want 16.9836507", clips[2].In)
	}
	if clips[0].Source != `X:\v\a.mp4` {
		t.Errorf("source = %q, want unquoted absolute path", clips[0].Source)
	}
}

func TestParseVegasTXTFramesAreExact(t *testing.T) {
	// The whole point of importing rather than eyeballing: Vegas writes cut
	// points that land on exact 29.97 frames. 9142.4760ms / (1001/30) ms = 274.
	clips, _, err := parseVegasTXT(strings.NewReader(vegasFixture))
	if err != nil {
		t.Fatalf("parseVegasTXT: %v", err)
	}
	const frameMS = 1001.0 / 30.0 // 33.3667ms at 29.97
	dur := (clips[0].Out - clips[0].In) * 1000.0
	frames := dur / frameMS
	if d := frames - 274.0; d > 1e-3 || d < -1e-3 {
		t.Errorf("clip 0 = %.4f frames, want exactly 274 at 29.97", frames)
	}
}

func TestParseVegasTXTAudioOnlyFallback(t *testing.T) {
	audioOnly := `"ID";"StartTime";"Length";"MediaType";"FileName";"StreamStart"
1; 0.0000; 1000.0000; AUDIO; "X:\v\a.wav"; 500.0000
`
	clips, _, err := parseVegasTXT(strings.NewReader(audioOnly))
	if err != nil {
		t.Fatalf("parseVegasTXT: %v", err)
	}
	if len(clips) != 1 {
		t.Fatalf("clips = %d, want 1 (audio-only edit must still import)", len(clips))
	}
	if !impClose(clips[0].In, 0.5) || !impClose(clips[0].Out, 1.5) {
		t.Errorf("clip = [%.4f,%.4f], want [0.5000,1.5000]", clips[0].In, clips[0].Out)
	}
}

func TestParseVegasTXTMissingColumnIsAnError(t *testing.T) {
	_, _, err := parseVegasTXT(strings.NewReader("\"ID\";\"StartTime\"\n1; 0.0\n"))
	if err == nil {
		t.Fatal("want an error naming the missing column, got nil")
	}
	if !strings.Contains(err.Error(), "length") {
		t.Errorf("error = %v, want it to name the missing column", err)
	}
}

func TestSplitVegasRowRespectsQuotedSemicolon(t *testing.T) {
	got := splitVegasRow(`1; 2; VIDEO; "X:\odd;name\a.mp4"; 3`)
	want := []string{"1", "2", "VIDEO", `X:\odd;name\a.mp4`, "3"}
	if len(got) != len(want) {
		t.Fatalf("fields = %d (%q), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The bin <clip> outside the sequence spans the whole file (in=0 out=8996) and
// must NOT be imported as an event. The sequence holds the real cuts, the second
// of which refers to the file by id alone.
const fcp7Fixture = `<?xml version="1.0"?>
<xmeml version="5">
 <project>
  <children>
   <clip id="a.mp4">
    <media><video><track><clipitem id="bin1">
      <in>0</in><out>8996</out><start>0</start>
      <file id="a"><pathurl>file://localhost/X:/v/a.mp4</pathurl></file>
    </clipitem></track></video></media>
   </clip>
   <sequence>
    <rate><ntsc>TRUE</ntsc><timebase>30</timebase></rate>
    <media>
     <video><track>
      <clipitem id="c2">
       <name>second</name>
       <in>600</in><out>900</out><start>300</start>
       <file id="a"/>
      </clipitem>
      <clipitem id="c1">
       <name>first</name>
       <in>25</in><out>325</out><start>0</start>
       <file id="a"><pathurl>file://localhost/X:/v/a.mp4</pathurl></file>
      </clipitem>
     </track></video>
     <audio><track>
      <clipitem id="a1"><in>25</in><out>325</out><start>0</start><file id="a"/></clipitem>
     </track></audio>
    </media>
   </sequence>
  </children>
 </project>
</xmeml>`

func TestParseFCP7XMLSkipsBinClipAndUsesNTSCRate(t *testing.T) {
	clips, fps, err := parseFCP7XML(strings.NewReader(fcp7Fixture))
	if err != nil {
		t.Fatalf("parseFCP7XML: %v", err)
	}
	if !impClose(fps, 30000.0/1001.0) {
		t.Errorf("fps = %.6f, want 29.970030 (ntsc TRUE + timebase 30)", fps)
	}
	if len(clips) != 2 {
		t.Fatalf("clips = %d, want 2 — the bin <clip> full-file item must be skipped", len(clips))
	}

	// Ordered by timeline <start>, so "first" (start 0) comes before "second".
	if clips[0].Label != "first" || clips[1].Label != "second" {
		t.Errorf("order = [%s %s], want [first second] (sort by <start>)", clips[0].Label, clips[1].Label)
	}
	// Frames -> seconds at the true NTSC rate.
	if !impClose(clips[0].In, 25.0/(30000.0/1001.0)) {
		t.Errorf("clip 0 In = %.6f, want frames/29.97", clips[0].In)
	}
	if !impClose(clips[0].Out, 325.0/(30000.0/1001.0)) {
		t.Errorf("clip 0 Out = %.6f, want frames/29.97", clips[0].Out)
	}
	// The second clipitem carries only <file id="a"/> — the id table must resolve it.
	if clips[1].Source != `X:\v\a.mp4` {
		t.Errorf("clip 1 source = %q, want it resolved from the file-id table", clips[1].Source)
	}
}

func TestParseFCP7XMLNoSequenceIsAnError(t *testing.T) {
	_, _, err := parseFCP7XML(strings.NewReader(`<?xml version="1.0"?><xmeml version="5"><project/></xmeml>`))
	if err == nil || !strings.Contains(err.Error(), "sequence") {
		t.Fatalf("err = %v, want an error naming <sequence>", err)
	}
}

func TestResolveFPS(t *testing.T) {
	cases := []struct {
		rate xRate
		want float64
	}{
		{xRate{Timebase: 30, NTSC: "TRUE"}, 30000.0 / 1001.0},
		{xRate{Timebase: 30, NTSC: "true"}, 30000.0 / 1001.0},
		{xRate{Timebase: 30, NTSC: "FALSE"}, 30},
		{xRate{Timebase: 25, NTSC: ""}, 25},
		{xRate{Timebase: 0, NTSC: "TRUE"}, 0},
	}
	for _, c := range cases {
		if got := resolveFPS(c.rate); !impClose(got, c.want) {
			t.Errorf("resolveFPS(%+v) = %v, want %v", c.rate, got, c.want)
		}
	}
}

func TestPathFromURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"file://localhost/X:/v/a.mp4", `X:\v\a.mp4`},
		{"file:///X:/v/a.mp4", `X:\v\a.mp4`},
		{"file://localhost/X:/v/my%20clip.mp4", `X:\v\my clip.mp4`},
		{"", ""},
	}
	for _, c := range cases {
		if got := pathFromURL(c.in); got != c.want {
			t.Errorf("pathFromURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
