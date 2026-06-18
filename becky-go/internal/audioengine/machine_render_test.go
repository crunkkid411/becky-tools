package audioengine

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"becky-go/internal/drummachine"
)

// --- test-only in-memory PCM16 mono WAV encoder (mirrors refmatch_test.go) so we
// can synthesize pad samples and feed dsp.DecodeWAV exactly as the real loader does.

func encodeTestWAV(samples []float64, sr int) []byte {
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
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // mono
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sr))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sr*2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(dataLen))
	buf.Write(data)
	return buf.Bytes()
}

// dcSample returns n samples all at value v (a trivial, easy-to-assert one-shot).
func dcSample(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// writeTestWAV writes a synthesized WAV into dir and returns its path.
func writeTestWAV(t *testing.T, dir, name string, samples []float64, sr int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, encodeTestWAV(samples, sr), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// machineWith builds a machine with a single 16-step pattern and a hit on pad p at
// the given steps; tempo defaults to 120.
func machineWith(t *testing.T, padHits map[int][]int) *drummachine.Machine {
	t.Helper()
	m := drummachine.NewMachine()
	for pad, steps := range padHits {
		for _, s := range steps {
			var err error
			m, err = m.SetStep(0, pad, s, true, 100)
			if err != nil {
				t.Fatalf("SetStep pad=%d step=%d: %v", pad, s, err)
			}
		}
	}
	return m
}

func TestSequenceMachinePattern_EventCountAndOffsets(t *testing.T) {
	const sr = 48000
	// Kick on steps 0,4,8,12. At 120 BPM a 1/16 step = (60/120)/4 = 0.125 s = 6000 samples.
	m := machineWith(t, map[int][]int{0: {0, 4, 8, 12}})
	pat, _ := m.PatternForScene(0)
	ev := SequenceMachinePattern(m, pat, sr)
	if len(ev) != 4 {
		t.Fatalf("event count = %d, want 4", len(ev))
	}
	wantOffsets := []int64{0, 24000, 48000, 72000} // step*6000
	for i, e := range ev {
		if e.SampleOffset != wantOffsets[i] {
			t.Errorf("event %d offset = %d, want %d", i, e.SampleOffset, wantOffsets[i])
		}
		if e.Velocity != 100 {
			t.Errorf("event %d velocity = %d, want 100", i, e.Velocity)
		}
		if e.Pad != 0 {
			t.Errorf("event %d pad = %d, want 0", i, e.Pad)
		}
	}
}

func TestSequenceMachinePattern_Empty(t *testing.T) {
	m := drummachine.NewMachine() // no hits
	pat, _ := m.PatternForScene(0)
	if ev := SequenceMachinePattern(m, pat, 48000); ev != nil {
		t.Errorf("empty pattern produced %d events, want nil", len(ev))
	}
	if SequenceMachinePattern(nil, pat, 48000) != nil {
		t.Errorf("nil machine should produce nil")
	}
	if SequenceMachinePattern(m, pat, 0) != nil {
		t.Errorf("zero sample rate should produce nil")
	}
}

func TestSequenceMachinePattern_MuteSolo(t *testing.T) {
	// Pads 0 and 1 both hit on step 0.
	m := machineWith(t, map[int][]int{0: {0}, 1: {0}})

	// Mute pad 1 -> only pad 0 sounds.
	muted, err := m.MutePad(1, true)
	if err != nil {
		t.Fatal(err)
	}
	pat, _ := muted.PatternForScene(0)
	ev := SequenceMachinePattern(muted, pat, 48000)
	if len(ev) != 1 || ev[0].Pad != 0 {
		t.Fatalf("after mute pad1: got %d events (first pad %v), want 1 event on pad 0", len(ev), firstPad(ev))
	}

	// Solo pad 1 -> only pad 1 sounds.
	soloed, err := m.SoloPad(1, true)
	if err != nil {
		t.Fatal(err)
	}
	pat2, _ := soloed.PatternForScene(0)
	ev2 := SequenceMachinePattern(soloed, pat2, 48000)
	if len(ev2) != 1 || ev2[0].Pad != 1 {
		t.Fatalf("after solo pad1: got %d events (first pad %v), want 1 event on pad 1", len(ev2), firstPad(ev2))
	}
}

func firstPad(ev []MachineEvent) int {
	if len(ev) == 0 {
		return -1
	}
	return ev[0].Pad
}

func TestSequenceMachinePattern_Swing(t *testing.T) {
	const sr = 48000
	// Hits on step 0 (on-beat) and step 1 (off-beat). With swing the off-beat delays.
	m := machineWith(t, map[int][]int{0: {0, 1}})
	// Apply maximum swing 0.75.
	var err error
	m, err = m.SetSwing(0, 0.75)
	if err != nil {
		t.Fatal(err)
	}
	pat, _ := m.PatternForScene(0)
	ev := SequenceMachinePattern(m, pat, sr)
	if len(ev) != 2 {
		t.Fatalf("want 2 events, got %d", len(ev))
	}
	// Step 0 stays at offset 0; step 1 delayed by up to half a step (3000 samples).
	if ev[0].SampleOffset != 0 {
		t.Errorf("on-beat offset = %d, want 0", ev[0].SampleOffset)
	}
	// Straight step1 would be 6000; swing should push it later (toward 9000).
	if ev[1].SampleOffset <= 6000 {
		t.Errorf("swung off-beat offset = %d, want > 6000 (delayed)", ev[1].SampleOffset)
	}
}

func TestApplyChokeCutoffs(t *testing.T) {
	const sr = 48000
	// Default kit puts pads 2 (closed hat) + 3 (open hat) in the same choke group.
	// Hit pad 3 (open) on step 0 and pad 2 (closed) on step 2: the open hat's decay
	// must be trimmed to end at the closed hat's offset.
	m := machineWith(t, map[int][]int{3: {0}, 2: {2}})
	pat, _ := m.PatternForScene(0)
	ev := SequenceMachinePattern(m, pat, sr)
	if len(ev) != 2 {
		t.Fatalf("want 2 events, got %d", len(ev))
	}
	// Find the pad-3 (open hat) event — it should be choked (DecaySec set > 0 and
	// equal to the gap to the pad-2 hit).
	var openHat *MachineEvent
	for i := range ev {
		if ev[i].Pad == 3 {
			openHat = &ev[i]
		}
	}
	if openHat == nil {
		t.Fatal("no pad-3 event found")
	}
	// Gap = 2 steps = 12000 samples = 0.25 s.
	wantDecay := 12000.0 / float64(sr)
	if math.Abs(openHat.DecaySec-wantDecay) > 1e-9 {
		t.Errorf("choked open-hat DecaySec = %v, want %v", openHat.DecaySec, wantDecay)
	}
}

func TestLoadMachineKit_MissingSamplesFallBack(t *testing.T) {
	dir := t.TempDir()
	// Pad 0 gets a real sample; pad 1 points at a missing file; pad 2 has no path.
	writeTestWAV(t, dir, "kick.wav", dcSample(0.5, 100), 48000)

	m := drummachine.NewMachine()
	var err error
	m, err = m.SetPadSample(0, "kick.wav") // relative -> joined with dir
	if err != nil {
		t.Fatal(err)
	}
	m, err = m.SetPadSample(1, "does-not-exist.wav")
	if err != nil {
		t.Fatal(err)
	}

	kit := LoadMachineKit(dir, m)
	if !kit.PadHasSample(0) {
		t.Error("pad 0 should have loaded its sample")
	}
	if kit.PadHasSample(1) {
		t.Error("pad 1 (missing file) should NOT have a sample (sine fallback)")
	}
	if kit.PadHasSample(2) {
		t.Error("pad 2 (no path) should NOT have a sample")
	}
	if kit.Len() != 1 {
		t.Errorf("kit Len = %d, want 1", kit.Len())
	}
	// nil machine never crashes.
	if LoadMachineKit(dir, nil).Len() != 0 {
		t.Error("nil machine should give an empty kit")
	}
}

func TestRenderMachine_GainApplied(t *testing.T) {
	const sr = 48000
	dir := t.TempDir()
	writeTestWAV(t, dir, "kick.wav", dcSample(0.5, 10), sr)

	m := drummachine.NewMachine()
	var err error
	m, err = m.SetPadSample(0, "kick.wav")
	if err != nil {
		t.Fatal(err)
	}
	// One hit on step 0, velocity 100.
	m, err = m.SetStep(0, 0, 0, true, 100)
	if err != nil {
		t.Fatal(err)
	}
	kit := LoadMachineKit(dir, m)

	render := func(level float64) float32 {
		mm, err := m.SetPadLevel(0, level)
		if err != nil {
			t.Fatal(err)
		}
		pat, _ := mm.PatternForScene(0)
		ev := SequenceMachinePattern(mm, pat, sr)
		buf := RenderMachine(ev, sr, 100, kit)
		return buf[0] // first sample = the one-shot's first frame
	}
	full := render(1.0)
	half := render(0.5)
	if full <= 0 {
		t.Fatalf("expected positive output at full level, got %v", full)
	}
	// Half level should be ~half the amplitude (pre-tanh both are small so ~linear).
	ratio := float64(half) / float64(full)
	if ratio < 0.45 || ratio > 0.55 {
		t.Errorf("half-level ratio = %v, want ~0.5", ratio)
	}
}

func TestRenderMachine_PitchShortensSample(t *testing.T) {
	// A +12 semitone (one octave up) pitch halves the resampled length.
	pcm := make([]float32, 1000)
	for i := range pcm {
		pcm[i] = 0.3
	}
	up := resamplePitch(pcm, 12)
	if len(up) < 480 || len(up) > 520 {
		t.Errorf("+12 semis length = %d, want ~500 (half)", len(up))
	}
	down := resamplePitch(pcm, -12)
	if len(down) < 1900 || len(down) > 2100 {
		t.Errorf("-12 semis length = %d, want ~2000 (double)", len(down))
	}
	if resamplePitch(pcm, 0) == nil {
		t.Error("0 semis should return the input unchanged")
	}
}

func TestRenderMachine_DecayEnvelope(t *testing.T) {
	const sr = 48000
	// A long DC one-shot with a short decay: late samples must be quieter than early.
	pcm := make([]float32, sr) // 1 s of DC 1.0
	for i := range pcm {
		pcm[i] = 1.0
	}
	// 100 ms decay over a 1.0 DC sample.
	play := machinePlay{pcm: pcm, amp: 1.0, decayLen: decaySamplesFor(0.1, len(pcm), sr)}
	early := play.envAt()
	play.pos = play.decayLen / 2
	mid := play.envAt()
	play.pos = play.decayLen
	end := play.envAt()
	if !(early > mid && mid > end) {
		t.Errorf("decay env not monotonic: early=%v mid=%v end=%v", early, mid, end)
	}
	if end != 0 {
		t.Errorf("decay end env = %v, want 0", end)
	}
}

func TestRenderMachine_SineFallbackWhenNoSample(t *testing.T) {
	const sr = 48000
	// No kit at all -> every pad uses the sine fallback; output must be non-silent.
	m := machineWith(t, map[int][]int{0: {0}})
	pat, _ := m.PatternForScene(0)
	ev := SequenceMachinePattern(m, pat, sr)
	buf := RenderMachine(ev, sr, 4800, nil) // nil kit
	var energy float64
	for _, s := range buf {
		energy += math.Abs(float64(s))
	}
	if energy == 0 {
		t.Error("sine fallback produced silence; want audible output")
	}
}

func TestRenderMachine_Deterministic(t *testing.T) {
	const sr = 48000
	dir := t.TempDir()
	writeTestWAV(t, dir, "kick.wav", dcSample(0.4, 200), sr)
	writeTestWAV(t, dir, "snare.wav", dcSample(0.3, 150), sr)

	m := drummachine.NewMachine()
	var err error
	m, err = m.SetPadSample(0, "kick.wav")
	if err != nil {
		t.Fatal(err)
	}
	m, err = m.SetPadSample(1, "snare.wav")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []int{0, 4, 8, 12} {
		m, _ = m.SetStep(0, 0, s, true, 100)
	}
	for _, s := range []int{4, 12} {
		m, _ = m.SetStep(0, 1, s, true, 90)
	}
	kit := LoadMachineKit(dir, m)
	pat, _ := m.PatternForScene(0)

	render := func() []float32 {
		ev := SequenceMachinePattern(m, pat, sr)
		return RenderMachine(ev, sr, 96000, kit)
	}
	a := render()
	b := render()
	if len(a) != len(b) {
		t.Fatalf("length mismatch %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at sample %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestRenderMachine_EmptyAndInvalid(t *testing.T) {
	if RenderMachine(nil, 48000, 0, nil) != nil {
		t.Error("numSamples 0 should return nil")
	}
	if RenderMachine(nil, 0, 100, nil) != nil {
		t.Error("sampleRate 0 should return nil")
	}
	buf := RenderMachine(nil, 48000, 100, nil)
	if len(buf) != 100 {
		t.Fatalf("empty schedule should return a 100-sample silent buffer, got %d", len(buf))
	}
	for _, s := range buf {
		if s != 0 {
			t.Fatal("empty schedule buffer should be silent")
		}
	}
}

func TestMachineDurationAndLoopSamples(t *testing.T) {
	const sr = 48000
	m := machineWith(t, map[int][]int{0: {0, 8}})
	pat, _ := m.PatternForScene(0)
	ev := SequenceMachinePattern(m, pat, sr)
	dur := MachineDurationSamples(ev, sr)
	// last hit at step 8 = 48000, plus 1 s tail = 96000.
	if dur != 96000 {
		t.Errorf("duration = %d, want 96000", dur)
	}
	// 16 steps at 120 BPM: 16 * 6000 = 96000 samples for one cycle.
	loop := MachineLoopSamples(m, pat, sr)
	if loop != 96000 {
		t.Errorf("loop samples = %d, want 96000", loop)
	}
	if MachineDurationSamples(nil, sr) != 0 {
		t.Error("empty schedule duration should be 0")
	}
}

func TestIsAbsCrossPlatform(t *testing.T) {
	cases := []struct {
		p    string
		want bool
	}{
		{"", false},
		{"kick.wav", false},
		{"kits/kick.wav", false},
		{`kits\kick.wav`, false},
		{"/usr/share/kick.wav", true},
		{`C:\kits\kick.wav`, true},
		{"C:/kits/kick.wav", true},
		{`\\server\share\kick.wav`, true},
		{"z:/x.wav", true},
	}
	for _, c := range cases {
		if got := isAbsCrossPlatform(c.p); got != c.want {
			t.Errorf("isAbsCrossPlatform(%q) = %v, want %v", c.p, got, c.want)
		}
	}
}
