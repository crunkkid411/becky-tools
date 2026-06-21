package main

// machine.go — the `--play-machine` / `--play-pad` CLI seam for the 16-pad
// drummachine. NEW FILE: it does not touch main.go's existing flags. It hooks in
// via an init() that scans os.Args BEFORE main()/flag.Parse runs, so a stray
// drummachine flag never collides with the device-selection FlagSet in main.go.
//
// Two modes (the canvas GUI execs the engine for sound — the proven becky-canvas
// pattern):
//
//	becky-daw-engine --play-machine <machine.json> [--loops N] [--kit DIR]
//	    ▶ a pattern: load the kit, render the pattern, play it (looped if N>1).
//	becky-daw-engine --play-pad <machine.json> --pad N [--vel V] [--kit DIR]
//	    audition pad N once (instant feedback on a pad click).
//
// Sound itself requires the `-tags audio` build (machine.go calls into
// playMachineAudio / playPadAudio, which are real only under //go:build audio; the
// no-audio build prints a rebuild hint and exits 2 — same contract as
// --play-pattern-audio).
//
// Offline schedule dump (no audio build, no hardware): with --schedule it prints
// the computed []MachineEvent as JSON so the GUI/tests can inspect timing without
// sound. Exit codes match main.go: 0 ok; 1 error; 2 degrade.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/audioengine"
	"becky-go/internal/dawmodel"
	"becky-go/internal/drummachine"
	"becky-go/internal/sampledecode"
	"becky-go/internal/sampler"
)

// machineFlagNames are the drummachine subcommand flags this seam owns.
var machineFlagNames = []string{
	"-play-machine", "--play-machine",
	"-play-pad", "--play-pad",
	"-render-machine", "--render-machine",
	"-render-arrangement", "--render-arrangement",
	"-render-song", "--render-song",
}

// hasMachineFlag reports whether args contain one of our subcommand flags (also
// matching the `--play-machine=foo` form).
func hasMachineFlag(args []string) bool {
	for _, a := range args {
		for _, name := range machineFlagNames {
			if a == name || strings.HasPrefix(a, name+"=") {
				return true
			}
		}
	}
	return false
}

// init runs before main(): if a drummachine subcommand is present we handle it and
// exit, so main.go's default FlagSet never sees (and rejects) our flags. When none
// is present this is a no-op and main() proceeds unchanged.
func init() {
	args := os.Args[1:]
	if !hasMachineFlag(args) {
		return
	}
	os.Exit(machineModeRun(args))
}

// machineModeRun parses and dispatches the drummachine subcommands. It uses its own
// FlagSet (ContinueOnError, output discarded) so usage never leaks into main.go.
func machineModeRun(args []string) int {
	fs := flag.NewFlagSet("machine", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	playMachine := fs.String("play-machine", "", "render and play a machine.json drum pattern")
	playPad := fs.String("play-pad", "", "audition a single pad of a machine.json")
	renderMachine := fs.String("render-machine", "", "offline bounce: render a machine.json pattern to a WAV file (pure Go, no audio build needed)")
	renderArr := fs.String("render-arrangement", "", "offline bounce: render a dawmodel project.json's drum clip to a WAV — the SAME chain the canvas drum ▶ uses (MachineFromArrangement + default kit), pure Go")
	renderSong := fs.String("render-song", "", "offline bounce: render a WHOLE project.json (drums + bass + chords + melody) to a WAV via the synth + drum kit, pure Go")
	pad := fs.Int("pad", 0, "with --play-pad: the pad index 0..15 to audition")
	vel := fs.Int("vel", 100, "with --play-pad: velocity 1..127")
	loops := fs.Int("loops", 1, "with --play-machine: tile the pattern N times for a seamless loop")
	outWAV := fs.String("out", "", "with --render-machine: output WAV path (default: <machine-stem>.wav next to the input)")
	rngSeed := fs.Int64("seed", 42, "with --render-machine: RNG seed for deterministic round-robin (default 42)")
	kitDir := fs.String("kit", "", "kit directory to resolve relative pad SamplePaths (default: machine.json's folder)")
	schedule := fs.Bool("schedule", false, "print the computed event schedule as JSON instead of playing (offline, no audio)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "usage: becky-daw-engine [--play-machine <machine.json> [--loops N] | --play-pad <machine.json> --pad N [--vel V] | --render-machine <machine.json> [--out WAV] [--seed N]] [--kit DIR] [--schedule]")
		return 2
	}

	switch {
	case *renderSong != "":
		return runRenderSong(*renderSong, *outWAV)
	case *renderArr != "":
		return runRenderArrangement(*renderArr, *kitDir, *outWAV, *rngSeed)
	case *renderMachine != "":
		return runRenderMachine(*renderMachine, *kitDir, *outWAV, *rngSeed)
	case *playPad != "":
		return runPlayPad(*playPad, *kitDir, *pad, *vel, *schedule)
	case *playMachine != "":
		return runPlayMachine(*playMachine, *kitDir, *loops, *schedule)
	default:
		fmt.Fprintln(os.Stderr, "machine: no subcommand value (use --play-machine <file>, --play-pad <file> --pad N, or --render-machine <file>)")
		return 2
	}
}

// runRenderSong is the --render-song offline bounce: it renders a WHOLE project.json
// (every track — drums via the kit, bass/chords/melody via the synth) to a WAV, pure
// Go. This is the reusable pipe stage so anything that emits a dawmodel project.json
// (becky-canvas, becky-compose, becky-arrange) can be turned into an audible file with
// one command — the same renderer becky-song uses.
func runRenderSong(path, outPath string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "render-song: read:", err)
		return 1
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		fmt.Fprintln(os.Stderr, "render-song: parse project.json:", err)
		return 1
	}
	if outPath == "" {
		outPath = strings.TrimSuffix(path, filepath.Ext(path)) + ".wav"
	}
	if err := audioengine.RenderArrangementWAV(&arr, outPath, 48000, 1); err != nil {
		fmt.Fprintln(os.Stderr, "render-song:", err)
		return 2
	}
	fmt.Printf("render-song: wrote %s\n", outPath)
	return 0
}

// runRenderArrangement is the --render-arrangement offline bounce: it loads a
// dawmodel project.json, converts its drum clip to a Machine with EXACTLY the chain
// becky-canvas's drum ▶ uses (drummachine.MachineFromArrangement +
// WithDefaultKitSamples), then renders it to a WAV via the pure-Go sampler. This is
// the one command that PROVES the canvas drum audio works (ffprobe the WAV) without a
// GUI, an audio device, or a GPU — so the cloud→local handoff has a concrete,
// skippable-by-nobody verification step.
func runRenderArrangement(path, kitDirFlag, outPath string, seed int64) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "render-arrangement: read:", err)
		return 1
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		fmt.Fprintln(os.Stderr, "render-arrangement: parse project.json:", err)
		return 1
	}
	kitDir := kitDirFlag
	if kitDir == "" {
		kitDir = os.Getenv("BECKY_DRUM_KIT")
	}
	if kitDir == "" {
		kitDir = `X:/AI-2/becky-tools/samples/kit`
	}
	m := drummachine.MachineFromArrangement(&arr).WithDefaultKitSamples(kitDir)

	tmp, err := os.CreateTemp("", "becky-render-machine-*.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "render-arrangement: temp:", err)
		return 1
	}
	defer os.Remove(tmp.Name())
	body, _ := json.MarshalIndent(m, "", "  ")
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		fmt.Fprintln(os.Stderr, "render-arrangement: stage machine:", err)
		return 1
	}
	tmp.Close()

	if outPath == "" {
		outPath = strings.TrimSuffix(path, filepath.Ext(path)) + ".wav"
	}
	return runRenderMachine(tmp.Name(), kitDir, outPath, seed)
}

// runRenderMachine is the --render-machine offline bounce. It renders one pattern bar
// of the first scene in machine.json to a mono PCM WAV at 48 kHz, using the
// SamplerKitPCM + RenderSamplerPattern engine (velocity, AmpEnv, Hermite resampling,
// declick, round-robin — all the P0/P1 musical correctness). Pure Go: no audio build
// tag, no cgo, works on CI. Output is byte-identical for a given seed.
//
// Pads with a SamplePath are decoded via sampledecode and wrapped in a minimal
// sampler.Sound (NewDrumSound defaults: AmpVelTrack=1, DeclickMs=3, EnvOneshot).
// Relative SamplePaths are resolved against kitDir (or the machine.json directory).
// Pads without a SamplePath are silent (nil entry in SamplerKitMap).
//
// If --out is empty the WAV is written next to the machine.json as <stem>.wav.
// Degrade-never-crash: unreadable samples are skipped with a warning; an empty
// schedule (no audible hits) exits 2 (degrade) instead of writing silence.
func runRenderMachine(path, kitDirFlag, outPath string, rngSeed int64) int {
	m, kitDir, code := loadMachineFile(path, kitDirFlag)
	if code != -1 {
		return code
	}

	pat, ok := m.PatternForScene(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "render-machine: no playable pattern in scene 0 (degrade)")
		return 2
	}

	const sr = 48000
	numSamples := audioengine.MachineLoopSamples(m, pat, sr)
	if numSamples <= 0 {
		fmt.Fprintln(os.Stderr, "render-machine: pattern has zero duration (degrade)")
		return 2
	}

	// Build SamplerKitMap + decode PCM for every pad that has a SamplePath.
	var kitMap audioengine.SamplerKitMap
	// variantPCM: padIdx -> samplePath -> []float32 (mono, native sample rate).
	// srcRate: samplePath -> source rate (we track per-path to resample correctly).
	type pcmEntry struct {
		mono  []float32
		srcSR int
	}
	decoded := make(map[string]pcmEntry) // path → decoded (memoize; same file may appear on multiple pads)

	audiblePads := 0
	for padIdx := 0; padIdx < drummachine.PadCount; padIdx++ {
		p := m.Kit.Pads[padIdx]
		if p.SamplePath == "" {
			continue // nil in kitMap → silence
		}
		absPath := p.SamplePath
		if !filepath.IsAbs(absPath) {
			absPath = filepath.Join(kitDir, absPath)
		}

		entry, already := decoded[absPath]
		if !already {
			audio, err := sampledecode.DecodeWAVFile(absPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "render-machine: pad %d sample %q: %v (skipping)\n", padIdx, absPath, err)
				continue
			}
			// Downmix to mono: average all channels per frame.
			mono := make([]float32, audio.Frames)
			ch := audio.Channels
			if ch < 1 {
				ch = 1
			}
			for f := 0; f < audio.Frames; f++ {
				var sum float32
				for c := 0; c < ch && f*ch+c < len(audio.Samples); c++ {
					sum += audio.Samples[f*ch+c]
				}
				mono[f] = sum / float32(ch)
			}
			entry = pcmEntry{mono: mono, srcSR: audio.SampleRate}
			decoded[absPath] = entry
		}

		// Build a minimal Sound with one all-velocity layer / one variant.
		snd := sampler.NewDrumSound(p.Name)
		variant := sampler.Variant{
			SamplePath:     absPath,
			PitchKeycenter: sampler.DefaultKeycenter,
		}
		snd.Layers = []sampler.Layer{{
			VelLo:      1,
			VelHi:      127,
			RoundRobin: []sampler.Variant{variant},
		}}
		kitMap[padIdx] = &snd
		audiblePads++
	}

	if audiblePads == 0 {
		fmt.Fprintln(os.Stderr, "render-machine: no pads have a SamplePath — nothing to render (degrade)")
		return 2
	}

	// Build SamplerKitPCM: padIdx → samplePath → []float32 at device rate.
	variantFloat := make(map[int]map[string][]float32, audiblePads)
	for padIdx := 0; padIdx < drummachine.PadCount; padIdx++ {
		snd := kitMap[padIdx]
		if snd == nil {
			continue
		}
		for _, layer := range snd.Layers {
			for _, v := range layer.RoundRobin {
				entry, ok := decoded[v.SamplePath]
				if !ok {
					continue
				}
				if variantFloat[padIdx] == nil {
					variantFloat[padIdx] = make(map[string][]float32)
				}
				variantFloat[padIdx][v.SamplePath] = entry.mono
				// Store source rate for this pad (use first variant's rate — all come from same file here).
				_ = entry.srcSR // resampling happens inside BuildSamplerKitPCMFromFloat32
			}
		}
	}

	// Determine a representative source rate (use first decoded entry; resample in BuildSamplerKitPCMFromFloat32).
	srcRate := sr
	for _, e := range decoded {
		srcRate = e.srcSR
		break
	}

	kitPCM := audioengine.BuildSamplerKitPCMFromFloat32(variantFloat, srcRate, sr)

	buf := audioengine.RenderSamplerPattern(audioengine.RenderSamplerPatternOpts{
		Kit:              kitMap,
		KitPCM:           kitPCM,
		Pattern:          pat,
		Machine:          m,
		DeviceSampleRate: sr,
		NumSamples:       numSamples,
		RNGSeed:          rngSeed,
	})
	if len(buf) == 0 {
		fmt.Fprintln(os.Stderr, "render-machine: render produced no output (degrade)")
		return 2
	}

	// Default output path: <machine-stem>.wav next to the machine.json.
	if outPath == "" {
		base := filepath.Base(path)
		ext := filepath.Ext(base)
		stem := base[:len(base)-len(ext)]
		outPath = filepath.Join(dirOf(path), stem+".wav")
	}

	if err := audioengine.WriteMonoFloat32WAV(outPath, buf, sr); err != nil {
		fmt.Fprintln(os.Stderr, "render-machine: write WAV:", err)
		return 1
	}

	durSec := float64(numSamples) / float64(sr)
	fmt.Printf("render-machine: wrote %s (%.2fs, %d samples, %d audible pads, seed %d)\n",
		outPath, durSec, numSamples, audiblePads, rngSeed)
	return 0
}

// loadMachineFile reads + normalises a machine.json. Returns (machine, kitDir, code);
// code != -1 means the caller should return code (error/degrade).
func loadMachineFile(path, kitDirFlag string) (*drummachine.Machine, string, int) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "machine: cannot read file:", err)
		return nil, "", 1
	}
	defer func() { _ = f.Close() }()
	m, err := drummachine.Load(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "machine: cannot parse machine.json:", err)
		return nil, "", 1
	}
	kitDir := kitDirFlag
	if kitDir == "" {
		kitDir = dirOf(path) // default: resolve relative samples next to the file
	}
	return m, kitDir, -1
}

// runPlayMachine loads the machine + kit and either prints the schedule (--schedule)
// or plays it (real only under -tags audio; no-audio build prints a rebuild hint).
func runPlayMachine(path, kitDirFlag string, loops int, scheduleOnly bool) int {
	m, kitDir, code := loadMachineFile(path, kitDirFlag)
	if code != -1 {
		return code
	}
	const sr = 48000
	pat, ok := m.PatternForScene(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "machine: no playable pattern (degrade)")
		return 2
	}
	events := audioengine.SequenceMachinePattern(m, pat, sr)

	if scheduleOnly {
		return emitMachineSchedule(m, events)
	}
	if len(events) == 0 {
		fmt.Fprintln(os.Stderr, "machine: pattern is empty (no audible hits)")
		return 2
	}
	kit := audioengine.LoadMachineKitAt(kitDir, m, sr)
	fmt.Printf("Playing %s | tempo %.0f BPM | %d hits | %d pad sample(s) loaded | loops %d\n",
		path, m.Tempo, len(events), kit.Len(), loops)
	return playMachineAudio(m, kit, sr, loops)
}

// runPlayPad loads the machine + kit and auditions one pad (or prints its 1-event
// schedule under --schedule).
func runPlayPad(path, kitDirFlag string, pad, vel int, scheduleOnly bool) int {
	m, kitDir, code := loadMachineFile(path, kitDirFlag)
	if code != -1 {
		return code
	}
	if pad < 0 || pad >= drummachine.PadCount {
		fmt.Fprintf(os.Stderr, "machine: pad index %d out of range [0,%d)\n", pad, drummachine.PadCount)
		return 2
	}
	const sr = 48000
	if scheduleOnly {
		p := m.Kit.Pads[pad]
		ev := audioengine.MachineEvent{
			Pad: pad, Velocity: vel, Level: p.Level, Pan: p.Pan,
			PitchSemis: p.PitchSemitones, DecaySec: p.Decay, Note: p.MidiNote,
		}
		return emitMachineSchedule(m, []audioengine.MachineEvent{ev})
	}
	kit := audioengine.LoadMachineKitAt(kitDir, m, sr)
	fmt.Printf("Auditioning pad %d (%s) of %s | vel %d | %s\n",
		pad, m.Kit.Pads[pad].Name, path, vel, sampleOrSine(kit, pad))
	return playPadAudio(m, kit, pad, vel, sr)
}

// machineScheduleReport is the JSON shape emitted by --schedule.
type machineScheduleReport struct {
	Tempo  float64                    `json:"tempo"`
	Events []audioengine.MachineEvent `json:"events"`
}

// emitMachineSchedule prints the computed schedule as JSON. Exit 2 (degrade) when
// the schedule is empty so the GUI knows nothing would sound.
func emitMachineSchedule(m *drummachine.Machine, events []audioengine.MachineEvent) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(machineScheduleReport{Tempo: m.Tempo, Events: events}); err != nil {
		fmt.Fprintln(os.Stderr, "machine: encode error:", err)
		return 1
	}
	if len(events) == 0 {
		fmt.Fprintln(os.Stderr, "machine: schedule is empty (no audible hits)")
		return 2
	}
	return 0
}

// sampleOrSine reports whether a pad will play its sample or the sine fallback.
func sampleOrSine(kit *audioengine.MachineKit, pad int) string {
	if kit != nil && kit.PadHasSample(pad) {
		return "sample"
	}
	return "sine fallback (no sample)"
}

// dirOf returns the directory portion of a path, treating both '/' and '\' as
// separators so a Windows-style machine.json path resolves on Linux/CI too.
func dirOf(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[:i]
	}
	return "."
}
