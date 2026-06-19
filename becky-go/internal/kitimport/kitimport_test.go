package kitimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/sampler"
)

// writeFixture writes content to dir/name and returns the full path.
func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// touch creates an empty file so a sample resolves as present (not Missing).
func touch(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("RIFF"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// SFZ
// ---------------------------------------------------------------------------

func TestParseSFZSimpleOneShot(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "kick.wav")
	sfz := `
// a simple one-shot drum mapped to key 36 (C1)
<region>
sample=kick.wav key=36 loop_mode=one_shot
`
	path := writeFixture(t, dir, "kick.sfz", sfz)
	res, err := ParseSFZ(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sounds) != 1 {
		t.Fatalf("expected 1 sound, got %d", len(res.Sounds))
	}
	s := res.Sounds[0]
	if !s.OneShot {
		t.Error("expected OneShot true from loop_mode=one_shot")
	}
	if len(s.Layers) != 1 || len(s.Layers[0].RoundRobin) != 1 {
		t.Fatalf("expected 1 layer x 1 variant, got %+v", s.Layers)
	}
	v := s.Layers[0].RoundRobin[0]
	if v.Missing {
		t.Errorf("kick.wav exists, should not be Missing: %q", v.SamplePath)
	}
	if filepath.Base(v.SamplePath) != "kick.wav" {
		t.Errorf("sample path = %q", v.SamplePath)
	}
	if v.LoopMode != sampler.OneShot {
		t.Errorf("variant loop mode = %v", v.LoopMode)
	}
}

func TestParseSFZMultiVelocity(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "snare_soft.wav")
	touch(t, dir, "snare_hard.wav")
	sfz := `
<group> key=38
<region> sample=snare_soft.wav lovel=1 hivel=63
<region> sample=snare_hard.wav lovel=64 hivel=127
`
	path := writeFixture(t, dir, "snare.sfz", sfz)
	res, err := ParseSFZ(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sounds) != 1 {
		t.Fatalf("expected 1 sound, got %d", len(res.Sounds))
	}
	s := res.Sounds[0]
	if len(s.Layers) != 2 {
		t.Fatalf("expected 2 velocity layers, got %d: %+v", len(s.Layers), s.Layers)
	}
	// layers sorted low->high
	if s.Layers[0].VelLo != 1 || s.Layers[0].VelHi != 63 {
		t.Errorf("layer0 range = %d..%d", s.Layers[0].VelLo, s.Layers[0].VelHi)
	}
	if s.Layers[1].VelLo != 64 || s.Layers[1].VelHi != 127 {
		t.Errorf("layer1 range = %d..%d", s.Layers[1].VelLo, s.Layers[1].VelHi)
	}
	// PickLayer should route a soft hit to the soft sample.
	l, _ := sampler.PickLayer(s, 30)
	if !strings.Contains(l.RoundRobin[0].SamplePath, "soft") {
		t.Errorf("vel 30 picked %q, expected soft", l.RoundRobin[0].SamplePath)
	}
}

func TestParseSFZRoundRobin(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"rr1.wav", "rr2.wav", "rr3.wav"} {
		touch(t, dir, n)
	}
	sfz := `
<group> key=42
<region> sample=rr1.wav seq_length=3 seq_position=1
<region> sample=rr2.wav seq_length=3 seq_position=2
<region> sample=rr3.wav seq_length=3 seq_position=3
`
	path := writeFixture(t, dir, "hat.sfz", sfz)
	res, err := ParseSFZ(path)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Sounds[0]
	if len(s.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(s.Layers))
	}
	rr := s.Layers[0].RoundRobin
	if len(rr) != 3 {
		t.Fatalf("expected 3 round-robin variants, got %d", len(rr))
	}
	// ordered by seq_position
	for i, want := range []string{"rr1.wav", "rr2.wav", "rr3.wav"} {
		if filepath.Base(rr[i].SamplePath) != want {
			t.Errorf("rr[%d] = %q want %q", i, rr[i].SamplePath, want)
		}
	}
	// SelectVariant cycles deterministically through them.
	c := 0
	got := []string{}
	for i := 0; i < 4; i++ {
		v, next := sampler.SelectVariant(s.Layers[0], c)
		got = append(got, filepath.Base(v.SamplePath))
		c = next
	}
	want := []string{"rr1.wav", "rr2.wav", "rr3.wav", "rr1.wav"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cycle[%d] = %q want %q", i, got[i], want[i])
		}
	}
}

func TestParseSFZChokePair(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "chh.wav")
	touch(t, dir, "ohh.wav")
	// closed hat in group 1; open hat off_by group 1 (closed cuts open).
	sfz := `
<region> sample=chh.wav key=42 group=1
<region> sample=ohh.wav key=46 group=1 off_by=1
`
	path := writeFixture(t, dir, "hats.sfz", sfz)
	res, err := ParseSFZ(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sounds) != 2 {
		t.Fatalf("expected 2 sounds (chh, ohh), got %d", len(res.Sounds))
	}
	// sorted by key: 42 then 46
	chh, ohh := res.Sounds[0], res.Sounds[1]
	if chh.ChokeGroup != 1 {
		t.Errorf("chh choke group = %d, want 1", chh.ChokeGroup)
	}
	if ohh.ChokeGroup != 1 {
		t.Errorf("ohh choke group = %d, want 1", ohh.ChokeGroup)
	}
	if len(ohh.OffBy) != 1 || ohh.OffBy[0] != 1 {
		t.Errorf("ohh off_by = %v, want [1]", ohh.OffBy)
	}
}

func TestParseSFZWindowsBackslashPath(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "samples/kick.wav")
	// Windows-style relative path with backslash must resolve on Linux/CI.
	sfz := "<region> sample=samples\\kick.wav key=36\n"
	path := writeFixture(t, dir, "win.sfz", sfz)
	res, err := ParseSFZ(path)
	if err != nil {
		t.Fatal(err)
	}
	v := res.Sounds[0].Layers[0].RoundRobin[0]
	if v.Missing {
		t.Errorf("backslash path should resolve to an existing file; got Missing for %q", v.SamplePath)
	}
	if !strings.HasSuffix(filepath.ToSlash(v.SamplePath), "samples/kick.wav") {
		t.Errorf("resolved path = %q", v.SamplePath)
	}
}

func TestParseSFZMissingSampleTolerated(t *testing.T) {
	dir := t.TempDir()
	// no file on disk
	sfz := "<region> sample=ghost.wav key=40\n"
	path := writeFixture(t, dir, "ghost.sfz", sfz)
	res, err := ParseSFZ(path)
	if err != nil {
		t.Fatalf("a missing sample must not fail the parse: %v", err)
	}
	if len(res.Sounds) != 1 {
		t.Fatalf("expected the region to be kept, got %d sounds", len(res.Sounds))
	}
	v := res.Sounds[0].Layers[0].RoundRobin[0]
	if !v.Missing {
		t.Error("expected Missing=true for a non-existent sample")
	}
	if !anyContains(res.Notes, "missing sample") {
		t.Errorf("expected a missing-sample note, got %v", res.Notes)
	}
}

func TestParseSFZUnknownOpcodeTolerated(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "k.wav")
	sfz := `
#define $UNUSED 1
#include "shared.sfz"
<region> sample=k.wav key=36 ampeg_attack=0.001 some_future_opcode=42 pan=-50 tune=-15 volume=-3 offset=100 end=20000
`
	path := writeFixture(t, dir, "u.sfz", sfz)
	res, err := ParseSFZ(path)
	if err != nil {
		t.Fatalf("unknown opcodes must not fail the parse: %v", err)
	}
	v := res.Sounds[0].Layers[0].RoundRobin[0]
	// Known opcodes still parsed correctly alongside unknowns.
	if v.Pan != -0.5 {
		t.Errorf("pan = %v, want -0.5 (from SFZ pan=-50)", v.Pan)
	}
	if v.Tune != -15 {
		t.Errorf("tune = %d, want -15", v.Tune)
	}
	if v.Gain != -3 {
		t.Errorf("gain = %v, want -3 (from volume)", v.Gain)
	}
	if v.StartFrame != 100 || v.EndFrame != 20000 {
		t.Errorf("offset/end = %d/%d, want 100/20000", v.StartFrame, v.EndFrame)
	}
	if !anyContains(res.Notes, "preprocessor") {
		t.Errorf("expected #include/#define to be noted, got %v", res.Notes)
	}
}

func TestParseSFZGlobalGroupInheritance(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.wav")
	touch(t, dir, "b.wav")
	// global sets loop_mode; group sets key; region overrides nothing but sample.
	sfz := `
<global> loop_mode=one_shot
<group> key=50 group=2
<region> sample=a.wav
<region> sample=b.wav
`
	path := writeFixture(t, dir, "inh.sfz", sfz)
	res, err := ParseSFZ(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sounds) != 1 {
		t.Fatalf("both regions share key 50 -> 1 sound, got %d", len(res.Sounds))
	}
	s := res.Sounds[0]
	if !s.OneShot {
		t.Error("global loop_mode=one_shot should be inherited")
	}
	if s.ChokeGroup != 2 {
		t.Errorf("group choke = %d, want 2", s.ChokeGroup)
	}
	if len(s.Layers[0].RoundRobin) != 2 {
		t.Errorf("expected 2 variants under one key/vel, got %d", len(s.Layers[0].RoundRobin))
	}
}

func TestParseSFZUnreadableFile(t *testing.T) {
	if _, err := ParseSFZ(filepath.Join(t.TempDir(), "nope.sfz")); err == nil {
		t.Fatal("expected an error for a non-existent file")
	}
}

// ---------------------------------------------------------------------------
// DecentSampler
// ---------------------------------------------------------------------------

func TestParseDecentSamplerSimple(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "kick.wav")
	dsp := `<?xml version="1.0" encoding="UTF-8"?>
<DecentSampler>
  <groups>
    <group>
      <sample path="kick.wav" rootNote="36" loNote="36" hiNote="36" start="0" end="44100"/>
    </group>
  </groups>
</DecentSampler>`
	path := writeFixture(t, dir, "kick.dspreset", dsp)
	res, err := ParseDecentSampler(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sounds) != 1 {
		t.Fatalf("expected 1 sound, got %d", len(res.Sounds))
	}
	v := res.Sounds[0].Layers[0].RoundRobin[0]
	if v.Missing {
		t.Errorf("kick.wav exists, should not be Missing")
	}
	if v.PitchKeycenter != 36 {
		t.Errorf("keycenter = %d, want 36", v.PitchKeycenter)
	}
	if v.EndFrame != 44100 {
		t.Errorf("end = %d, want 44100", v.EndFrame)
	}
}

func TestParseDecentSamplerMultiVelocity(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "soft.wav")
	touch(t, dir, "hard.wav")
	dsp := `<DecentSampler><groups><group>
	  <sample path="soft.wav" rootNote="38" loVel="1" hiVel="63"/>
	  <sample path="hard.wav" rootNote="38" loVel="64" hiVel="127"/>
	</group></groups></DecentSampler>`
	path := writeFixture(t, dir, "snare.dspreset", dsp)
	res, err := ParseDecentSampler(path)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Sounds[0]
	if len(s.Layers) != 2 {
		t.Fatalf("expected 2 velocity layers, got %d", len(s.Layers))
	}
	l, _ := sampler.PickLayer(s, 100)
	if !strings.Contains(l.RoundRobin[0].SamplePath, "hard") {
		t.Errorf("vel 100 picked %q, want hard", l.RoundRobin[0].SamplePath)
	}
}

func TestParseDecentSamplerRoundRobin(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"r1.wav", "r2.wav", "r3.wav"} {
		touch(t, dir, n)
	}
	dsp := `<DecentSampler><groups><group seqMode="round_robin">
	  <sample path="r1.wav" rootNote="42" seqMode="round_robin" seqLength="3" seqPosition="1"/>
	  <sample path="r2.wav" rootNote="42" seqMode="round_robin" seqLength="3" seqPosition="2"/>
	  <sample path="r3.wav" rootNote="42" seqMode="round_robin" seqLength="3" seqPosition="3"/>
	</group></groups></DecentSampler>`
	path := writeFixture(t, dir, "hat.dspreset", dsp)
	res, err := ParseDecentSampler(path)
	if err != nil {
		t.Fatal(err)
	}
	rr := res.Sounds[0].Layers[0].RoundRobin
	if len(rr) != 3 {
		t.Fatalf("expected 3 round-robin variants, got %d", len(rr))
	}
	for i, want := range []string{"r1.wav", "r2.wav", "r3.wav"} {
		if filepath.Base(rr[i].SamplePath) != want {
			t.Errorf("rr[%d] = %q want %q", i, rr[i].SamplePath, want)
		}
	}
}

func TestParseDecentSamplerWindowsPathAndMissing(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Samples/kick.wav")
	dsp := `<DecentSampler><groups><group>
	  <sample path="Samples\kick.wav" rootNote="36"/>
	  <sample path="Samples\ghost.wav" rootNote="38"/>
	</group></groups></DecentSampler>`
	path := writeFixture(t, dir, "win.dspreset", dsp)
	res, err := ParseDecentSampler(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sounds) != 2 {
		t.Fatalf("expected 2 sounds, got %d", len(res.Sounds))
	}
	// key 36 first
	if res.Sounds[0].Layers[0].RoundRobin[0].Missing {
		t.Error("backslash path to existing file should resolve")
	}
	if !res.Sounds[1].Layers[0].RoundRobin[0].Missing {
		t.Error("ghost sample should be flagged Missing")
	}
	if !anyContains(res.Notes, "missing sample") {
		t.Errorf("expected a missing-sample note, got %v", res.Notes)
	}
}

func TestParseDecentSamplerTuningToCents(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "t.wav")
	dsp := `<DecentSampler><groups><group>
	  <sample path="t.wav" rootNote="60" tuning="0.5" pan="-50"/>
	</group></groups></DecentSampler>`
	path := writeFixture(t, dir, "t.dspreset", dsp)
	res, err := ParseDecentSampler(path)
	if err != nil {
		t.Fatal(err)
	}
	v := res.Sounds[0].Layers[0].RoundRobin[0]
	if v.Tune != 50 { // 0.5 semitones -> 50 cents
		t.Errorf("tune = %d cents, want 50", v.Tune)
	}
	if v.Pan != -0.5 {
		t.Errorf("pan = %v, want -0.5", v.Pan)
	}
}

func TestParseDecentSamplerMalformed(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "bad.dspreset", "<DecentSampler><groups><group>")
	if _, err := ParseDecentSampler(path); err == nil {
		t.Fatal("expected an error for malformed XML")
	}
}

func anyContains(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}
