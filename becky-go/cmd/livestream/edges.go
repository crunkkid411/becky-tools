package main

// edges.go - report item #6: turn the kept sentences into frame-exact source
// ranges. The transcript only says WHICH words stay; the AUDIO picks where each
// cut lands (ported from the apology edit's build_ranges.py, 2026-10-05):
//
//   - a pause between the last dropped word and the first kept word: the edge
//     goes on becky-cut's own keep boundary, so it is becky-cut's edge;
//   - no pause (the cut sits inside running speech): the quietest frame
//     boundary in the gap between the two words;
//   - an edge that is still louder than becky-cut's own speech threshold is
//     reported as a loud cut, so a person checks it.
//
// becky-cut (auto-editor + VAD, through BeckyCut.cs inside VEGAS) then removes
// the dead air inside the kept ranges exactly as it always does; finalPieces
// predicts that result so the timeline VEGAS builds can be checked against it.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
)

// cutReport is becky-cut's --dry-run JSON (the fields used here).
type cutReport struct {
	FPS         float64 `json:"fps"`
	ThresholdDB float64 `json:"threshold_db"`
	SpeechDB    float64 `json:"speech_db"`
	VADApplied  bool    `json:"vad_applied"`
	Decisions   []struct {
		Start  float64 `json:"start"`
		End    float64 `json:"end"`
		Status string  `json:"status"`
	} `json:"decisions"`
}

type span struct{ A, B float64 }

func loadCutReport(path string) (cutReport, error) {
	var r cutReport
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, fmt.Errorf("becky-cut report %s: %w", path, err)
	}
	if len(r.Decisions) == 0 {
		return r, fmt.Errorf("becky-cut report %s has no decisions", path)
	}
	return r, nil
}

// keeps returns becky-cut's keep spans, in time order.
func (r cutReport) keeps() []span {
	var out []span
	for _, d := range r.Decisions {
		if d.Status == "keep" && d.End > d.Start {
			out = append(out, span{d.Start, d.End})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].A < out[j].A })
	return out
}

// Range is one kept stretch of the source, on the frame grid.
type Range struct {
	In     float64 `json:"in"`
	Out    float64 `json:"out"`
	W0     int     `json:"w0"`
	W1     int     `json:"w1"`
	InHow  string  `json:"in_how"`
	OutHow string  `json:"out_how"`
	InDB   float64 `json:"in_db,omitempty"`  // loudness at a cut inside speech
	OutDB  float64 `json:"out_db,omitempty"` // (0 when the edge is becky-cut's)
	First  string  `json:"first"`
	Last   string  `json:"last"`
}

// audio is the source's 16 kHz mono PCM, for measuring loudness at a cut.
type audio struct {
	sr  int
	pcm []int16
}

// dbAt is the RMS level (dBFS) of the 20 ms around t.
func (a *audio) dbAt(t float64) float64 {
	lo, hi := int((t-0.01)*float64(a.sr)), int((t+0.01)*float64(a.sr))
	lo = max(lo, 0)
	hi = min(hi, len(a.pcm))
	if hi <= lo {
		return -120
	}
	var sum float64
	for _, v := range a.pcm[lo:hi] {
		sum += float64(v) * float64(v)
	}
	return 20 * math.Log10(math.Sqrt(sum/float64(hi-lo))/32768+1e-9)
}

// frameCut picks the best frame boundary in [t0, t1]: the quietest one. latest
// picks the LAST boundary within 3 dB of the quietest (starting tight matters
// more than ending tight).
func (a *audio) frameCut(t0, t1, fps float64, latest bool) (float64, string, float64) {
	f0, f1 := int(math.Ceil(t0*fps)), int(math.Floor(t1*fps))
	if f1 < f0 {
		c := math.Round((t0+t1)/2*fps) / fps
		return c, "mid", a.dbAt(c)
	}
	best, bestDB := 0.0, math.Inf(1)
	dbs := make([]float64, 0, f1-f0+1)
	for f := f0; f <= f1; f++ {
		d := a.dbAt(float64(f) / fps)
		dbs = append(dbs, d)
		if d < bestDB {
			best, bestDB = float64(f)/fps, d
		}
	}
	if latest {
		for i := len(dbs) - 1; i >= 0; i-- {
			if dbs[i] <= bestDB+3 {
				return float64(f0+i) / fps, "speech", dbs[i]
			}
		}
	}
	return best, "speech", bestDB
}

// wordMid is where a word's voice certainly is (zero-length words get 80 ms).
func wordMid(w Word) float64 {
	if w.End-w.Start > 0.05 {
		return (w.Start + w.End) / 2
	}
	return w.Start + 0.08
}

// keepIndex answers "which becky-cut keep span is at/after/before t".
type keepIndex []span

func (k keepIndex) at(t float64) (span, bool) {
	i := sort.Search(len(k), func(i int) bool { return k[i].A > t }) - 1
	if i >= 0 && k[i].A <= t && t < k[i].B {
		return k[i], true
	}
	return span{}, false
}

func (k keepIndex) after(t float64) (span, bool) {
	i := sort.Search(len(k), func(i int) bool { return k[i].A >= t })
	if i < len(k) {
		return k[i], true
	}
	return span{}, false
}

func (k keepIndex) before(t float64) (span, bool) {
	i := sort.Search(len(k), func(i int) bool { return k[i].A > t }) - 1
	for i >= 0 && k[i].B > t {
		i--
	}
	if i >= 0 {
		return k[i], true
	}
	return span{}, false
}

// contentRanges turns kept words (keep[i] per word) into merged frame-exact
// source ranges. duration caps the last edge.
func contentRanges(words []Word, keep []bool, keeps []span, au *audio, fps, duration float64) []Range {
	k := keepIndex(keeps)
	var out []Range
	for i := 0; i < len(words); {
		if !keep[i] {
			i++
			continue
		}
		fw := i
		for i < len(words) && keep[i] {
			i++
		}
		lw := i - 1
		r := Range{W0: fw, W1: lw, InHow: "file-start", OutHow: "file-end",
			First: wordsText(words, fw, min(fw+5, lw)), Last: wordsText(words, max(fw, lw-5), lw)}

		if fw > 0 {
			pw := words[fw-1]
			K, ok := k.at(words[fw].Start + 0.15)
			if !ok {
				K, ok = k.after(words[fw].Start)
			}
			if ok && K.A > wordMid(pw) && K.A < words[fw].Start+1.0 {
				r.In, r.InHow = K.A, "becky-cut"
			} else {
				r.In, r.InHow, r.InDB = au.frameCut(math.Min(pw.End, words[fw].Start)-0.1, words[fw].Start+0.1, fps, true)
			}
		}
		if lw+1 < len(words) {
			nw := words[lw+1]
			K, ok := k.at(words[lw].Start + 0.05)
			if !ok {
				K, ok = k.at(words[lw].Start + 0.15)
			}
			if !ok {
				K, ok = k.before(words[lw].End + 0.3)
			}
			if ok && K.B <= nw.Start+0.15 && K.B > words[lw].Start {
				r.Out, r.OutHow = K.B, "becky-cut"
			} else {
				r.Out, r.OutHow, r.OutDB = au.frameCut(math.Min(words[lw].End, nw.Start)-0.1, nw.Start+0.1, fps, false)
			}
		} else {
			r.Out = math.Min(math.Ceil(words[lw].End*fps+3)/fps, duration)
		}
		// becky-cut's times are frame-aligned but rounded to 1 ms.
		r.In, r.Out = snap(r.In, fps), snap(r.Out, fps)
		if r.Out > r.In {
			out = append(out, r)
		}
	}
	return mergeRanges(out)
}

func snap(t, fps float64) float64 { return math.Round(t*fps) / fps }

// mergeRanges joins ranges that touch or overlap after snapping.
func mergeRanges(rs []Range) []Range {
	var out []Range
	for _, r := range rs {
		if n := len(out); n > 0 && r.In <= out[n-1].Out+1e-6 {
			m := &out[n-1]
			if r.Out > m.Out {
				m.Out, m.W1, m.Last, m.OutHow, m.OutDB = r.Out, r.W1, r.Last, r.OutHow, r.OutDB
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// finalPieces predicts what BeckyCut.cs leaves: each content range minus
// becky-cut's cut spans (= intersected with its keep spans).
func finalPieces(rs []Range, keeps []span, fps float64) []span {
	var out []span
	for _, r := range rs {
		for _, k := range keeps {
			a, b := math.Max(r.In, k.A), math.Min(r.Out, k.B)
			if b-a > 0.5/fps {
				out = append(out, span{snap(a, fps), snap(b, fps)})
			}
		}
	}
	return out
}

// loudEdges lists cut points inside speech that are still louder than
// becky-cut's own threshold.
func loudEdges(rs []Range, threshold float64) []string {
	var out []string
	for _, r := range rs {
		if r.InHow != "becky-cut" && r.InHow != "file-start" && r.InDB > threshold {
			out = append(out, fmt.Sprintf("start of \"%s\" at %s (%.0f dB)", r.First, clock(r.In), r.InDB))
		}
		if r.OutHow != "becky-cut" && r.OutHow != "file-end" && r.OutDB > threshold {
			out = append(out, fmt.Sprintf("end of \"%s\" at %s (%.0f dB)", r.Last, clock(r.Out), r.OutDB))
		}
	}
	return out
}

func wordsText(words []Word, a, b int) string {
	s := ""
	for i := a; i <= b && i < len(words); i++ {
		if i > a {
			s += " "
		}
		s += words[i].Word
	}
	return s
}

// piece is one event on the finished timeline: source in/out at timeline TL.
type piece struct {
	In  float64 `json:"in"`
	Out float64 `json:"out"`
	TL  float64 `json:"timeline"`
}

// toTimeline maps a source span onto the timeline. ok=false when none of it
// survived the edit.
func toTimeline(ps []piece, s0, s1 float64) (t0, t1 float64, ok bool) {
	for _, p := range ps {
		a, b := math.Max(s0, p.In), math.Min(s1, p.Out)
		if b <= a {
			continue
		}
		if !ok {
			t0, ok = p.TL+(a-p.In), true
		}
		t1 = p.TL + (b - p.In)
	}
	return t0, t1, ok
}

// readWAV16 loads a 16-bit mono PCM WAV (what ffmpeg -ac 1 -c:a pcm_s16le writes).
func readWAV16(path string) (*audio, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%s is not a WAV file", path)
	}
	a := &audio{}
	bits, channels := 0, 0
	for p := 12; p+8 <= len(b); {
		id, n, body := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:p+8])), p+8
		switch id {
		case "fmt ":
			if body+16 > len(b) {
				return nil, fmt.Errorf("%s: short fmt chunk", path)
			}
			channels = int(binary.LittleEndian.Uint16(b[body+2:]))
			a.sr = int(binary.LittleEndian.Uint32(b[body+4:]))
			bits = int(binary.LittleEndian.Uint16(b[body+14:]))
		case "data":
			end := min(body+n, len(b))
			if n <= 0 { // a streamed WAV leaves the size unset
				end = len(b)
			}
			a.pcm = make([]int16, (end-body)/2)
			for i := range a.pcm {
				a.pcm[i] = int16(binary.LittleEndian.Uint16(b[body+2*i:]))
			}
		}
		if n <= 0 {
			break
		}
		p = body + n + n%2
	}
	if bits != 16 || channels != 1 || a.sr == 0 || a.pcm == nil {
		return nil, fmt.Errorf("%s: need 16-bit mono PCM (got %d-bit, %d channels)", path, bits, channels)
	}
	return a, nil
}

// writeWAV16 writes 16-bit mono PCM.
func writeWAV16(path string, sr int, pcm []int16) error {
	data := make([]byte, 44+2*len(pcm))
	copy(data[0:], "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+2*len(pcm)))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], uint32(sr))
	binary.LittleEndian.PutUint32(data[28:], uint32(sr*2))
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(2*len(pcm)))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(v))
	}
	return os.WriteFile(path, data, 0o644)
}
