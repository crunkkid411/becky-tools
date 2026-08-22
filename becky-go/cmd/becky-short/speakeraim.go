// speakeraim.go — when several people are on screen, frame the one who is
// actually TALKING.
//
// Jordan, 2026-08-21, quoting a decision we had already made and never wired up:
//
//	"We also agreed to implement LR-ASD into our stack - in your own words
//	 'LR-ASD *and* becky-diarize (voice turns) *and* ArcFace (face identity) -
//	 three independent signals into one decision, which is the >=2-signal rule
//	 applied to speaker selection.' Is this implemented into this pipeline? It
//	 needs to be, just make sure that if there is no visible speaker that it
//	 doesn't break the pipeline; only relevant when there are people visibly
//	 speaking."
//
// It was NOT implemented. cmd/becky-speaking has done the whole job since it was
// built — face detection, ArcFace tracking into persistent identities, then
// LR-ASD scoring each track's lip motion against the real soundtrack — and
// nothing in the shorts pipeline had ever called it. becky-short framed on
// whoever MediaPipe Pose found most prominent, which on a two-shot is "whoever
// is closest to the lens", not "whoever is speaking".
//
// TWO SIGNALS BEFORE IT DECIDES. LR-ASD says which mouth is moving with the
// audio; ArcFace says the mouth in frame 40 belongs to the same person as the
// mouth in frame 12 (without it, per-frame lip scores cannot be attributed to
// anyone). That is the >=2 rule met. becky-diarize's voice turns would be the
// third and are NOT joined in yet — see the honest gap at the bottom of this
// file.
//
// IT IS A RUNG, NOT A GATE. This runs FIRST, above pose, and only answers when
// there are at least two visible faces and one of them clearly wins. One face,
// no faces, a tie, no LR-ASD checkout, a POV shot with nobody in it — every one
// of those falls straight through to the ladder as before. A shot with no
// visible speaker is normal in Jordan's footage (half his shots are not on the
// speaker) and must never cost the render.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/crop"
	"becky-go/internal/ground"
)

// speakerMinFaces is how many tracked faces must be on screen before "which one
// is talking" is a question worth asking. With one face there is nothing to
// choose between and pose already frames it better than a face box would.
const speakerMinFaces = 2

// speakerMinCoverage is how much of the span the winning track must actually be
// visible for before its boxes are allowed to steer the crop. Below this the
// speaker is in shot too briefly to build a camera path from, and the ladder's
// other rungs know more about the rest of the span than this one does.
const speakerMinCoverage = 0.5

// speakingReportJSON is the subset of becky-speaking's output this needs.
// Everything else (per-track notes, device, frames_used) is deliberately ignored.
type speakingReportJSON struct {
	OK             bool   `json:"ok"`
	Confidence     string `json:"confidence"` // conclusion | candidate | none
	SpeakerTrackID *int   `json:"speaker_track_id"`
	Note           string `json:"note"`
	Tracks         []struct {
		ID           int      `json:"id"`
		Start        float64  `json:"start"`
		End          float64  `json:"end"`
		Detections   int      `json:"detections"`
		SpeakingFrac *float64 `json:"speaking_frac"`
		Speaking     bool     `json:"speaking"`
		Boxes        []struct {
			T    float64    `json:"t"`
			BBox [4]float64 `json:"bbox"`
		} `json:"boxes"`
	} `json:"tracks"`
}

// speakerCache holds ONE whole-window who-is-speaking pass, sliced per span.
//
// Same shape and same reason as groundCache: a short is cut into spans at the
// footage's own shot boundaries, and Jordan's Mouse Trap prank has THIRTY of
// them. Asking per span would launch becky-speaking thirty times, and each launch
// pays a face-model load and an LR-ASD load to answer about frames the previous
// launch already decoded. Passes are cheap to add in this pipeline; doing the
// SAME work twice for the same answer is the one thing that is still waste.
type speakerCache struct {
	done             bool
	winStart, winEnd float64
	rep              speakingReportJSON
	ok               bool
}

var shortSpeaker speakerCache

// resetShortSpeaker clears the pass between shorts.
func resetShortSpeaker(winStart, winEnd float64) {
	shortSpeaker = speakerCache{winStart: winStart, winEnd: winEnd}
}

// lookup runs the whole-window pass once and returns it to every later span.
func (c *speakerCache) lookup(src string) (speakingReportJSON, bool) {
	if c.done {
		return c.rep, c.ok
	}
	c.done = true
	if c.winEnd <= c.winStart {
		return c.rep, false
	}
	bin, found := resolveSpeakingBin()
	if !found {
		return c.rep, false
	}
	rep, err := runSpeaking(bin, src, c.winStart, c.winEnd)
	if err != nil || !rep.OK {
		return c.rep, false
	}
	c.rep, c.ok = rep, true
	return c.rep, true
}

// speakerAim returns a crop path following whichever visible face is speaking.
//
// ok is false for every "not applicable" case, and that is the normal outcome on
// POV and hidden-camera footage. It never returns an error: a rung that cannot
// answer says so by falling through.
func speakerAim(cfg config.Config, src string, start, end, aspect float64, srcW, srcH int,
	cuts []float64) (rects []crop.Rect, note string, ok bool) {

	rep, got := shortSpeaker.lookup(src)
	if !got {
		return nil, "", false
	}
	// Fewer than two faces: nothing to choose between.
	if len(rep.Tracks) < speakerMinFaces {
		return nil, "", false
	}
	// becky-speaking already applies the margin rule and reports "conclusion"
	// only when the winner is clear of the runner-up. A "candidate" is a coin
	// flip and this rung does not act on coin flips.
	if rep.Confidence != "conclusion" || rep.SpeakerTrackID == nil {
		return nil, "", false
	}

	span := end - start
	if span <= 0 {
		return nil, "", false
	}
	var (
		winFrac float64
		samples []ground.Sample
		winSeen bool
	)
	for _, t := range rep.Tracks {
		if t.ID != *rep.SpeakerTrackID {
			continue
		}
		winSeen = true
		if t.SpeakingFrac != nil {
			winFrac = *t.SpeakingFrac
		}
		// ONLY the boxes inside THIS span. The pass covered the whole short;
		// a span frames on where the speaker was during that span, and a span
		// the speaker is absent from falls through to the rungs below.
		var seenFirst, seenLast float64
		for _, b := range t.Boxes {
			if b.T < start || b.T > end {
				continue
			}
			// bbox is [x1,y1,x2,y2] in FRACTIONS of the frame, facetrack's own
			// convention.
			cx := (b.BBox[0] + b.BBox[2]) / 2
			if cx <= 0 || cx >= 1 {
				continue
			}
			if len(samples) == 0 {
				seenFirst = b.T
			}
			seenLast = b.T
			samples = append(samples, ground.Sample{T: b.T, X: cx})
		}
		if cov := (seenLast - seenFirst) / span; cov < speakerMinCoverage {
			return nil, "", false
		}
	}
	if !winSeen || len(samples) < 2 {
		return nil, "", false
	}
	path := ground.PanPath(samples, start, end, panOutFPS, panSmoothSeconds, cuts)
	if len(path) == 0 {
		return nil, "", false
	}
	rects = make([]crop.Rect, 0, len(path))
	for _, p := range path {
		r := crop.StaticAt(srcW, srcH, aspect, p.X)
		r.T = p.T
		rects = append(rects, r)
	}
	return rects, fmt.Sprintf("%d people on screen; LR-ASD and the face tracker agree track %d is the one "+
		"talking (%.0f%% of its frames), so the crop follows THAT person",
		len(rep.Tracks), *rep.SpeakerTrackID, winFrac*100), true
}

func runSpeaking(bin, src string, start, end float64) (speakingReportJSON, error) {
	out, err := exec.Command(bin, "--video", src,
		"--start", strconv.FormatFloat(start, 'f', 3, 64),
		"--end", strconv.FormatFloat(end, 'f', 3, 64),
		"--boxes").Output()
	if err != nil {
		return speakingReportJSON{}, err
	}
	var rep speakingReportJSON
	if err := json.Unmarshal(out, &rep); err != nil {
		return speakingReportJSON{}, err
	}
	return rep, nil
}

// resolveSpeakingBin finds becky-speaking: BECKY_SPEAKING env -> next to the
// running exe -> PATH. Same order and same degrade-to-("",false) contract as
// resolveCutBin above it, for the same reason (a separate `main` package cannot
// be imported).
func resolveSpeakingBin() (string, bool) {
	if p := strings.TrimSpace(os.Getenv("BECKY_SPEAKING")); p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if fileExistsAt(p) {
			return p, true
		}
		return "", false
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), speakingExeName())
		if fileExistsAt(cand) {
			return cand, true
		}
	}
	if p, err := exec.LookPath("becky-speaking"); err == nil {
		return p, true
	}
	return "", false
}

func speakingExeName() string {
	if runtime.GOOS == "windows" {
		return "becky-speaking.exe"
	}
	return "becky-speaking"
}

// THE THIRD SIGNAL IS NOT JOINED IN YET.
//
// becky-diarize already produces voice turns (who is talking WHEN, from the
// audio alone, with no reference to the picture). Crossing it with this would
// close the loop Jordan described: LR-ASD says a mouth moved with the sound,
// ArcFace says whose mouth it is across time, and diarization says the VOICE
// changed speaker at the same instant. Three independent signals, one decision.
//
// It is not wired here because the join is a real piece of work, not a call:
// diarize's speaker labels are per-utterance and anonymous ("SPEAKER_00"), so
// binding them to face tracks needs an alignment pass that does not exist yet.
// Claiming two signals and shipping two is honest; claiming three would not be.
