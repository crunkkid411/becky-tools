// vad.go — Silero-VAD (via sherpa-onnx) speech-mask gate for becky-transcribe.
//
// Parakeet (like every ASR) hallucinates short phrases over silence — the
// classic "Thank you for watching" on a near-silent clip. becky already computes
// a Silero VAD speech mask elsewhere (becky-cut); here we reuse the same helper
// to drop transcript segments that fall in NO-speech regions, so a hallucinated
// phrase never enters the searchable index as real speech.
//
// The gate is overlap-based and conservative: a segment is kept if any
// meaningful fraction of its span overlaps a VAD speech region. Segments with
// ~zero speech overlap are dropped (and recorded in an audit list). The whole
// path degrades gracefully — a missing model / helper / python leaves the
// transcript untouched (vad_applied=false), so a clip with genuine speech is
// never harmed when VAD can't run.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/proc"
	"becky-go/internal/pyhelpers"
)

// minSegmentSpeechPct: a segment kept must have at least this fraction of its
// span covered by VAD speech. Low (10%) so a brief real utterance survives but a
// phrase invented over pure silence (0% overlap) is dropped.
const minSegmentSpeechPct = 10.0

// realSpeechRegionSec: the corroboration floor (F4 fix). Silero VAD is an
// INDEPENDENT detector — when it carves out a contiguous speech REGION of at least
// this duration, that region IS real speech, and ASR text riding it is a real
// vocalization, NOT a silence hallucination. A brief human "thank you" registers as
// a ~0.5-0.6s Silero region, so any segment overlapping a region this long or longer
// is kept as TRUSTWORTHY speech (never flagged low-confidence). The old gate instead
// demanded 0.7s of ABSOLUTE OVERLAP, which a genuine ~0.6s utterance can never reach,
// so it wrongly buried every brief real vocalization. 0.25s is short enough to keep
// a real brief word while still treating a sub-quarter-second transient as suspect.
const realSpeechRegionSec = 0.25

// lowConfMinOverlapSec: when a segment touches ONLY tiny Silero slivers (no region
// reaches realSpeechRegionSec), it is flagged low_confidence only if its absolute
// speech overlap is also below this floor — a sub-tenth-of-a-second blip with no real
// region behind it is exactly the transient/hallucination regime. A segment with a
// real region behind it is never flagged regardless of absolute overlap.
const lowConfMinOverlapSec = 0.1

// vadSpan is one [start,end] speech region in seconds.
type vadSpan struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// vadSegmentsJSON mirrors the subset of vad_silero.py's default-mode stdout.
type vadSegmentsJSON struct {
	Skipped  bool      `json:"skipped"`
	Reason   string    `json:"reason"`
	Duration float64   `json:"duration"`
	Segments []vadSpan `json:"segments"`
}

// gateSegmentsByVAD runs Silero VAD on the (already-extracted) 16 kHz WAV and
// filters segments by speech overlap. It returns the kept segments, the dropped
// ones (audit), and whether the gate actually ran. On any failure it returns the
// segments unchanged with applied=false so a real transcript is never harmed.
func gateSegmentsByVAD(cfg config.Config, wav string, segs []Segment, verbose bool) (kept []Segment, dropped []DroppedSegment, applied bool) {
	if cfg.SileroVADModel == "" {
		beckyio.Logf(verbose, "vad-gate: no silero model configured; skipping (transcript unchanged)")
		return segs, nil, false
	}
	if _, err := os.Stat(cfg.SileroVADModel); err != nil {
		beckyio.Logf(verbose, "vad-gate: silero model not found (%v); skipping", err)
		return segs, nil, false
	}
	script, err := pyhelpers.Materialize("vad_silero.py", pyhelpers.VADSilero)
	if err != nil {
		beckyio.Logf(verbose, "vad-gate: cannot materialize helper (%v); skipping", err)
		return segs, nil, false
	}

	spans, err := runTranscribeVAD(cfg.Python, script, cfg.SileroVADModel, wav)
	if err != nil {
		beckyio.Logf(verbose, "vad-gate: probe degraded (%v); skipping (transcript unchanged)", err)
		return segs, nil, false
	}

	for _, s := range segs {
		pct, overlapSec := segmentSpeechOverlap(s, spans)
		if pct < minSegmentSpeechPct {
			dropped = append(dropped, DroppedSegment{
				Start:     round3(s.Start),
				End:       round3(s.End),
				Text:      s.Text,
				SpeechPct: round3(pct),
				Reason:    "no VAD speech overlap — likely ASR hallucination over silence",
			})
			beckyio.Logf(verbose, "vad-gate: drop [%.2f-%.2f] %.0f%% speech: %q", s.Start, s.End, pct, s.Text)
			continue
		}
		p := round3(pct)
		s.SpeechPct = &p
		// CORROBORATION (F4): does this segment ride a REAL Silero speech region? If
		// Silero — an independent detector — carved out a contiguous speech region of
		// realSpeechRegionSec or longer that this segment overlaps, the segment is
		// genuine speech (a brief "thank you" is a ~0.6s region) and is KEPT as
		// trustworthy. Only a segment with NO real region behind it AND a near-zero
		// absolute overlap (a transient blip) is flagged low_confidence.
		region := longestOverlappingRegion(s, spans)
		if region < realSpeechRegionSec && overlapSec < lowConfMinOverlapSec {
			s.LowConfidence = true
			beckyio.Logf(verbose, "vad-gate: flag low-confidence [%.2f-%.2f] region=%.2fs overlap=%.2fs: %q",
				s.Start, s.End, region, overlapSec, s.Text)
		} else {
			beckyio.Logf(verbose, "vad-gate: keep real speech [%.2f-%.2f] region=%.2fs overlap=%.2fs: %q",
				s.Start, s.End, region, overlapSec, s.Text)
		}
		kept = append(kept, s)
	}
	if kept == nil {
		kept = []Segment{}
	}
	return kept, dropped, true
}

// segmentSpeechOverlap returns the percentage of a segment's duration that
// overlaps any VAD speech span AND the absolute overlap in seconds. A zero-length
// segment (start==end, e.g. an untimed fallback word) is treated as speech
// (100%, 0s) so it is never dropped on a timing technicality.
func segmentSpeechOverlap(s Segment, spans []vadSpan) (pct, seconds float64) {
	dur := s.End - s.Start
	if dur <= 0 {
		return 100.0, 0.0
	}
	for _, sp := range spans {
		lo := s.Start
		if sp.Start > lo {
			lo = sp.Start
		}
		hi := s.End
		if sp.End < hi {
			hi = sp.End
		}
		if hi > lo {
			seconds += hi - lo
		}
	}
	return 100.0 * seconds / dur, seconds
}

// segmentSpeechPct returns only the speech-overlap percentage (see
// segmentSpeechOverlap).
func segmentSpeechPct(s Segment, spans []vadSpan) float64 {
	pct, _ := segmentSpeechOverlap(s, spans)
	return pct
}

// longestOverlappingRegion returns the duration of the LONGEST Silero speech region
// that this segment overlaps (0 if it overlaps none). This is the F4 corroboration
// signal: a long Silero region behind a segment means the underlying audio really IS
// speech (Silero, an independent detector, said so), so the ASR text is a real
// vocalization rather than a hallucination over silence — even when the segment's own
// timing only clips the edge of that region. A zero-length segment returns 0 (its
// trustworthiness is decided by overlap, not region length).
func longestOverlappingRegion(s Segment, spans []vadSpan) float64 {
	if s.End <= s.Start {
		return 0
	}
	longest := 0.0
	for _, sp := range spans {
		lo := s.Start
		if sp.Start > lo {
			lo = sp.Start
		}
		hi := s.End
		if sp.End < hi {
			hi = sp.End
		}
		if hi <= lo {
			continue // no overlap with this region
		}
		if d := sp.End - sp.Start; d > longest {
			longest = d // the FULL region length, not just the overlapped slice
		}
	}
	return longest
}

// runTranscribeVAD runs the Silero helper in default (segment) mode and returns
// the merged, sorted speech spans.
func runTranscribeVAD(python, script, model, wav string) ([]vadSpan, error) {
	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("becky_transcribe_vad_%d.json", os.Getpid()))
	defer os.Remove(outFile)
	cmd := exec.Command(python, script, wav, "--model", model, "--output", outFile)
	proc.NoWindow(cmd) // no console flash when becky-clip spawns us windowless
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, tail(errBuf.String()))
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		return nil, err
	}
	var res vadSegmentsJSON
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("unexpected VAD output: %s", tail(string(data)))
	}
	if res.Skipped {
		return nil, fmt.Errorf("vad skipped: %s", res.Reason)
	}
	spans := res.Segments
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	return spans, nil
}
