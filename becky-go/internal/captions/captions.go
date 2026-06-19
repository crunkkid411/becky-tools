// Package captions is becky-clip's deterministic CAPTION-ACQUISITION decision
// engine — the step that runs BEFORE any local transcription (Jordan's hard
// requirement). Given a downloaded video, it answers one question: do we already
// have, or can we cheaply fetch, a TRUSTWORTHY official transcript — or must we
// fall back to local ASR?
//
// The sequence (deterministic, same input → same decision):
//  1. Extract the YouTube id from the filename's "[<11-char id>]" token. No id ⇒
//     no online step is possible (the file didn't come from yt-dlp).
//  2. Probe the VIDEO's real duration (ffprobe via internal/mediainfo).
//  3. If an OFFICIAL subtitle already sits next to the video (<stem>.en.srt or
//     <stem>.srt — NOT a becky-made <stem>_LOCAL.srt), parse it and take its last
//     cue end as the caption COVERAGE in seconds.
//  4. Else, if an id exists, fetch YouTube auto/official subs via yt-dlp (behind
//     the FetchAutoSubs seam) and GUARANTEE the result lands as <stem>.en.srt in
//     the SAME folder as the video, then parse it for coverage.
//  5. EDIT DETECTION (the load-bearing forensic check): compare coverage to the
//     video duration. Jordan edits incriminating segments out of his livestreams
//     with YouTube's "edit" feature, which SHORTENS the captions relative to the
//     full video he downloaded. If coverage ≥ duration*CoverageOK the official
//     captions are COMPLETE and trustworthy; if they fall short the stream was
//     likely edited and the official transcript must NOT be trusted.
//  6. Decide: "use_official" (a complete official transcript exists / was fetched)
//     or "local_needed" (no id, no captions available, or coverage too short).
//
// This package NEVER runs local ASR and NEVER writes the video — it only decides
// and (at most) drops the official <stem>.en.srt sidecar next to the source. It
// opts out of becky's offline invariant for exactly ONE explicit, logged network
// step (the yt-dlp fetch), mirroring becky-scout; with --offline that step is
// skipped entirely. Degrade-never-crash: a missing id, an absent transcript, a
// yt-dlp failure, or an unprobeable video all yield a clean "local_needed"
// Decision, never a panic.
package captions

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"becky-go/internal/mediainfo"
	"becky-go/internal/sidecar"
)

// CoverageOK is the fraction of the video's duration the official captions must
// span to be considered COMPLETE (and therefore trustworthy). A 3-hour stream
// whose official .srt only runs ~2 hours (coverage_ratio ≈ 0.67) is below this
// floor → treated as edited → local transcription needed. 0.90 tolerates the
// normal small tail gap (captions often stop a few seconds before the very end)
// without accepting a stream that had real minutes cut out of it.
const CoverageOK = 0.90

// idTokenRe matches the bracketed 11-char YouTube id yt-dlp embeds in the
// downloaded filename, e.g. "[46T0KmQA7Eg]". The alphabet is YouTube's URL-safe
// base64 ([A-Za-z0-9_-]); the bracket requirement keeps a bare 11-char run inside
// a longer word from matching. Only the first token is used.
var idTokenRe = regexp.MustCompile(`\[([A-Za-z0-9_-]{11})\]`)

// FetchAutoSubs is the SEAM over the single online step. It must fetch YouTube
// auto/official subtitles for video id `id` and guarantee that, on success, the
// English SRT exists at exactly `outPath` (which the caller sets to
// <video-stem>.en.srt next to the source). It returns the path actually written
// (normally == outPath) or an error. The production implementation
// (realFetchAutoSubs) shells out to yt-dlp; tests override this var with a fake
// that writes a canned .srt so the whole decision flow runs offline. Production
// never reassigns it.
var FetchAutoSubs = realFetchAutoSubs

// Action is the decision becky-captions emits: either the official transcript can
// be trusted, or local ASR is required.
type Action string

const (
	// ActionUseOfficial: a COMPLETE official transcript exists beside the video
	// (or was just fetched). No local transcription needed.
	ActionUseOfficial Action = "use_official"
	// ActionLocalNeeded: there is no trustworthy official transcript — no id, no
	// captions available (private/removed/none), or the official captions are too
	// short (the stream was likely edited). The caller must run local ASR.
	ActionLocalNeeded Action = "local_needed"
)

// Decision is the deterministic JSON result of analysing one video. Coverage and
// ratio are zero when no official transcript was found. OfficialSRT is the path
// to the official sidecar that was used/fetched, or "" when none.
type Decision struct {
	ID               string  `json:"id"`                // YouTube id from the filename, or ""
	VideoDuration    float64 `json:"video_duration"`    // probed video length, seconds (0 if unprobeable)
	OfficialSRT      string  `json:"official_srt"`      // path to the official .srt used/fetched, or ""
	OfficialCoverage float64 `json:"official_coverage"` // last cue end of the official srt, seconds
	CoverageRatio    float64 `json:"coverage_ratio"`    // official_coverage / video_duration (0 if no duration/coverage)
	Action           Action  `json:"action"`            // use_official | local_needed
	Reason           string  `json:"reason"`            // plain-language explanation of the decision
	Edited           bool    `json:"edited"`            // official captions present but too short (likely YouTube-edited)
	Fetched          bool    `json:"fetched"`           // an official srt was downloaded this run
}

// Options controls one Analyze run.
type Options struct {
	// Offline skips the yt-dlp fetch entirely: the decision is based ONLY on a
	// local official srt that is already present. Use for a check-only run.
	Offline bool
	// Logf, if non-nil, receives one human progress line per meaningful step
	// (id found, probe result, fetch attempt). Diagnostics only; never affects the
	// Decision. nil disables logging.
	Logf func(format string, args ...any)
}

// Analyze runs the deterministic caption-acquisition sequence for videoPath and
// returns the Decision. It never returns an error for an "expected" outcome
// (no id, no captions, edited, unprobeable) — those are encoded in the Decision
// (action=local_needed). It only surfaces an error for a truly broken call (an
// empty path); even then the caller can fall back to local ASR.
func Analyze(videoPath string, opt Options) (Decision, error) {
	logf := opt.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if strings.TrimSpace(videoPath) == "" {
		return Decision{Action: ActionLocalNeeded, Reason: "no video path given"}, fmt.Errorf("captions: empty video path")
	}

	d := Decision{}

	// (1) YouTube id from the filename's bracketed token.
	d.ID = extractID(filepath.Base(videoPath))
	if d.ID != "" {
		logf("captions: id=%s", d.ID)
	} else {
		logf("captions: no [id] token in filename — online fetch not possible")
	}

	// (2) Probe the real video duration. A failure (no ffprobe / unreadable) is a
	// degrade, not a crash: duration stays 0 and edit-detection can't run, so we
	// fall through to local ASR.
	d.VideoDuration = probeDuration(videoPath, logf)

	// (3) Official subtitle already on disk (NOT a _LOCAL one).
	if off := findOfficialSRT(videoPath); off != "" {
		logf("captions: official subtitle present: %s", filepath.Base(off))
		d.OfficialSRT = off
		d.OfficialCoverage = coverageOf(off)
		return decide(d, "local"), nil
	}

	// (4) No local official srt — try the single online fetch (unless --offline or
	// no id). The fetch GUARANTEES, on success, that <stem>.en.srt exists.
	if opt.Offline {
		d.Reason = "no official subtitle next to the video and --offline set — local transcription needed"
		d.Action = ActionLocalNeeded
		return d, nil
	}
	if d.ID == "" {
		d.Reason = "no official subtitle next to the video and no YouTube id in the filename — local transcription needed"
		d.Action = ActionLocalNeeded
		return d, nil
	}

	out := OfficialSRTPath(videoPath)
	logf("captions: fetching auto/official subs via yt-dlp → %s", filepath.Base(out))
	written, err := FetchAutoSubs(d.ID, out)
	if err != nil {
		// A missing/edited/private video, no captions, or a network failure all
		// land here — a valid "local_needed" outcome, not a crash.
		logf("captions: yt-dlp fetch did not yield captions: %v", err)
		d.Reason = "no official captions available online (" + firstLine(err) + ") — local transcription needed"
		d.Action = ActionLocalNeeded
		return d, nil
	}
	if strings.TrimSpace(written) == "" {
		written = out
	}
	if !fileExists(written) {
		logf("captions: yt-dlp reported success but no srt at %s", written)
		d.Reason = "yt-dlp produced no subtitle file — local transcription needed"
		d.Action = ActionLocalNeeded
		return d, nil
	}
	d.Fetched = true
	d.OfficialSRT = written
	d.OfficialCoverage = coverageOf(written)
	return decide(d, "fetched"), nil
}

// decide applies edit-detection to a Decision that already has VideoDuration +
// OfficialCoverage filled, sets Action/Reason/Edited/CoverageRatio, and returns
// it. `origin` ("local"/"fetched") only flavours the reason text.
func decide(d Decision, origin string) Decision {
	src := "the official transcript beside the video"
	if origin == "fetched" {
		src = "the official transcript fetched from YouTube"
	}

	// No usable coverage at all → the "official" file had no cues. Fall to local.
	if d.OfficialCoverage <= 0 {
		d.Action = ActionLocalNeeded
		d.Reason = src + " has no caption cues — local transcription needed"
		return d
	}

	// Can't edit-detect without a duration: accept the official captions (we have
	// real cues and no evidence they're short), but say so.
	if d.VideoDuration <= 0 {
		d.CoverageRatio = 0
		d.Action = ActionUseOfficial
		d.Reason = "using " + src + " (video duration unavailable, so completeness could not be verified)"
		return d
	}

	d.CoverageRatio = d.OfficialCoverage / d.VideoDuration
	if d.CoverageRatio >= CoverageOK {
		d.Action = ActionUseOfficial
		d.Reason = fmt.Sprintf("using %s — it covers %.0f%% of the video (complete)", src, d.CoverageRatio*100)
		return d
	}

	// Short captions on a longer video: the hallmark of a YouTube-edited stream.
	d.Edited = true
	d.Action = ActionLocalNeeded
	d.Reason = fmt.Sprintf(
		"%s covers only %.0f%% of the %s video (%s of captions) — the stream was likely edited, so local transcription is needed for the full video",
		src, d.CoverageRatio*100, hms(d.VideoDuration), hms(d.OfficialCoverage))
	return d
}

// OfficialSRTPath is the canonical official-subtitle path for a video:
// <stem>.en.srt in the SAME directory as the source (separator-safe). This is the
// path the fetch must land on so the file matches "same naming scheme, same
// folder" and the index pairs it (sidecar.FindSubtitle prefers ".en.srt"). The
// stem is derived from the LOCAL video filename — NOT from the YouTube title,
// which may have changed since download.
func OfficialSRTPath(videoPath string) string {
	dir := filepath.Dir(videoPath)
	stem := stemOf(videoPath)
	return filepath.Join(dir, stem+".en.srt")
}

// findOfficialSRT returns the path to an OFFICIAL subtitle already sitting next
// to the video — <stem>.en.srt or <stem>.srt — or "" if none. It deliberately
// EXCLUDES a becky-made <stem>_LOCAL.srt (that is our own ASR output, not an
// official transcript; treating it as official would defeat the purpose). The
// ".en.srt" form is preferred over a bare ".srt". Only the same directory is
// checked (the fetch always lands here, and yt-dlp downloads sit beside the
// video) — the forgiving cross-folder resolver is for the index, not for this
// trust decision.
func findOfficialSRT(videoPath string) string {
	dir := filepath.Dir(videoPath)
	stem := stemOf(videoPath)
	enSRT := filepath.Join(dir, stem+".en.srt")
	if fileExists(enSRT) {
		return enSRT
	}
	bareSRT := filepath.Join(dir, stem+".srt")
	if fileExists(bareSRT) {
		return bareSRT
	}
	return ""
}

// coverageOf parses a subtitle file and returns its caption COVERAGE: the end
// time (seconds) of its last cue. A parse failure or an empty transcript yields
// 0 (degrade, never crash) — which decide() reads as "no usable captions".
func coverageOf(srtPath string) float64 {
	sub, err := sidecar.ParseSubtitle(srtPath)
	if err != nil || len(sub.Segments) == 0 {
		return 0
	}
	var last float64
	for _, s := range sub.Segments {
		if s.End > last {
			last = s.End
		}
	}
	return last
}

// probeDuration returns the video's duration in seconds via ffprobe, or 0 on any
// failure (no ffprobe on PATH, unreadable file). The ffprobe binary honours the
// BECKY_FFPROBE env override, else "ffprobe" on PATH — matching the other tools.
func probeDuration(videoPath string, logf func(string, ...any)) float64 {
	ff := strings.TrimSpace(os.Getenv("BECKY_FFPROBE"))
	if ff == "" {
		ff = "ffprobe"
	}
	info, err := mediainfo.Probe(ff, videoPath)
	if err != nil {
		logf("captions: could not probe video duration (%v) — edit-detection skipped", firstLine(err))
		return 0
	}
	logf("captions: video duration %s (%.1fs)", hms(info.Duration), info.Duration)
	return info.Duration
}

// extractID returns the bracketed 11-char YouTube id from a filename, or "".
func extractID(name string) string {
	m := idTokenRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return m[1]
}

// stemOf returns a file name without its extension. Index/the GUI always feed
// host-native absolute paths, so filepath.Base/Ext are correct here.
func stemOf(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// fileExists reports whether path is an existing regular (non-directory) file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// hms formats seconds as H:MM:SS (or M:SS under an hour) for human-readable
// reasons. Negative/zero → "0:00".
func hms(sec float64) string {
	if sec <= 0 {
		return "0:00"
	}
	total := int(sec + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// firstLine returns the first line of an error message (compact reasons).
func firstLine(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
