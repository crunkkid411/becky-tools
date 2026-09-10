package main

// level.go — the DETECTION THRESHOLD, measured from the recording's own audio.
//
// WHY THIS EXISTS (2026-08-16, found on Jordan's IMG_9624.MP4).
//
// auto-editor's audio threshold is an ABSOLUTE level (its default is 4% of full
// scale, about -28 dBFS), so how much it cuts depends entirely on how loud the
// recording happens to be. Jordan's own working flow never handed it a raw
// camera file: it re-encoded first with `compand ... , volume=4, asoftclip`
// (about +14 dB on a quiet phone clip) and only THEN ran
// `auto-editor --edit audio:-27dB,stream=all`. becky-cut skipped that polish
// step and ran detection on the raw file, which is why the same clip came out
// shredded here and clean there.
//
// WHY IT CHANGED AGAIN (2026-09-10, the Rode Wireless GO II shoot).
//
// The first fix derived the threshold from ffmpeg volumedetect's single
// `mean_volume` and clamped it at -50 dB. A single RMS number cannot express a
// VALLEY, and the clamp put the right answer out of reach at ANY --headroom:
//
//	file                     mean   picked  needed   error
//	SNOW_20260823114143.mp4  -38.7  -37.7   -58.25   +20.5 dB
//	VTNZ3433.MP4             -44.4  -43.4   -63.25   +19.9 dB
//	LZTE3925.MP4             -42.3  -41.3   -62.75   +21.5 dB
//
// Measured on that 16-clip shoot, room tone sat at -75..-93 dBFS and speech at
// -30..-42 dBFS — a 48 dB valley. What generalises across mics is not a level
// but a POSITION INSIDE THAT VALLEY: Otsu's method, run over all 16 clips,
// landed at mean 0.523 / median 0.526 of the way up, and a flat 0.52 differs
// from Otsu's own pick by 1.2 dB on average inside a 48 dB gap. So the constant
// is 0.52, and unlike Otsu it makes no bimodal-histogram assumption to go wrong
// on a clip that is all speech or all silence. Evidence and the rejected
// alternatives: HANDOFF-BECKY-CUT-ADAPTIVE.md.
//
//	floor_db  = 5th  percentile of per-frame RMS dBFS   (room tone)
//	speech_db = 90th percentile of per-frame RMS dBFS   (programme level)
//	threshold = floor_db + 0.52 * (speech_db - floor_db)
//
// The measurement is the same 20 ms window / 10 ms hop envelope
// scripts/speechcut.py uses, done in Go off an ffmpeg PCM pipe so becky-cut
// needs neither Python nor numpy to pick its own threshold.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"becky-go/internal/proc"
)

const (
	// defaultThresholdDB is auto-editor's own default (4% amplitude = -27.96 dB).
	// The adaptive threshold never goes ABOVE it, so footage already at a normal
	// level behaves exactly as it did before any of this existed.
	defaultThresholdDB = -28.0
	// defaultValleyFraction is how far up the floor->speech valley the threshold
	// sits. 0.52 is Otsu's own measured position across the 16-clip Rode shoot.
	defaultValleyFraction = 0.52
	// defaultHeadroomDB is an ADDITIVE nudge on top of the adaptive value, in dB.
	// Zero by default — the whole point is that no dial needs turning.
	defaultHeadroomDB = 0.0
	// minValleyDB is the sanity gate. Under this the recording has no usable
	// separation between its silence and its speech (an all-speech clip, heavy
	// compression, a loud continuous background), the two percentiles are
	// measuring the same material, and the adaptive number is meaningless. We
	// fall back to auto-editor's default and SAY SO — loudly, never silently.
	minValleyDB = 12.0

	// The analysis envelope, identical to scripts/speechcut.py so both tools
	// report the same floor and speech level for the same file.
	levelSampleRate = 16000
	levelFrame      = 320 // 20 ms
	levelHop        = 160 // 10 ms -> 100 frames/sec
	levelEps        = 1e-10
)

// levelStats is one recording's own dynamic range, in dBFS.
type levelStats struct {
	FloorDB  float64 // 5th percentile of per-frame RMS — room tone
	SpeechDB float64 // 90th percentile — programme level
	ValleyDB float64 // SpeechDB - FloorDB
	Frames   int
}

// detectThresholdDB places the cut threshold inside the file's own valley.
// fraction is how far up it sits (0..1), headroomDB an additive nudge. The
// result is capped at auto-editor's default so this can only ever keep MORE
// audio than the pre-2026-08 behaviour on normal-level footage, never less.
// PURE — unit-tested in cut_test.go.
func detectThresholdDB(floorDB, speechDB, fraction, headroomDB float64) float64 {
	t := floorDB + fraction*(speechDB-floorDB) + headroomDB
	if t > defaultThresholdDB {
		t = defaultThresholdDB
	}
	return t
}

// percentile is numpy's linear-interpolation percentile over an ALREADY SORTED
// slice, so becky's numbers match speechcut.py's to the decimal.
// PURE — unit-tested.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	pos := p / 100.0 * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// statsFromFrameDB turns a per-frame dBFS envelope into the two percentiles.
// PURE — unit-tested. It sorts a copy, so the caller's slice is untouched.
func statsFromFrameDB(db []float64) levelStats {
	if len(db) == 0 {
		return levelStats{}
	}
	sorted := make([]float64, len(db))
	copy(sorted, db)
	sort.Float64s(sorted)
	floor := percentile(sorted, 5)
	speech := percentile(sorted, 90)
	return levelStats{
		FloorDB:  floor,
		SpeechDB: speech,
		ValleyDB: speech - floor,
		Frames:   len(db),
	}
}

// frameDB reads little-endian 16-bit mono PCM and returns per-frame RMS in
// dBFS, one value every levelHop samples over a levelFrame window — the same
// frame count as speechcut.py's `1 + (len(x) - FRAME) // HOP`.
// It always reads its input to EOF so the feeding process never blocks on a
// full pipe.
func frameDB(r io.Reader) []float64 {
	win := make([]float64, levelFrame)
	buf := make([]byte, 2*levelFrame)

	if _, err := io.ReadFull(r, buf); err != nil {
		// Shorter than one analysis window: nothing measurable.
		_, _ = io.Copy(io.Discard, r)
		return nil
	}
	for i := 0; i < levelFrame; i++ {
		win[i] = float64(int16(binary.LittleEndian.Uint16(buf[2*i:]))) / 32768.0
	}

	var out []float64
	hop := buf[:2*levelHop]
	for {
		out = append(out, rmsDB(win))
		if _, err := io.ReadFull(r, hop); err != nil {
			break // EOF, or a partial hop — the envelope ends here
		}
		copy(win, win[levelHop:])
		for i := 0; i < levelHop; i++ {
			win[levelFrame-levelHop+i] = float64(int16(binary.LittleEndian.Uint16(hop[2*i:]))) / 32768.0
		}
	}
	_, _ = io.Copy(io.Discard, r)
	return out
}

// rmsDB is one window's RMS in dBFS. PURE.
func rmsDB(win []float64) float64 {
	var sum float64
	for _, v := range win {
		sum += v * v
	}
	return 20.0 * math.Log10(math.Sqrt(sum/float64(len(win)))+levelEps)
}

// measureLevels decodes the input's audio ONCE (16 kHz mono, decode-only, no
// file written) and returns its floor/speech percentiles. Measured cost on this
// machine: about 15 seconds for 145 minutes of footage — it is already free, do
// not optimise it.
func measureLevels(ffmpeg, input string) (levelStats, error) {
	cmd := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-i", input,
		"-vn", "-ac", "1", "-ar", strconv.Itoa(levelSampleRate), "-f", "s16le", "-")
	proc.NoWindow(cmd)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return levelStats{}, err
	}
	if err := cmd.Start(); err != nil {
		return levelStats{}, fmt.Errorf("ffmpeg: %w", err)
	}
	db := frameDB(bufio.NewReaderSize(stdout, 1<<20))
	if wErr := cmd.Wait(); wErr != nil {
		return levelStats{}, fmt.Errorf("ffmpeg decode failed: %v: %s", wErr, tail(errBuf.String()))
	}
	if len(db) == 0 {
		return levelStats{}, fmt.Errorf("no audio to measure (no audio stream?)")
	}
	return statsFromFrameDB(db), nil
}

// editExpr builds auto-editor's --edit argument for a threshold in dB.
// stream=all matches Jordan's own command line (auto-edit.bat), so a clip with
// a second audio track is judged on all of its audio, not just stream 0.
func editExpr(thresholdDB float64) string {
	return fmt.Sprintf("audio:%.1fdB,stream=all", thresholdDB)
}
