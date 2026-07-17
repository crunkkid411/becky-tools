package main

// peaks2.go — the ACCURATE waveform verb (Phase-2 of Becky Review Native).
//
// The original `peaks` verb (peaks.go) is a coarse GUIDE: 8 kHz decode, 200
// max(abs) buckets, normalized per clip — the loudest moment in ANY window
// reads as 1.0, so levels can't be compared across clips or against a
// threshold. `peaks2` is the honest version: TRUE min/max per requested
// column (one column per screen pixel) at ABSOLUTE scale (1.0 = digital
// full scale, int16 32767), decoded at 48 kHz mono.
//
// It shares the SAME on-disk peak cache the native timeline (becky-timeline,
// native/becky-timeline/main.cpp) already writes:
//   %LOCALAPPDATA%\becky\peaks\<fnv1a64(path|size|mtime)>.bpk   (BPK2 format)
// L0 = one (min,max) int8 pair per 64 samples @48kHz = 750 bins/sec, plus a
// per-SECOND coverage map so only never-decoded seconds are decoded (windowed,
// never the whole multi-GB source). Decode once ever, by whichever side gets
// there first — the engine reads the timeline's cache for free and vice versa.
//
// Additive only: the existing `peaks` verb and its clients are untouched.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	"becky-go/internal/mediainfo"
)

// BPK cache constants — MUST match native/becky-timeline/main.cpp (kSpb,
// kPeakRate) or the two sides stop sharing cache files.
const (
	bpkSamplesPerBin = 64
	bpkRate          = 48000
	bpkBinsPerSec    = float64(bpkRate) / float64(bpkSamplesPerBin) // 750
)

// defaultPeak2Cols / maxPeak2Cols bound the column count: one column per
// screen pixel is the intent, so the cap is a generous "widest plausible
// monitor", not a data-resolution limit.
const (
	defaultPeak2Cols = 1000
	maxPeak2Cols     = 8000
)

// Peaks2Result is the reply: per-column true min/max at ABSOLUTE scale
// (-1..1, where 1.0 = int16 full scale). Arrays are always present (never
// null). Columns whose window was silent or could not be decoded are 0,0.
// Cache is the .bpk cache file name (verification/debug aid).
type Peaks2Result struct {
	Min   []float64 `json:"min"`
	Max   []float64 `json:"max"`
	Count int       `json:"count"`
	Cache string    `json:"cache,omitempty"`
}

// emptyPeaks2 is the shared degrade reply (unresolved source, no duration,
// no cache dir) — the waveform lane just stays blank, never an error.
func emptyPeaks2() Peaks2Result {
	return Peaks2Result{Min: []float64{}, Max: []float64{}, Count: 0}
}

// peaks2Mu serializes the whole verb. ponytail: one global lock — decode is
// the only slow part and verb calls arrive one at a time from the bridge;
// per-source locks if concurrent callers ever matter.
var peaks2Mu sync.Mutex

// Peaks2 returns true min/max per column for source's [inSec,outSec) window,
// backed by the shared .bpk pyramid cache. Missing seconds are decoded via
// ffmpeg (48 kHz mono, windowed — only the uncovered runs) and saved back so
// the next caller (engine OR native timeline) pays nothing. Degrade-never-
// crash: every failure path returns empty arrays, never an error.
func (a *App) Peaks2(source string, inSec, outSec float64, cols int) Peaks2Result {
	cols = resolvePeak2Cols(cols)
	v, ok := a.resolveSource(source)
	if !ok {
		return emptyPeaks2()
	}
	if outSec < inSec {
		inSec, outSec = outSec, inSec
	}
	inSec = clampNonNeg(inSec)
	if outSec <= inSec {
		return emptyPeaks2()
	}
	a.mu.Lock()
	ffmpeg := a.cfg.FFmpeg
	ffprobe := a.cfg.FFprobe
	a.mu.Unlock()

	peaks2Mu.Lock()
	defer peaks2Mu.Unlock()

	cachePath, ok := bpkCachePath(v.Path)
	if !ok {
		return emptyPeaks2()
	}
	p := loadBPK(cachePath)
	if p == nil {
		dur := probeDuration(a, ffprobe, source, v.Path)
		if dur <= 0 {
			return emptyPeaks2()
		}
		p = newPeakFile(cachePath, dur)
	}
	if outSec > p.duration {
		outSec = p.duration
	}
	if outSec <= inSec {
		return emptyPeaks2()
	}

	// Decode only the whole-second runs the cache has never covered.
	if ffmpeg != "" {
		for _, r := range p.uncoveredRuns(int(inSec), int(math.Ceil(outSec))) {
			samples, err := decodePCMWindow(ffmpeg, v.Path, float64(r[0]), float64(r[1]), bpkRate)
			if err != nil {
				continue // that run stays empty → zero columns, never a crash
			}
			p.fill(samples, uint64(r[0])*bpkRate)
			p.markSeconds(r[0], r[1])
		}
	}
	if p.dirty {
		p.save() // best-effort; the reply is correct either way
	}
	mins, maxs := p.columns(inSec, outSec, cols)
	return Peaks2Result{Min: mins, Max: maxs, Count: cols, Cache: filepath.Base(cachePath)}
}

// resolvePeak2Cols applies the column-count contract: <=0 becomes the
// default, and the cap guards a runaway payload. PURE.
func resolvePeak2Cols(cols int) int {
	if cols <= 0 {
		cols = defaultPeak2Cols
	}
	if cols > maxPeak2Cols {
		cols = maxPeak2Cols
	}
	return cols
}

// probeDuration finds the source duration for sizing a fresh cache: the
// configured ffprobe first (what export.go trusts), then App.Probe's
// env/PATH fallback (what the GUI timeline trusts).
func probeDuration(a *App, ffprobe, source, path string) float64 {
	if ffprobe != "" {
		if info, err := mediainfo.Probe(ffprobe, path); err == nil && info.Duration > 0 {
			return info.Duration
		}
	}
	return a.Probe(source).Duration
}

// ---------------------------------------------------------------------------
// The shared .bpk cache (BPK2) — format twin of native/becky-timeline/main.cpp
// ---------------------------------------------------------------------------

// peakFile is one source's L0 min/max lane plus its per-second coverage map.
// n0[i] > x0[i] (127 > -128) is the EMPTY sentinel for a never-decoded bin.
type peakFile struct {
	path      string
	n0, x0    []int8
	secFilled []uint8
	duration  float64
	dirty     bool
}

// fnv1a64 is the same hash the C++ side uses for cache file names.
func fnv1a64(s string) uint64 {
	h := uint64(1469598103934665603)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// windowsFiletime converts a file's mod time to the Windows FILETIME integer
// (100ns ticks since 1601) — the exact value the C++ side hashes, recovered
// losslessly from Go's ModTime (which Windows fills FROM that FILETIME).
func windowsFiletime(fi os.FileInfo) uint64 {
	return uint64(fi.ModTime().UnixNano()/100 + 116444736000000000)
}

// bpkCachePath computes the shared cache file for source:
// %LOCALAPPDATA%\becky\peaks\<fnv1a64(path|size|filetime)>.bpk — byte-for-byte
// the C++ peaksCachePath() scheme, so both sides land on the SAME file.
func bpkCachePath(source string) (string, bool) {
	fi, err := os.Stat(source)
	if err != nil {
		return "", false
	}
	key := fmt.Sprintf("%s|%d|%d", source, fi.Size(), windowsFiletime(fi))
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = "."
	}
	dir := filepath.Join(base, "becky", "peaks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false
	}
	return filepath.Join(dir, fmt.Sprintf("%016x.bpk", fnv1a64(key))), true
}

// newPeakFile sizes sentinel-initialized arrays for `duration` seconds —
// same sizing arithmetic as the C++ sizeArrays().
func newPeakFile(path string, duration float64) *peakFile {
	bins := int(duration*bpkBinsPerSec) + 2
	p := &peakFile{
		path:      path,
		n0:        make([]int8, bins),
		x0:        make([]int8, bins),
		secFilled: make([]uint8, int(duration)+2),
		duration:  duration,
	}
	for i := range p.n0 {
		p.n0[i] = 127
		p.x0[i] = -128
	}
	return p
}

// loadBPK parses a BPK1/BPK2 cache file. Any mismatch (wrong constants, torn
// concurrent write, truncation) returns nil — the caller just re-decodes, so
// a racing becky-timeline save can never corrupt a reply.
func loadBPK(path string) *peakFile {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) < 28 {
		return nil
	}
	magic := string(raw[:4])
	if magic != "BPK1" && magic != "BPK2" {
		return nil
	}
	spb := binary.LittleEndian.Uint32(raw[4:8])
	rate := binary.LittleEndian.Uint32(raw[8:12])
	count := binary.LittleEndian.Uint64(raw[12:20])
	dur := math.Float64frombits(binary.LittleEndian.Uint64(raw[20:28]))
	if spb != bpkSamplesPerBin || rate != bpkRate || count >= 1<<32 || dur <= 0 {
		return nil
	}
	p := newPeakFile(path, dur)
	off := 28
	if magic == "BPK2" {
		if len(raw) < off+4 {
			return nil
		}
		secN := int(binary.LittleEndian.Uint32(raw[off : off+4]))
		off += 4
		if secN > len(p.secFilled) || len(raw) < off+secN {
			return nil
		}
		copy(p.secFilled, raw[off:off+secN])
		off += secN
	} else {
		for i := range p.secFilled { // BPK1 = whole file decoded
			p.secFilled[i] = 1
		}
	}
	n := int(count)
	if n > len(p.n0) {
		n = len(p.n0)
	}
	if len(raw) < off+int(count)*2 {
		return nil
	}
	for i := 0; i < n; i++ {
		p.n0[i] = int8(raw[off+i*2])
		p.x0[i] = int8(raw[off+i*2+1])
	}
	return p
}

// save writes the BPK2 file (temp + rename so a concurrent reader never sees
// a half-written cache). Best-effort: a failed save only costs a re-decode.
func (p *peakFile) save() {
	var buf bytes.Buffer
	buf.WriteString("BPK2")
	binary.Write(&buf, binary.LittleEndian, uint32(bpkSamplesPerBin))
	binary.Write(&buf, binary.LittleEndian, uint32(bpkRate))
	binary.Write(&buf, binary.LittleEndian, uint64(len(p.n0)))
	binary.Write(&buf, binary.LittleEndian, p.duration)
	binary.Write(&buf, binary.LittleEndian, uint32(len(p.secFilled)))
	buf.Write(p.secFilled)
	pairs := make([]byte, len(p.n0)*2)
	for i := range p.n0 {
		pairs[i*2] = byte(p.n0[i])
		pairs[i*2+1] = byte(p.x0[i])
	}
	buf.Write(pairs)
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, p.path); err != nil {
		os.Remove(tmp)
		return
	}
	p.dirty = false
}

// fill folds raw 48 kHz mono samples starting at absolute sample position
// startSample into the L0 min/max bins — the same >>8 int8 quantization the
// C++ decoder uses, so both writers produce comparable bins.
func (p *peakFile) fill(samples []int16, startSample uint64) {
	for i, s := range samples {
		bin := int((startSample + uint64(i)) / bpkSamplesPerBin)
		if bin >= len(p.n0) {
			break
		}
		q := int8(s >> 8)
		if q < p.n0[bin] {
			p.n0[bin] = q
		}
		if q > p.x0[bin] {
			p.x0[bin] = q
		}
	}
}

// markSeconds records whole seconds [a,b) as decoded in the coverage map.
func (p *peakFile) markSeconds(a, b int) {
	for s := a; s < b && s < len(p.secFilled); s++ {
		if s >= 0 {
			p.secFilled[s] = 1
		}
	}
	p.dirty = true
}

// uncoveredRuns returns the maximal runs of never-decoded whole seconds
// inside [a,b) — exactly what still needs an ffmpeg decode.
func (p *peakFile) uncoveredRuns(a, b int) [][2]int {
	if a < 0 {
		a = 0
	}
	if b > len(p.secFilled) {
		b = len(p.secFilled)
	}
	var runs [][2]int
	runA := -1
	for s := a; s < b; s++ {
		if p.secFilled[s] == 0 && runA < 0 {
			runA = s
		}
		if p.secFilled[s] != 0 && runA >= 0 {
			runs = append(runs, [2]int{runA, s})
			runA = -1
		}
	}
	if runA >= 0 {
		runs = append(runs, [2]int{runA, b})
	}
	return runs
}

// columns reduces the [inSec,outSec) window to `cols` true min/max pairs at
// absolute scale: each column is the min/max over ALL of its L0 bins (never
// a resample), so no transient is ever dropped and silence is honestly ~0.
// Empty (never-decoded) columns are 0,0. PURE over the loaded bins.
func (p *peakFile) columns(inSec, outSec float64, cols int) (mins, maxs []float64) {
	mins = make([]float64, cols)
	maxs = make([]float64, cols)
	span := outSec - inSec
	for i := 0; i < cols; i++ {
		b0 := int((inSec + span*float64(i)/float64(cols)) * bpkBinsPerSec)
		b1 := int((inSec + span*float64(i+1)/float64(cols)) * bpkBinsPerSec)
		if b1 <= b0 {
			b1 = b0 + 1
		}
		if b1 > len(p.n0) {
			b1 = len(p.n0)
		}
		mn, mx := int8(127), int8(-128)
		for b := b0; b < b1 && b >= 0; b++ {
			if p.n0[b] > p.x0[b] {
				continue // empty sentinel
			}
			if p.n0[b] < mn {
				mn = p.n0[b]
			}
			if p.x0[b] > mx {
				mx = p.x0[b]
			}
		}
		if mn > mx {
			continue // whole column undecoded/silent → 0,0
		}
		mins[i] = clampUnit(float64(mn) / 127.0)
		maxs[i] = clampUnit(float64(mx) / 127.0)
	}
	return mins, maxs
}

// clampUnit clamps to [-1,1] (int8 -128 would otherwise read -1.008).
func clampUnit(v float64) float64 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}
