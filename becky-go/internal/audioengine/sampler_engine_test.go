package audioengine

// sampler_engine_test.go — MUSICAL behavior tests for the sampler engine.
//
// Red-team rule: tests must assert MUSICAL outcomes, not struct plumbing.
// Every test renders real audio and checks the resulting waveform:
//
//   - Higher velocity → higher RMS/peak (P0-2 VelGain)
//   - Choked voice tail ramps to ~0 within the declick window (P0-4 declick)
//   - Resampling up shifts dominant frequency / shortens buffer (P1-2 Hermite)
//   - AmpEnv decay (AHD) shortens the audible tail vs no decay (P1-1 AmpEnv)
//   - Offline render is byte-deterministic with a fixed seed (determinism)

import (
	"math"
	"os"
	"testing"

	"becky-go/internal/drummachine"
	"becky-go/internal/sampler"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

const testSR = 44100 // sample rate used for all engine tests

// sineWave synthesizes a mono sine wave at frequency Hz for dur seconds.
func sineWave(freq, dur float64, sr int) []float32 {
	n := int(dur * float64(sr))
	s := make([]float32, n)
	for i := range s {
		s[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / float64(sr)))
	}
	return s
}

// rmsAudio returns the root-mean-square of buf.
func rmsAudio(buf []float32) float64 {
	if len(buf) == 0 {
		return 0
	}
	var sum float64
	for _, s := range buf {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(buf)))
}

// peakAbsAudio returns the absolute peak amplitude.
func peakAbsAudio(buf []float32) float64 {
	var pk float64
	for _, s := range buf {
		a := math.Abs(float64(s))
		if a > pk {
			pk = a
		}
	}
	return pk
}

// dominantFreqAudio estimates the dominant frequency in buf via DFT over the
// first 4096 frames. Sufficient for verifying pitch-shift direction.
func dominantFreqAudio(buf []float32, sr int) float64 {
	n := 4096
	if len(buf) < n {
		n = len(buf)
	}
	maxMag := 0.0
	bestK := 1
	for k := 1; k < n/2; k++ {
		var re, im float64
		for t := 0; t < n; t++ {
			angle := 2 * math.Pi * float64(k) * float64(t) / float64(n)
			re += float64(buf[t]) * math.Cos(angle)
			im -= float64(buf[t]) * math.Sin(angle)
		}
		mag := re*re + im*im
		if mag > maxMag {
			maxMag, bestK = mag, k
		}
	}
	return float64(bestK) * float64(sr) / float64(n)
}

// makeSinglePadKit builds a minimal SamplerKitMap + SamplerKitPCM for pad 0
// with the given Sound and one variant backed by pcm.
func makeSinglePadKit(snd *sampler.Sound, pcm []float32) (SamplerKitMap, *SamplerKitPCM) {
	const path = "test://pad0.wav"

	snd.Layers = []sampler.Layer{
		{
			VelLo: 1,
			VelHi: 127,
			RoundRobin: []sampler.Variant{
				{
					SamplePath:     path,
					LoopMode:       sampler.OneShot,
					PitchKeycenter: 60,
				},
			},
			RRMode: sampler.Sequential,
		},
	}

	var kitMap SamplerKitMap
	kitMap[0] = snd

	rawPCM := map[int]map[string][]float32{
		0: {path: pcm},
	}
	kitPCM := BuildSamplerKitPCMFromFloat32(rawPCM, testSR, testSR)
	return kitMap, kitPCM
}

// makeTestMachine makes a Machine with pad 0 at level 1.0, tempo 120.
// Must use NewMachine() because Kit.Pads is a slice initialised by DefaultKit().
func makeTestMachine() *drummachine.Machine {
	m := drummachine.NewMachine()
	m.Kit.Pads[0].Level = 1.0
	return m
}

// makePattern16 makes a 16-step pattern with pad 0 on step 0 at vel.
func makePattern16(vel int) drummachine.Pattern {
	lane := make([]drummachine.Step, 16)
	lane[0] = drummachine.Step{On: true, Vel: vel}
	lanes := make([][]drummachine.Step, drummachine.PadCount)
	lanes[0] = lane
	for i := 1; i < drummachine.PadCount; i++ {
		lanes[i] = make([]drummachine.Step, 16)
	}
	return drummachine.Pattern{
		Steps: 16,
		Lanes: lanes,
	}
}

// renderPad0 is a convenience wrapper: renders numSamples of a one-pad pattern.
func renderPad0(snd *sampler.Sound, pcm []float32, vel int, numSamples int64) []float32 {
	kitMap, kitPCM := makeSinglePadKit(snd, pcm)
	m := makeTestMachine()
	pat := makePattern16(vel)
	return RenderSamplerPattern(RenderSamplerPatternOpts{
		Kit:              kitMap,
		KitPCM:           kitPCM,
		Pattern:          pat,
		Machine:          m,
		DeviceSampleRate: testSR,
		NumSamples:       numSamples,
		RNGSeed:          42,
	})
}

// ---------------------------------------------------------------------------
// P0-2 — Velocity → amplitude (higher vel → higher RMS)
// ---------------------------------------------------------------------------

func TestVelocityScalesAmplitude(t *testing.T) {
	// A sine is the cleanest probe: pure level difference, no spectral contamination.
	pcm := sineWave(440, 0.5, testSR)

	sndLow := sampler.NewDrumSound("low")
	sndHigh := sampler.NewDrumSound("high")
	// AmpVelTrack=1 (default from NewDrumSound): full velocity → loudness.

	const nSamples = int64(testSR / 4) // render 250 ms
	bufLow := renderPad0(&sndLow, pcm, 10, nSamples)
	bufHigh := renderPad0(&sndHigh, pcm, 127, nSamples)

	rLow := rmsAudio(bufLow)
	rHigh := rmsAudio(bufHigh)

	if rLow == 0 {
		t.Fatal("velocity 10 render is silent")
	}
	if rHigh == 0 {
		t.Fatal("velocity 127 render is silent")
	}
	// vel 127 should be significantly louder (square-law: (127/127)^2=1 vs (10/127)^2≈0.006)
	ratio := rHigh / rLow
	if ratio < 5 {
		t.Errorf("expected high-vel RMS >> low-vel RMS; ratio=%.2f (want >= 5)", ratio)
	}
	t.Logf("rmsLow(vel=10)=%.4f  rmsHigh(vel=127)=%.4f  ratio=%.2f", rLow, rHigh, ratio)
}

func TestVelocityPeakHigherThanLow(t *testing.T) {
	pcm := sineWave(220, 0.3, testSR)
	snd := sampler.NewDrumSound("kick")
	buf1 := renderPad0(&snd, pcm, 1, int64(testSR/4))
	buf127 := renderPad0(&snd, pcm, 127, int64(testSR/4))

	pk1 := peakAbsAudio(buf1)
	pk127 := peakAbsAudio(buf127)
	if pk127 <= pk1 {
		t.Errorf("expected peak(vel=127) > peak(vel=1); got %.4f vs %.4f", pk127, pk1)
	}
}

// ---------------------------------------------------------------------------
// P0-3 — Round-robin: sequential alternates between loud/quiet variants
// ---------------------------------------------------------------------------

func TestSequentialRRAlternates(t *testing.T) {
	const path0 = "test://v0.wav"
	const path1 = "test://v1.wav"

	pcmLoud := sineWave(440, 0.1, testSR)
	pcmQuiet := make([]float32, len(pcmLoud))
	for i, s := range pcmLoud {
		pcmQuiet[i] = s * 0.05
	}

	snd := sampler.NewDrumSound("rr")
	snd.Layers = []sampler.Layer{
		{
			VelLo: 1, VelHi: 127,
			RoundRobin: []sampler.Variant{
				{SamplePath: path0, LoopMode: sampler.OneShot, PitchKeycenter: 60},
				{SamplePath: path1, LoopMode: sampler.OneShot, PitchKeycenter: 60},
			},
			RRMode: sampler.Sequential,
		},
	}

	var kitMap SamplerKitMap
	kitMap[0] = &snd
	kitPCM := BuildSamplerKitPCMFromFloat32(
		map[int]map[string][]float32{0: {path0: pcmLoud, path1: pcmQuiet}},
		testSR, testSR,
	)

	// Two-step pattern: step 0 and step 8 both fire (2 hits).
	lanes := make([][]drummachine.Step, drummachine.PadCount)
	lane := make([]drummachine.Step, 16)
	lane[0] = drummachine.Step{On: true, Vel: 100}
	lane[8] = drummachine.Step{On: true, Vel: 100}
	lanes[0] = lane
	for i := 1; i < drummachine.PadCount; i++ {
		lanes[i] = make([]drummachine.Step, 16)
	}
	pat := drummachine.Pattern{Steps: 16, Lanes: lanes}

	m := drummachine.NewMachine()
	m.Kit.Pads[0].Level = 1.0

	const nSamples = int64(testSR) // 1 second
	buf := RenderSamplerPattern(RenderSamplerPatternOpts{
		Kit:              kitMap,
		KitPCM:           kitPCM,
		Pattern:          pat,
		Machine:          m,
		DeviceSampleRate: testSR,
		NumSamples:       nSamples,
		RNGSeed:          42,
	})
	if len(buf) == 0 {
		t.Fatal("render returned empty buffer")
	}

	// step 0 at 120 BPM, 1/16 step ≈ 5512 samples.
	stepSmp := int(math.Round(float64(testSR) * 60.0 / (120.0 * 4.0)))
	step0Off := 0
	step8Off := stepSmp * 8

	win := testSR / 10 // 100 ms window per hit
	if step8Off+win > len(buf) {
		t.Skipf("buffer too short for hit windows (step8=%d+%d, len=%d)", step8Off, win, len(buf))
	}
	rms0 := rmsAudio(buf[step0Off : step0Off+win])
	rms8 := rmsAudio(buf[step8Off : step8Off+win])

	// First hit plays path0 (loud), second plays path1 (quiet).
	if rms0 <= rms8 {
		t.Errorf("first RR hit (loud) should have higher RMS than second (quiet); rms0=%.4f rms8=%.4f", rms0, rms8)
	}
	t.Logf("RR hit 0 (loud): rms=%.4f   hit 8 (quiet): rms=%.4f", rms0, rms8)
}

// ---------------------------------------------------------------------------
// P0-4 — Declick: choked voice tail does NOT jump to zero in one sample
// ---------------------------------------------------------------------------

func TestDeclickChokeNoHardCut(t *testing.T) {
	// Two pads share a choke group; the second hit cuts the first.
	// Without declick, the amplitude drops to zero in one sample → audible click.
	const path0 = "test://long0.wav"
	const path1 = "test://long1.wav"

	// 2-second sustained tone.
	longPCM := sineWave(440, 2.0, testSR)

	snd0 := sampler.NewDrumSound("open")
	snd0.ChokeGroup = 1
	snd0.DeclickMs = 5 // 5 ms declick floor
	snd0.Layers = []sampler.Layer{{
		VelLo: 1, VelHi: 127,
		RoundRobin: []sampler.Variant{{SamplePath: path0, LoopMode: sampler.OneShot, PitchKeycenter: 60}},
		RRMode:     sampler.Sequential,
	}}

	snd1 := sampler.NewDrumSound("closed")
	snd1.ChokeGroup = 1
	snd1.DeclickMs = 5
	snd1.Layers = []sampler.Layer{{
		VelLo: 1, VelHi: 127,
		RoundRobin: []sampler.Variant{{SamplePath: path1, LoopMode: sampler.OneShot, PitchKeycenter: 60}},
		RRMode:     sampler.Sequential,
	}}

	var kitMap SamplerKitMap
	kitMap[0] = &snd0
	kitMap[1] = &snd1

	kitPCM := BuildSamplerKitPCMFromFloat32(
		map[int]map[string][]float32{
			0: {path0: longPCM},
			1: {path1: longPCM},
		},
		testSR, testSR,
	)

	// pad0 step 0, pad1 step 4 (chokes pad0).
	lanes := make([][]drummachine.Step, drummachine.PadCount)
	lane0 := make([]drummachine.Step, 16)
	lane0[0] = drummachine.Step{On: true, Vel: 100}
	lanes[0] = lane0
	lane1 := make([]drummachine.Step, 16)
	lane1[4] = drummachine.Step{On: true, Vel: 100}
	lanes[1] = lane1
	for i := 2; i < drummachine.PadCount; i++ {
		lanes[i] = make([]drummachine.Step, 16)
	}

	pat := drummachine.Pattern{Steps: 16, Lanes: lanes}
	m := drummachine.NewMachine()
	m.Kit.Pads[0].Level = 1.0
	m.Kit.Pads[1].Level = 1.0

	const nSamples = int64(testSR) // 1 second
	buf := RenderSamplerPattern(RenderSamplerPatternOpts{
		Kit:              kitMap,
		KitPCM:           kitPCM,
		Pattern:          pat,
		Machine:          m,
		DeviceSampleRate: testSR,
		NumSamples:       nSamples,
		RNGSeed:          42,
	})
	if len(buf) == 0 {
		t.Fatal("render returned empty buffer")
	}

	// Step 4 fires at approximately sample 5512.
	stepSmp := int(math.Round(float64(testSR) * 60.0 / (120.0 * 4.0)))
	step4Smp := stepSmp * 4

	if step4Smp < 5 || step4Smp+10 >= len(buf) {
		t.Skipf("choke onset out of bounds (step4=%d, len=%d)", step4Smp, len(buf))
	}

	// Measure amplitude just before the choke fires.
	preChokePeak := 0.0
	for i := step4Smp - 5; i < step4Smp; i++ {
		a := math.Abs(float64(buf[i]))
		if a > preChokePeak {
			preChokePeak = a
		}
	}
	// Measure amplitude in the first few samples AFTER the choke fires.
	postChokePeak := 0.0
	for i := step4Smp + 1; i < step4Smp+6; i++ {
		a := math.Abs(float64(buf[i]))
		if a > postChokePeak {
			postChokePeak = a
		}
	}

	// Hard cut (no declick): amplitude drops from preChokePeak → ~0 in one sample.
	// With declick: post-choke samples remain close to pre-choke amplitude (5ms ramp).
	if preChokePeak > 0.01 && postChokePeak < preChokePeak*0.05 {
		t.Errorf("declick failed: amplitude jumped from %.4f to %.4f (expected smooth ramp)",
			preChokePeak, postChokePeak)
	}
	t.Logf("pre-choke peak=%.4f  post-choke (1-5 samples after)=%.4f", preChokePeak, postChokePeak)
}

// ---------------------------------------------------------------------------
// P1-2 — Hermite resampling: pitch-up shortens buffer and shifts freq upward
// ---------------------------------------------------------------------------

func TestResampleHermitePitchUp(t *testing.T) {
	// +12 semitones (one octave up): buffer should be ~half length, freq ~2x.
	const refFreq = 440.0
	pcm := sineWave(refFreq, 0.5, testSR)

	upPCM := resampleHermitePitch(pcm, 12.0)
	if upPCM == nil {
		t.Fatal("resampleHermitePitch returned nil for +12 semis")
	}

	// Length: +12 semis = speed×2 → ~half the frames.
	expectedLen := len(pcm) / 2
	if math.Abs(float64(len(upPCM)-expectedLen)) > float64(expectedLen)*0.03 {
		t.Errorf("pitch-up length: got %d, want ~%d (±3%%)", len(upPCM), expectedLen)
	}

	// Dominant frequency should be ~880 Hz (>1.5× original).
	origFreq := dominantFreqAudio(pcm, testSR)
	shiftedFreq := dominantFreqAudio(upPCM, testSR)
	if shiftedFreq < origFreq*1.5 {
		t.Errorf("pitch-up dominant freq: got %.1f Hz, want > %.1f Hz (orig=%.1f)",
			shiftedFreq, origFreq*1.5, origFreq)
	}
	t.Logf("orig=%.1f Hz  shifted=%.1f Hz  len orig=%d  len shifted=%d",
		origFreq, shiftedFreq, len(pcm), len(upPCM))
}

func TestResampleHermitePitchDown(t *testing.T) {
	// -12 semis should produce ~double length and ~half frequency.
	pcm := sineWave(880.0, 0.1, testSR) // 880 Hz, 100 ms
	downPCM := resampleHermitePitch(pcm, -12.0)
	if downPCM == nil {
		t.Fatal("resampleHermitePitch returned nil for -12 semis")
	}
	wantLen := len(pcm) * 2
	if math.Abs(float64(len(downPCM)-wantLen)) > float64(wantLen)*0.03 {
		t.Errorf("pitch-down length: got %d, want ~%d (±3%%)", len(downPCM), wantLen)
	}
}

func TestResampleHermiteUnityValues(t *testing.T) {
	// Unity ratio (1.0) returns a copy — values identical.
	pcm := sineWave(440, 0.1, testSR)
	out := resampleHermite(pcm, 1.0)
	if out == nil {
		t.Fatal("unity ratio returned nil")
	}
	if len(out) != len(pcm) {
		t.Fatalf("unity ratio changed length: got %d, want %d", len(out), len(pcm))
	}
	for i, s := range out {
		if s != pcm[i] {
			t.Errorf("unity ratio changed sample at [%d]: %.6f != %.6f", i, s, pcm[i])
			break
		}
	}
}

// ---------------------------------------------------------------------------
// P1-1 — AmpEnv AHD: short decay silences the voice quickly
// ---------------------------------------------------------------------------

func TestAmpEnvAHDShortensTail(t *testing.T) {
	const duration = 0.5 // 500 ms sample
	pcm := sineWave(440, duration, testSR)

	// No envelope — plays full sample.
	sndFull := sampler.NewDrumSound("full")

	// AHD with 10 ms decay to zero.
	sndAHD := sampler.NewDrumSound("ahd")
	sndAHD.AmpEnv = sampler.AmpEnv{
		Type: sampler.EnvAHD,
		A:    0,
		H:    0,
		D:    0.010, // 10 ms
		R:    0.003,
	}

	const nSamples = int64(testSR / 2) // render 500 ms
	bufFull := renderPad0(&sndFull, pcm, 100, nSamples)
	bufAHD := renderPad0(&sndAHD, pcm, 100, nSamples)

	// Check energy in the last quarter of the buffer.
	lastQ := int(nSamples * 3 / 4)
	rmsFull := rmsAudio(bufFull[lastQ:])
	rmsAHD := rmsAudio(bufAHD[lastQ:])

	// AHD should be nearly silent in the last quarter; full-play should not.
	if rmsAHD > rmsFull*0.3 {
		t.Errorf("AHD decay did not shorten tail: late-quarter rmsAHD=%.4f rmsFull=%.4f",
			rmsAHD, rmsFull)
	}
	t.Logf("last-quarter RMS: full=%.4f  ahd=%.4f", rmsFull, rmsAHD)
}

// ---------------------------------------------------------------------------
// Determinism — same inputs + same seed → byte-identical output
// ---------------------------------------------------------------------------

func TestRenderSamplerPatternDeterministic(t *testing.T) {
	pcm := sineWave(440, 0.3, testSR)
	snd := sampler.NewDrumSound("kick")
	kitMap, kitPCM := makeSinglePadKit(&snd, pcm)
	m := makeTestMachine()
	pat := makePattern16(80)

	opts := RenderSamplerPatternOpts{
		Kit:              kitMap,
		KitPCM:           kitPCM,
		Pattern:          pat,
		Machine:          m,
		DeviceSampleRate: testSR,
		NumSamples:       int64(testSR / 2),
		RNGSeed:          42,
	}

	buf1 := RenderSamplerPattern(opts)
	buf2 := RenderSamplerPattern(opts)

	if len(buf1) == 0 {
		t.Fatal("first render is empty")
	}
	if len(buf1) != len(buf2) {
		t.Fatalf("render lengths differ: %d vs %d", len(buf1), len(buf2))
	}
	for i := range buf1 {
		if buf1[i] != buf2[i] {
			t.Errorf("render is non-deterministic at sample %d: %.8f vs %.8f", i, buf1[i], buf2[i])
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Degrade-never-crash: nil / missing inputs produce nil or silence, no panic
// ---------------------------------------------------------------------------

func TestRenderSamplerPatternDegrade(t *testing.T) {
	// nil Machine → nil (not panic)
	opts := RenderSamplerPatternOpts{DeviceSampleRate: testSR, NumSamples: 1000}
	if buf := RenderSamplerPattern(opts); buf != nil {
		t.Errorf("expected nil for nil Machine; got buf len %d", len(buf))
	}

	// zero NumSamples → nil
	snd := sampler.NewDrumSound("kick")
	kitMap, kitPCM := makeSinglePadKit(&snd, sineWave(440, 0.1, testSR))
	m := makeTestMachine()
	opts2 := RenderSamplerPatternOpts{
		Kit:              kitMap,
		KitPCM:           kitPCM,
		Pattern:          makePattern16(80),
		Machine:          m,
		DeviceSampleRate: testSR,
		NumSamples:       0,
	}
	if buf := RenderSamplerPattern(opts2); buf != nil {
		t.Errorf("expected nil for NumSamples=0; got len %d", len(buf))
	}

	// nil KitPCM → silent (degrade-never-crash)
	opts3 := RenderSamplerPatternOpts{
		Kit:              kitMap,
		KitPCM:           nil,
		Pattern:          makePattern16(80),
		Machine:          m,
		DeviceSampleRate: testSR,
		NumSamples:       1000,
	}
	buf3 := RenderSamplerPattern(opts3)
	if buf3 == nil {
		t.Error("expected non-nil buf for nil KitPCM (degrade to silent)")
		return
	}
	for i, s := range buf3 {
		if s != 0 {
			t.Errorf("expected silent buf for nil KitPCM, got %.4f at [%d]", s, i)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// resampleHermite edge cases
// ---------------------------------------------------------------------------

func TestResampleHermiteEdgeCases(t *testing.T) {
	// nil src → nil
	if r := resampleHermite(nil, 1.0); r != nil {
		t.Errorf("expected nil for nil src")
	}
	// empty src → nil
	if r := resampleHermite([]float32{}, 1.0); r != nil {
		t.Errorf("expected nil for empty src")
	}
	// zero ratio → nil
	if r := resampleHermite([]float32{1, 2, 3}, 0); r != nil {
		t.Errorf("expected nil for zero ratio")
	}
	// negative ratio → nil
	if r := resampleHermite([]float32{1, 2, 3}, -1); r != nil {
		t.Errorf("expected nil for negative ratio")
	}
	// zero semis → exact same slice pointer (no copy performed)
	pcm := sineWave(440, 0.1, testSR)
	out := resampleHermitePitch(pcm, 0)
	if &out[0] != &pcm[0] {
		t.Errorf("zero semis should return the original slice unchanged (same pointer)")
	}
}

// ---------------------------------------------------------------------------
// BuildSamplerKitPCMFromFloat32 — device-rate conversion
// ---------------------------------------------------------------------------

func TestBuildSamplerKitPCMResamples(t *testing.T) {
	// 1000-frame buffer at 22050 Hz → device rate 44100 should produce ~2000 frames.
	pcm := make([]float32, 1000)
	for i := range pcm {
		pcm[i] = float32(i) / 1000.0
	}
	kitPCM := BuildSamplerKitPCMFromFloat32(
		map[int]map[string][]float32{0: {"p": pcm}},
		22050, 44100,
	)
	got := kitPCM.pcmFor(0, "p")
	if got == nil {
		t.Fatal("pcmFor returned nil after resample")
	}
	want := 2000
	if math.Abs(float64(len(got)-want)) > 5 {
		t.Errorf("resampled length: got %d, want ~%d", len(got), want)
	}
}

func TestBuildSamplerKitPCMUnityRate(t *testing.T) {
	// Same src and dst rate → no resampling, length preserved.
	pcm := sineWave(440, 0.1, testSR)
	kitPCM := BuildSamplerKitPCMFromFloat32(
		map[int]map[string][]float32{0: {"p": pcm}},
		testSR, testSR,
	)
	got := kitPCM.pcmFor(0, "p")
	if got == nil {
		t.Fatal("pcmFor returned nil for unity rate")
	}
	if len(got) != len(pcm) {
		t.Errorf("unity rate changed length: got %d, want %d", len(got), len(pcm))
	}
}

// ---------------------------------------------------------------------------
// WriteMonoFloat32WAV — file is written and readable
// ---------------------------------------------------------------------------

func TestWriteMonoFloat32WAVCreatesFile(t *testing.T) {
	path := t.TempDir() + "/test.wav"
	pcm := sineWave(440, 0.05, testSR)
	if err := WriteMonoFloat32WAV(path, pcm, testSR); err != nil {
		t.Fatalf("WriteMonoFloat32WAV: %v", err)
	}
	// Verify the file exists and has a non-zero size.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("cannot stat written WAV: %v", err)
	}
	// Minimum: 44-byte header + 4*len(pcm) data bytes.
	minSize := int64(44 + 4*len(pcm))
	if info.Size() < minSize {
		t.Errorf("WAV too small: got %d bytes, want >= %d", info.Size(), minSize)
	}
}

func TestWriteMonoFloat32WAVEmptyBuf(t *testing.T) {
	path := t.TempDir() + "/empty.wav"
	if err := WriteMonoFloat32WAV(path, nil, testSR); err != nil {
		t.Fatalf("WriteMonoFloat32WAV(nil): %v", err)
	}
	if err := WriteMonoFloat32WAV(path, []float32{}, testSR); err != nil {
		t.Fatalf("WriteMonoFloat32WAV([]): %v", err)
	}
}
