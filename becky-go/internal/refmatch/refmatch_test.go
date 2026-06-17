package refmatch

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"becky-go/internal/dsp"
)

// --- in-memory PCM16 mono WAV encoder (test-only) so we can synthesize stems and
// feed dsp.DecodeWAV exactly as the real tool does. ---

func encodeWAV(samples []float64, sr int) []byte {
	var buf bytes.Buffer
	data := make([]byte, len(samples)*2)
	for i, s := range samples {
		if s > 1 {
			s = 1
		}
		if s < -1 {
			s = -1
		}
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16(math.Round(s*32767))))
	}
	dataLen := len(data)
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	binary.Write(&buf, binary.LittleEndian, uint32(sr))
	binary.Write(&buf, binary.LittleEndian, uint32(sr*2)) // byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(2))    // block align
	binary.Write(&buf, binary.LittleEndian, uint16(16))   // bits
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataLen))
	buf.Write(data)
	return buf.Bytes()
}

const testSR = 44100

// tone synthesizes amp*sin at freqHz for durSec seconds.
func tone(freqHz, amp, durSec float64) []float64 {
	n := int(durSec * testSR)
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = amp * math.Sin(2*math.Pi*freqHz*float64(i)/float64(testSR))
	}
	return out
}

// mix sums multiple signals (truncating to the shortest) and clamps to [-1,1].
func mix(sigs ...[]float64) []float64 {
	n := math.MaxInt
	for _, s := range sigs {
		if len(s) < n {
			n = len(s)
		}
	}
	if n == math.MaxInt {
		n = 0
	}
	out := make([]float64, n)
	for _, s := range sigs {
		for i := 0; i < n; i++ {
			out[i] += s[i]
		}
	}
	for i := range out {
		if out[i] > 1 {
			out[i] = 1
		}
		if out[i] < -1 {
			out[i] = -1
		}
	}
	return out
}

func profileFromSamples(t *testing.T, samples []float64, opt Options) Profile {
	t.Helper()
	a, err := dsp.DecodeWAV(encodeWAV(samples, testSR))
	if err != nil {
		t.Fatalf("decode synthetic wav: %v", err)
	}
	return Analyze(a, opt)
}

// bandDB pulls a named band's energy out of a profile.
func bandDB(t *testing.T, p Profile, name string) float64 {
	t.Helper()
	b, ok := p.BandByName(name)
	if !ok {
		t.Fatalf("band %q missing", name)
	}
	return b.EnergyDB
}

// --- profile structure / determinism ---

func TestProfileHasAllBandsInOrder(t *testing.T) {
	p := profileFromSamples(t, tone(440, 0.5, 1.0), Options{})
	if len(p.Bands) != len(bandDefs) {
		t.Fatalf("got %d bands, want %d", len(p.Bands), len(bandDefs))
	}
	for i, b := range p.Bands {
		if b.Name != bandDefs[i].name {
			t.Errorf("band %d = %q, want %q (order must be fixed)", i, b.Name, bandDefs[i].name)
		}
	}
}

func TestDeterministic(t *testing.T) {
	s := mix(tone(110, 0.4, 1.5), tone(880, 0.2, 1.5))
	p1 := profileFromSamples(t, s, Options{})
	p2 := profileFromSamples(t, s, Options{})
	if p1.LoudnessDB != p2.LoudnessDB || p1.CrestDB != p2.CrestDB || p1.CentroidHz != p2.CentroidHz {
		t.Errorf("scalar metrics differ between runs")
	}
	for i := range p1.Bands {
		if p1.Bands[i].EnergyDB != p2.Bands[i].EnergyDB {
			t.Errorf("band %s differs between runs: %v vs %v", p1.Bands[i].Name, p1.Bands[i].EnergyDB, p2.Bands[i].EnergyDB)
		}
	}
	// And the full plan must be identical.
	m := profileFromSamples(t, mix(tone(110, 0.6, 1.5)), Options{})
	pl1 := Match(p1, m)
	pl2 := Match(p1, m)
	if pl1.Headline != pl2.Headline || pl1.MoveCount != pl2.MoveCount || len(pl1.EQMoves) != len(pl2.EQMoves) {
		t.Errorf("match plan not deterministic")
	}
}

// --- band energy direction: bright vs dark ---

func TestBrightVsDarkBands(t *testing.T) {
	// A high tone has more presence/brilliance energy; a low tone more low/sub.
	bright := profileFromSamples(t, tone(4000, 0.5, 1.0), Options{}) // presence band
	dark := profileFromSamples(t, tone(90, 0.5, 1.0), Options{})     // low band

	if bandDB(t, bright, "presence") <= bandDB(t, dark, "presence") {
		t.Errorf("bright stem should have more presence energy: bright=%.1f dark=%.1f",
			bandDB(t, bright, "presence"), bandDB(t, dark, "presence"))
	}
	if bandDB(t, dark, "low") <= bandDB(t, bright, "low") {
		t.Errorf("dark stem should have more low energy: dark=%.1f bright=%.1f",
			bandDB(t, dark, "low"), bandDB(t, bright, "low"))
	}
	// Centroid (brightness) must be higher for the bright stem.
	if bright.CentroidHz <= dark.CentroidHz {
		t.Errorf("bright centroid %.0f should exceed dark centroid %.0f", bright.CentroidHz, dark.CentroidHz)
	}
}

// --- loudness direction: loud vs quiet ---

func TestLoudVsQuiet(t *testing.T) {
	loud := profileFromSamples(t, tone(440, 0.8, 1.0), Options{})
	quiet := profileFromSamples(t, tone(440, 0.1, 1.0), Options{})
	if loud.LoudnessDB <= quiet.LoudnessDB {
		t.Errorf("loud stem dBFS %.1f should exceed quiet %.1f", loud.LoudnessDB, quiet.LoudnessDB)
	}
	// ~18 dB difference between 0.8 and 0.1 amplitude.
	gap := loud.LoudnessDB - quiet.LoudnessDB
	if gap < 10 {
		t.Errorf("expected a sizeable loudness gap, got %.1f dB", gap)
	}
}

// --- dynamics: dynamic vs compressed ---

func TestDynamicVsCompressed(t *testing.T) {
	// "Dynamic": a quiet section then a loud burst -> high peak, low RMS -> big crest.
	dynamic := make([]float64, 0)
	dynamic = append(dynamic, tone(440, 0.05, 0.8)...) // quiet
	dynamic = append(dynamic, tone(440, 0.95, 0.2)...) // loud burst
	// "Compressed": steady level throughout -> peak ~ RMS -> low crest.
	compressed := tone(440, 0.6, 1.0)

	dp := profileFromSamples(t, dynamic, Options{})
	cp := profileFromSamples(t, compressed, Options{})
	if dp.CrestDB <= cp.CrestDB {
		t.Errorf("dynamic crest %.1f should exceed compressed crest %.1f", dp.CrestDB, cp.CrestDB)
	}
}

// --- match plan: correct-direction EQ moves ---

func TestMatchEQDirection(t *testing.T) {
	// Reference is bright (lots of presence); mine is dark (lots of low).
	ref := profileFromSamples(t, mix(tone(4000, 0.5, 1.0), tone(8000, 0.4, 1.0)), Options{})
	mine := profileFromSamples(t, mix(tone(90, 0.5, 1.0), tone(110, 0.4, 1.0)), Options{})
	plan := Match(ref, mine)

	// There must be moves; presence should be a BOOST (positive), low should be a CUT.
	var presence, low *EQMove
	for i := range plan.EQMoves {
		switch plan.EQMoves[i].Band {
		case "presence":
			presence = &plan.EQMoves[i]
		case "low":
			low = &plan.EQMoves[i]
		}
	}
	if presence == nil {
		t.Fatalf("expected a presence EQ move; got moves: %+v", plan.EQMoves)
	}
	if presence.DeltaDB <= 0 {
		t.Errorf("presence move should be a boost (+), got %.1f", presence.DeltaDB)
	}
	if low == nil {
		t.Fatalf("expected a low EQ move; got moves: %+v", plan.EQMoves)
	}
	if low.DeltaDB >= 0 {
		t.Errorf("low move should be a cut (-), got %.1f", low.DeltaDB)
	}
}

// --- match plan: gain direction ---

func TestMatchGainDirection(t *testing.T) {
	loudRef := profileFromSamples(t, tone(440, 0.8, 1.0), Options{})
	quietMine := profileFromSamples(t, tone(440, 0.1, 1.0), Options{})
	plan := Match(loudRef, quietMine)
	if plan.GainDB <= 0 {
		t.Errorf("reference louder -> gain move should be positive (turn up), got %.1f", plan.GainDB)
	}
	if plan.GainText == "" {
		t.Errorf("a >10 dB gap must produce a gain move text")
	}

	// Reverse: reference quieter -> turn down.
	plan2 := Match(quietMine, loudRef)
	if plan2.GainDB >= 0 {
		t.Errorf("reference quieter -> gain move should be negative (turn down), got %.1f", plan2.GainDB)
	}
}

// --- match plan: compression hint direction ---

func TestMatchCompressionDirection(t *testing.T) {
	dynamic := append(append([]float64{}, tone(440, 0.05, 0.8)...), tone(440, 0.95, 0.2)...)
	compressed := tone(440, 0.6, 1.0)
	dynP := profileFromSamples(t, dynamic, Options{})
	compP := profileFromSamples(t, compressed, Options{})

	// Reference compressed, mine dynamic -> "add bus compression".
	plan := Match(compP, dynP)
	if plan.CompText == "" {
		t.Fatalf("expected a compression hint (crest delta %.1f)", plan.CrestDeltaDB)
	}
	if !contains(plan.CompText, "add") {
		t.Errorf("mine more dynamic than ref -> should suggest ADDING compression, got %q", plan.CompText)
	}

	// Reference dynamic, mine compressed -> "ease off".
	plan2 := Match(dynP, compP)
	if !contains(plan2.CompText, "ease") {
		t.Errorf("ref more dynamic -> should suggest EASING off compression, got %q", plan2.CompText)
	}
}

// --- corroborate-then-conclude: small deltas are suppressed ---

func TestSmallDeltasSuppressed(t *testing.T) {
	s := mix(tone(110, 0.4, 1.5), tone(900, 0.3, 1.5), tone(5000, 0.2, 1.5))
	a := profileFromSamples(t, s, Options{})
	b := profileFromSamples(t, s, Options{}) // identical
	plan := Match(a, b)
	if len(plan.EQMoves) != 0 {
		t.Errorf("identical stems must yield zero EQ moves (close enough), got %v", plan.EQMoves)
	}
	if plan.GainText != "" {
		t.Errorf("identical stems must yield no gain move, got %q", plan.GainText)
	}
	if plan.CompText != "" {
		t.Errorf("identical stems must yield no compression move, got %q", plan.CompText)
	}
	if plan.MoveCount != 0 {
		t.Errorf("identical stems -> 0 moves, got %d", plan.MoveCount)
	}
	if !contains(plan.Verdict, "close enough") {
		t.Errorf("verdict should be 'close enough', got %q", plan.Verdict)
	}
	// But the raw band deltas must still ALL be present for completeness.
	if len(plan.BandDeltas) != len(bandDefs) {
		t.Errorf("band deltas should cover every band, got %d", len(plan.BandDeltas))
	}
}

// A delta just under threshold stays quiet; just over earns a move.
func TestThresholdBoundary(t *testing.T) {
	mkBand := func(name string, db float64) Band {
		b, _ := Profile{Bands: zeroBands()}.BandByName(name)
		b.EnergyDB = db
		return b
	}
	_ = mkBand
	// Build two minimal profiles by hand differing in one band by ~1.0 (< 1.5 thr).
	ref := Profile{Bands: zeroBands(), LoudnessDB: -12, PeakDB: -6, CrestDB: 6}
	mine := Profile{Bands: zeroBands(), LoudnessDB: -12, PeakDB: -6, CrestDB: 6}
	setBand(ref.Bands, "mid", -20)
	setBand(mine.Bands, "mid", -21) // 1.0 dB below threshold
	if len(Match(ref, mine).EQMoves) != 0 {
		t.Errorf("1.0 dB delta is below 1.5 dB threshold -> no move expected")
	}
	setBand(mine.Bands, "mid", -22.5) // 2.5 dB, above threshold
	plan := Match(ref, mine)
	if len(plan.EQMoves) != 1 || plan.EQMoves[0].Band != "mid" {
		t.Errorf("2.5 dB delta should produce exactly one mid move, got %+v", plan.EQMoves)
	}
	if plan.EQMoves[0].DeltaDB <= 0 {
		t.Errorf("ref louder in mid than mine -> boost (+), got %.1f", plan.EQMoves[0].DeltaDB)
	}
}

func setBand(bands []Band, name string, db float64) {
	for i := range bands {
		if bands[i].Name == name {
			bands[i].EnergyDB = db
			return
		}
	}
}

// --- degrade-never-crash ---

func TestDegradeEmptyAudio(t *testing.T) {
	p := Analyze(dsp.Audio{}, Options{})
	if !p.Degraded {
		t.Errorf("empty audio should degrade")
	}
	if p.Note == "" {
		t.Errorf("degraded profile should carry a plain note")
	}
	if len(p.Bands) != len(bandDefs) {
		t.Errorf("degraded profile must still report the full band shape")
	}
	// Matching two degraded profiles must not panic and must flag degraded.
	plan := Match(p, p)
	if !plan.Degraded {
		t.Errorf("matching degraded profiles should mark the plan degraded")
	}
}

func TestDegradeShortAudio(t *testing.T) {
	// Fewer samples than one frame.
	short := tone(440, 0.5, 0.001) // ~44 samples << frameSize 2048
	p := profileFromSamples(t, short, Options{})
	if !p.Degraded {
		t.Errorf("audio shorter than a frame should degrade")
	}
}

func TestSilentAudioNoNaN(t *testing.T) {
	silent := make([]float64, testSR) // 1s of zeros
	p := profileFromSamples(t, silent, Options{})
	if math.IsNaN(p.LoudnessDB) || math.IsInf(p.LoudnessDB, 0) {
		t.Errorf("silent loudness must be finite, got %v", p.LoudnessDB)
	}
	if p.LoudnessDB != silenceFloorDB {
		t.Errorf("silent loudness should clamp to floor %.0f, got %v", silenceFloorDB, p.LoudnessDB)
	}
	for _, b := range p.Bands {
		if math.IsNaN(b.EnergyDB) || math.IsInf(b.EnergyDB, 0) {
			t.Errorf("band %s energy must be finite, got %v", b.Name, b.EnergyDB)
		}
	}
}

func TestMalformedWAVErrors(t *testing.T) {
	_, err := dsp.DecodeWAV([]byte("not a wav at all"))
	if err == nil {
		t.Errorf("malformed bytes should error from the decoder (tool exits non-zero)")
	}
}

// --- K-weight approximation is labelled and changes loudness ---

func TestKWeightApproxLabelled(t *testing.T) {
	s := mix(tone(120, 0.5, 1.0), tone(6000, 0.3, 1.0))
	plain := profileFromSamples(t, s, Options{})
	kw := profileFromSamples(t, s, Options{KWeight: true})
	if !kw.KWeighted {
		t.Errorf("K-weighted profile must set KWeighted=true so output is honest")
	}
	if plain.KWeighted {
		t.Errorf("default profile must NOT claim K-weighting")
	}
	if kw.LoudnessDB == plain.LoudnessDB {
		t.Errorf("K-weight approx should change the loudness reading")
	}
	// The plan note must disclose the approximation.
	plan := Match(kw, kw)
	if !contains(plan.Note, "approximation") {
		t.Errorf("plan note must disclose the K-weight approximation, got %q", plan.Note)
	}
}

// --- headline always present and human ---

func TestHeadlineNonEmpty(t *testing.T) {
	ref := profileFromSamples(t, mix(tone(4000, 0.5, 1.0)), Options{})
	mine := profileFromSamples(t, mix(tone(90, 0.5, 1.0)), Options{})
	plan := Match(ref, mine)
	if plan.Headline == "" {
		t.Errorf("every plan must have a plain-English headline")
	}
	if plan.Verdict == "" {
		t.Errorf("every plan must have a verdict")
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
