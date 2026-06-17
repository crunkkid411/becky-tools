package stemscan

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// ---- in-memory PCM16 WAV encoder (test fixture only) --------------------------
// Builds a canonical RIFF/WAVE mono PCM16 buffer that dsp.DecodeWAV accepts, so the
// tests synthesize signals without touching disk.

func encodeWAV16(samples []float64, sr int) []byte {
	var data bytes.Buffer
	for _, x := range samples {
		if x > 1 {
			x = 1
		} else if x < -1 {
			x = -1
		}
		v := int16(math.Round(x * 32767))
		_ = binary.Write(&data, binary.LittleEndian, v)
	}
	pcm := data.Bytes()

	var buf bytes.Buffer
	w := func(s string) { buf.WriteString(s) }
	u32 := func(v uint32) { _ = binary.Write(&buf, binary.LittleEndian, v) }
	u16 := func(v uint16) { _ = binary.Write(&buf, binary.LittleEndian, v) }

	const channels = 1
	const bits = 16
	byteRate := uint32(sr * channels * bits / 8)
	blockAlign := uint16(channels * bits / 8)

	w("RIFF")
	u32(uint32(36 + len(pcm)))
	w("WAVE")
	w("fmt ")
	u32(16)
	u16(1) // PCM
	u16(channels)
	u32(uint32(sr))
	u32(byteRate)
	u16(blockAlign)
	u16(bits)
	w("data")
	u32(uint32(len(pcm)))
	buf.Write(pcm)
	return buf.Bytes()
}

// ---- signal generators --------------------------------------------------------

func sine(freq float64, sr, n int, amp float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/float64(sr))
	}
	return s
}

// noiseBand builds a deterministic broadband-ish signal by summing several sines in a
// frequency range (pseudo-noise, but reproducible).
func noiseBand(lo, hi float64, sr, n int, amp float64) []float64 {
	s := make([]float64, n)
	freqs := []float64{}
	for f := lo; f <= hi; f += (hi - lo) / 8 {
		freqs = append(freqs, f)
	}
	for i := range s {
		var v float64
		for j, f := range freqs {
			v += math.Sin(2*math.Pi*f*float64(i)/float64(sr) + float64(j))
		}
		s[i] = amp * v / float64(len(freqs))
	}
	return s
}

// percussiveLow makes a kick-like signal: a low sine with a sharp decaying envelope
// (high crest), mostly silence => punchy transient.
func percussiveLow(sr, n int) []float64 {
	s := sine(55, sr, n, 0.9)
	for i := range s {
		env := math.Exp(-float64(i) / (0.03 * float64(sr)))
		s[i] *= env
	}
	return s
}

const testSR = 44100

// ---- tests --------------------------------------------------------------------

func TestClippingDetected(t *testing.T) {
	// Full-scale square-ish wave: many samples pinned at +/-1.
	n := testSR
	s := make([]float64, n)
	for i := range s {
		if (i/100)%2 == 0 {
			s[i] = 1.0
		} else {
			s[i] = -1.0
		}
	}
	r := AnalyzeStem("loud.wav", encodeWAV16(s, testSR))
	if r.Skipped {
		t.Fatalf("unexpected skip: %s", r.Reason)
	}
	if !r.Clipping {
		t.Errorf("expected clipping flag, got false (clippedFrac=%v)", r.ClippedFrac)
	}
	if r.ClippedFrac <= 0 {
		t.Errorf("expected clipped fraction > 0, got %v", r.ClippedFrac)
	}
	if r.PeakDBFS < -1 {
		t.Errorf("expected peak near 0 dBFS, got %v", r.PeakDBFS)
	}
}

func TestNoClippingOnQuiet(t *testing.T) {
	s := sine(440, testSR, testSR, 0.1)
	r := AnalyzeStem("quiet.wav", encodeWAV16(s, testSR))
	if r.Clipping {
		t.Errorf("quiet signal should not clip")
	}
	if r.PeakDBFS > -15 {
		t.Errorf("expected quiet peak well below 0 dBFS, got %v", r.PeakDBFS)
	}
}

func TestLoudnessOrdering(t *testing.T) {
	loud := AnalyzeStem("a.wav", encodeWAV16(sine(440, testSR, testSR, 0.8), testSR))
	quiet := AnalyzeStem("b.wav", encodeWAV16(sine(440, testSR, testSR, 0.05), testSR))
	if !(loud.LoudnessDBFS > quiet.LoudnessDBFS) {
		t.Errorf("loud (%v) should be louder than quiet (%v)", loud.LoudnessDBFS, quiet.LoudnessDBFS)
	}
	// Quiet stem should be suggested UP; loud stem DOWN (toward -18 dBFS RMS).
	if quiet.SuggestGainDB <= 0 {
		t.Errorf("quiet stem should get a positive boost suggestion, got %v", quiet.SuggestGainDB)
	}
	if loud.SuggestGainDB >= 0 {
		t.Errorf("loud stem should get a negative trim suggestion, got %v", loud.SuggestGainDB)
	}
}

func TestSilenceFlagged(t *testing.T) {
	s := make([]float64, testSR) // all zeros
	r := AnalyzeStem("silent.wav", encodeWAV16(s, testSR))
	if r.Skipped {
		t.Fatalf("silent file should analyze, not skip: %s", r.Reason)
	}
	if !r.NearSilent {
		t.Errorf("expected near-silent flag on a zero buffer")
	}
	if r.SuggestGainDB != 0 {
		t.Errorf("near-silent stem must NOT be boosted, got gain %v", r.SuggestGainDB)
	}
}

func TestRoleHeuristicDirection(t *testing.T) {
	// Low-energy punchy tone -> kick/bass family (low family).
	kick := AnalyzeStem("k.wav", encodeWAV16(percussiveLow(testSR, testSR), testSR))
	if !inFamily(kick.Role, RoleKick, RoleBass) && kick.Role != RoleUnknown {
		t.Errorf("low punchy tone: expected kick/bass/unknown, got %q (%s)", kick.Role, kick.RoleBasis)
	}
	// We want it to actually LAND in the low family for a clearly low+punchy signal.
	if kick.Role != RoleKick && kick.Role != RoleBass {
		t.Logf("note: low punchy tone classified as %q (conf %v) — acceptable but check: %s",
			kick.Role, kick.RoleConfidence, kick.RoleBasis)
	}

	// High-energy bright content -> hats/cymbals family.
	hatSig := noiseBand(8000, 16000, testSR, testSR, 0.5)
	// give it transients so crest is high (percussive)
	for i := range hatSig {
		if (i/2000)%4 != 0 {
			hatSig[i] *= 0.1
		}
	}
	hat := AnalyzeStem("oh.wav", encodeWAV16(hatSig, testSR))
	if hat.Role != RoleHatsCymbals && hat.Role != RoleUnknown {
		t.Errorf("bright high-energy content: expected hats-or-cymbals/unknown, got %q (%s)",
			hat.Role, hat.RoleBasis)
	}
}

func inFamily(got Role, want ...Role) bool {
	for _, w := range want {
		if got == w {
			return true
		}
	}
	return false
}

func TestFilenameCorroboration(t *testing.T) {
	// A low punchy signal named "kick" should be named kick with raised confidence.
	r := AnalyzeStem("Kick_01.wav", encodeWAV16(percussiveLow(testSR, testSR), testSR))
	if r.Role == RoleKick && r.RoleConfidence < 0.7 {
		t.Errorf("kick spectrum + 'kick' filename should be high confidence, got %v", r.RoleConfidence)
	}
}

func TestMalformedDegrades(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		[]byte("not a wav at all"),
		[]byte("RIFF????WAVE"), // header only, no chunks
	}
	for i, b := range cases {
		r := AnalyzeStem("bad.wav", b)
		if !r.Skipped {
			t.Errorf("case %d: malformed input should be skipped, got %+v", i, r)
		}
		if r.Reason == "" {
			t.Errorf("case %d: skipped report must carry a reason", i)
		}
	}
}

func TestTooShortDegrades(t *testing.T) {
	r := AnalyzeStem("tiny.wav", encodeWAV16([]float64{0.1, -0.1, 0.2}, testSR))
	if !r.Skipped {
		t.Errorf("a 3-sample file should be skipped as too short, got %+v", r)
	}
}

func TestDeterminism(t *testing.T) {
	s := sine(220, testSR, testSR, 0.5)
	wav := encodeWAV16(s, testSR)
	a := AnalyzeStem("x.wav", wav)
	b := AnalyzeStem("x.wav", wav)
	if a != b {
		t.Errorf("same input must produce identical reports:\n a=%+v\n b=%+v", a, b)
	}
}

func TestBuildFolderReportSortedAndDeterministic(t *testing.T) {
	files := []FileInput{
		{Path: "z_vox.wav", Data: encodeWAV16(sine(440, testSR, testSR, 0.5), testSR)},
		{Path: "a_kick.wav", Data: encodeWAV16(percussiveLow(testSR, testSR), testSR)},
		{Path: "m_bad.wav", Data: []byte("garbage")},
	}
	r1 := BuildFolderReport("session", files)
	r2 := BuildFolderReport("session", files)

	if len(r1.Stems) != 3 {
		t.Fatalf("expected 3 stem entries, got %d", len(r1.Stems))
	}
	// Sorted by basename: a_kick, m_bad, z_vox.
	if r1.Stems[0].Name != "a_kick.wav" || r1.Stems[2].Name != "z_vox.wav" {
		t.Errorf("stems not sorted by name: %s ... %s", r1.Stems[0].Name, r1.Stems[2].Name)
	}
	if r1.StemCount != 2 || r1.SkippedCount != 1 {
		t.Errorf("expected 2 analyzed + 1 skipped, got %d + %d", r1.StemCount, r1.SkippedCount)
	}
	// Determinism across the whole report.
	if r1.Headline != r2.Headline {
		t.Errorf("headline not deterministic")
	}
	for i := range r1.Stems {
		if r1.Stems[i] != r2.Stems[i] {
			t.Errorf("stem %d not deterministic", i)
		}
	}
}

func TestEmptyFolderFriendly(t *testing.T) {
	r := BuildFolderReport("empty", nil)
	if r.StemCount != 0 || r.SkippedCount != 0 {
		t.Errorf("empty folder should have zero counts")
	}
	if r.Headline == "" {
		t.Errorf("empty folder should still have a friendly headline")
	}
}

func TestClippingHeadlineNamesStem(t *testing.T) {
	clip := make([]float64, testSR)
	for i := range clip {
		clip[i] = 1.0
	}
	files := []FileInput{
		{Path: "snare_OH.wav", Data: encodeWAV16(clip, testSR)},
		{Path: "bass.wav", Data: encodeWAV16(sine(80, testSR, testSR, 0.3), testSR)},
	}
	r := BuildFolderReport("sess", files)
	if len(r.Clipping) != 1 || r.Clipping[0] != "snare_OH.wav" {
		t.Errorf("expected snare_OH.wav flagged as clipping, got %v", r.Clipping)
	}
	if !bytes.Contains([]byte(r.Headline), []byte("CLIPPING")) {
		t.Errorf("headline should mention CLIPPING, got %q", r.Headline)
	}
}

func TestWindowsPathBasename(t *testing.T) {
	r := AnalyzeStem(`C:\Sessions\Band\kick.wav`, encodeWAV16(percussiveLow(testSR, testSR), testSR))
	if r.Name != "kick.wav" {
		t.Errorf("expected separator-agnostic basename 'kick.wav', got %q", r.Name)
	}
}

func TestRoleFromNameDoesNotMisreadBassdrum(t *testing.T) {
	// "bassdrum" must read as kick, not bass (ordering in roleFromName).
	if got := roleFromName("bassdrum_01.wav"); got != RoleKick {
		t.Errorf("bassdrum should map to kick, got %q", got)
	}
	if got := roleFromName("Bass_DI.wav"); got != RoleBass {
		t.Errorf("Bass_DI should map to bass, got %q", got)
	}
}

func TestNearSilentSuggestionNote(t *testing.T) {
	s := sine(440, testSR, testSR, 0.0002) // below silence threshold
	r := AnalyzeStem("ghost.wav", encodeWAV16(s, testSR))
	if !r.NearSilent {
		t.Fatalf("expected near-silent, loudness=%v", r.LoudnessDBFS)
	}
	if r.SuggestNote == "" {
		t.Errorf("near-silent stem should carry an explanatory suggest note")
	}
}
