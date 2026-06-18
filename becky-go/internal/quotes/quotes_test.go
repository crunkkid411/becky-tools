package quotes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// goldenSRT is a small synthesized transcript with clear sentence structure and
// one passage ("press charges") that is unintelligible without its prior
// sentence ("two restraining orders"). Timestamps are deliberately irregular so
// timestamp-identity assertions are meaningful.
const goldenSRT = `1
00:00:01,000 --> 00:00:03,500
Good evening everyone and welcome to the stream.

2
00:00:03,600 --> 00:00:06,200
I want to talk about something serious tonight.

3
00:00:06,300 --> 00:00:09,900
She filed two restraining orders against me last year.

4
00:00:10,000 --> 00:00:12,750
So I told her I would press charges if she kept it up.

5
00:00:12,800 --> 00:00:15,000
Anyway let's move on to the games.

6
00:00:15,100 --> 00:00:18,400
Thanks for watching and don't forget to subscribe.
`

func mustCues(t *testing.T) []Cue {
	t.Helper()
	cues := ParseSRT(goldenSRT)
	if len(cues) != 6 {
		t.Fatalf("expected 6 cues, got %d", len(cues))
	}
	return cues
}

// boundarySet builds the set of every verbatim cue boundary string for the
// timestamp-identity assertion.
func boundarySet(cues []Cue) map[string]bool {
	set := map[string]bool{}
	for _, c := range cues {
		set[c.StartRaw] = true
		set[c.EndRaw] = true
	}
	return set
}

func TestParseSRT_VerbatimTimecodes(t *testing.T) {
	cues := mustCues(t)
	if cues[0].StartRaw != "00:00:01,000" || cues[0].EndRaw != "00:00:03,500" {
		t.Errorf("cue 1 raw timecodes wrong: %q --> %q", cues[0].StartRaw, cues[0].EndRaw)
	}
	if cues[2].Text != "She filed two restraining orders against me last year." {
		t.Errorf("cue 3 text wrong: %q", cues[2].Text)
	}
	if cues[3].Index != 4 {
		t.Errorf("cue 4 Index = %d, want 4", cues[3].Index)
	}
}

func TestParseSRT_BOMandCRLF(t *testing.T) {
	withNoise := "\ufeff" + strings.ReplaceAll(goldenSRT, "\n", "\r\n")
	cues := ParseSRT(withNoise)
	if len(cues) != 6 {
		t.Fatalf("BOM+CRLF parse: expected 6 cues, got %d", len(cues))
	}
	if cues[0].StartRaw != "00:00:01,000" {
		t.Errorf("BOM not stripped / timecode corrupted: %q", cues[0].StartRaw)
	}
}

func TestExactSelector_LiteralOnly(t *testing.T) {
	cues := mustCues(t)
	sel := NewExactSelector(cues, "press charges|two restraining orders|nonexistent phrase here")
	anchors, err := sel.Select(context.Background(), "", "")
	if err != nil {
		t.Fatalf("exact select: %v", err)
	}
	if len(anchors) != 2 {
		t.Fatalf("expected 2 exact anchors, got %d: %+v", len(anchors), anchors)
	}
}

func TestRun_Exact_TimestampIdentity(t *testing.T) {
	cues := mustCues(t)
	set := boundarySet(cues)
	sel := NewExactSelector(cues, "press charges|restraining orders")
	sum, err := Run(context.Background(), cues, Options{Selector: sel})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(sum.Regions) == 0 {
		t.Fatal("expected regions from exact selection")
	}
	for _, r := range sum.Regions {
		if !set[r.Start] {
			t.Errorf("emitted start %q is not a real cue boundary", r.Start)
		}
		if !set[r.End] {
			t.Errorf("emitted end %q is not a real cue boundary", r.End)
		}
	}
	// the rendered SRT must also only contain real boundary strings.
	for _, line := range strings.Split(sum.SRT(), "\n") {
		if !strings.Contains(line, "-->") {
			continue
		}
		parts := strings.Split(line, "-->")
		start := strings.TrimSpace(parts[0])
		end := strings.TrimSpace(parts[1])
		if !set[start] || !set[end] {
			t.Errorf("SRT timing line has a non-boundary timestamp: %q", line)
		}
	}
}

func TestRun_NoCommentLinesInSRT(t *testing.T) {
	cues := mustCues(t)
	sel := NewExactSelector(cues, "press charges")
	sum, _ := Run(context.Background(), cues, Options{Selector: sel})
	for _, line := range strings.Split(sum.SRT(), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "//") {
			t.Errorf("SRT contains a comment line: %q", line)
		}
	}
}

func TestJSONSelector_ParsesQuoteAndCue(t *testing.T) {
	data := []byte(`{"anchors":[
		{"quote":"press charges","because":"threat"},
		{"cue":2,"because":"context"},
		{"because":"unusable - dropped"}
	]}`)
	js, err := NewJSONSelector(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	anchors, _ := js.Select(context.Background(), "", "")
	if len(anchors) != 2 {
		t.Fatalf("expected 2 usable anchors (third has neither quote nor cue), got %d", len(anchors))
	}
}

func TestRun_JSONSelector_CueAnchorResolves(t *testing.T) {
	cues := mustCues(t)
	// cue index 3 (0-based) == source cue 4 "press charges" line.
	data := []byte(`{"anchors":[{"cue":3,"because":"the threat"}]}`)
	js, _ := NewJSONSelector(data)
	sum, err := Run(context.Background(), cues, Options{Selector: js})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(sum.Regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(sum.Regions))
	}
	if !strings.Contains(sum.Regions[0].Text, "press charges") {
		t.Errorf("cue anchor resolved to wrong text: %q", sum.Regions[0].Text)
	}
	if sum.Regions[0].StartCue != 4 {
		t.Errorf("start_cue = %d, want 4 (1-based)", sum.Regions[0].StartCue)
	}
}

// fakeExpander says "yes" only for a configured neighbor substring, "no"
// otherwise — deterministic, no model.
type fakeExpander struct {
	yesIfContains string
	calls         int
}

func (f *fakeExpander) NeedsContext(_ context.Context, _ string, neighbor string) (bool, error) {
	f.calls++
	if f.yesIfContains == "" {
		return false, nil
	}
	return strings.Contains(strings.ToLower(neighbor), f.yesIfContains), nil
}

func TestRun_Expansion_IncludesExactNeighborNoRunaway(t *testing.T) {
	cues := mustCues(t)
	// Anchor the "press charges" sentence (cue 4). Its prior sentence
	// ("two restraining orders", cue 3) gives the needed context; the expander
	// says yes ONLY to that phrase, so expansion must include cue 3 and STOP.
	data := []byte(`{"anchors":[{"quote":"press charges if she kept it up","because":"threat"}]}`)
	js, _ := NewJSONSelector(data)
	exp := &fakeExpander{yesIfContains: "restraining orders"}
	sum, err := Run(context.Background(), cues, Options{
		Selector: js,
		Expander: exp,
		Caps:     ExpandCaps{MaxSentencesPerSide: 4, MaxRegionSeconds: 90},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(sum.Regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(sum.Regions))
	}
	r := sum.Regions[0]
	if !strings.Contains(r.Text, "restraining orders") {
		t.Errorf("expansion did not include the needed prior sentence: %q", r.Text)
	}
	if !strings.Contains(r.Text, "press charges") {
		t.Errorf("region lost its anchor sentence: %q", r.Text)
	}
	// no runaway: the unrelated cue 5 ("move on to the games") must NOT be in.
	if strings.Contains(r.Text, "move on to the games") {
		t.Errorf("runaway expansion pulled in an unrelated sentence: %q", r.Text)
	}
	if r.ExpandedBefore < 1 {
		t.Errorf("expected expanded_before >= 1, got %d", r.ExpandedBefore)
	}
}

func TestRun_Expansion_OffWithoutExpander(t *testing.T) {
	cues := mustCues(t)
	data := []byte(`{"anchors":[{"quote":"press charges if she kept it up"}]}`)
	js, _ := NewJSONSelector(data)
	sum, _ := Run(context.Background(), cues, Options{Selector: js}) // no Expander
	if len(sum.Regions) != 1 {
		t.Fatalf("expected 1 region, got %d", len(sum.Regions))
	}
	if strings.Contains(sum.Regions[0].Text, "restraining orders") {
		t.Errorf("expansion ran without an Expander: %q", sum.Regions[0].Text)
	}
}

func TestRun_Merge_AdjacentAnchorsBecomeOneRegion(t *testing.T) {
	cues := mustCues(t)
	// Two anchors on adjacent cues (cue 3 and cue 4); with neither expanding they
	// touch (StartCue 4 <= EndCue 3 + 1) and merge into one region.
	data := []byte(`{"anchors":[
		{"quote":"two restraining orders against me"},
		{"quote":"press charges if she kept it up"}
	]}`)
	js, _ := NewJSONSelector(data)
	sum, err := Run(context.Background(), cues, Options{Selector: js})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sum.AfterMerge != 1 {
		t.Fatalf("expected 1 region after merge, got %d (%+v)", sum.AfterMerge, sum.Regions)
	}
	if sum.SelectedCount != 2 {
		t.Errorf("expected selected=2 before merge, got %d", sum.SelectedCount)
	}
}

func TestRun_Reproducibility_IdenticalTwice(t *testing.T) {
	cues := mustCues(t)
	sel := NewExactSelector(cues, "press charges|restraining orders|welcome to the stream")
	a, _ := Run(context.Background(), cues, Options{Selector: sel})
	sel2 := NewExactSelector(cues, "press charges|restraining orders|welcome to the stream")
	b, _ := Run(context.Background(), cues, Options{Selector: sel2})
	if a.SRT() != b.SRT() {
		t.Errorf("non-reproducible output:\nA:\n%s\nB:\n%s", a.SRT(), b.SRT())
	}
}

func TestRun_SourceIntegrity_SRTUnchanged(t *testing.T) {
	before := sha256.Sum256([]byte(goldenSRT))
	cues := ParseSRT(goldenSRT)
	sel := NewExactSelector(cues, "press charges")
	_, _ = Run(context.Background(), cues, Options{Selector: sel})
	after := sha256.Sum256([]byte(goldenSRT))
	if hex.EncodeToString(before[:]) != hex.EncodeToString(after[:]) {
		t.Error("source transcript content changed during run")
	}
}

func TestRun_Unmatched_QuoteNotInTranscript(t *testing.T) {
	cues := mustCues(t)
	data := []byte(`{"anchors":[{"quote":"this phrase does not appear anywhere at all"}]}`)
	js, _ := NewJSONSelector(data)
	sum, _ := Run(context.Background(), cues, Options{Selector: js})
	if len(sum.Regions) != 0 {
		t.Errorf("expected no regions for an unmatched quote, got %d", len(sum.Regions))
	}
	if len(sum.Unmatched) != 1 {
		t.Errorf("expected 1 unmatched quote recorded, got %d", len(sum.Unmatched))
	}
}
