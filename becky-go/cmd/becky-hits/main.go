// becky-hits — turn a forensic agent's MINIMAL hit-list (a transcript filename +
// a timestamp, optionally a quote) into a becky Reel (internal/edl) that Becky
// Review loads straight onto its timeline. The agent stays cheap: it only emits
// {srt, t, q} per finding — becky-hits does the heavy mapping here:
//
//	becky-hits --hits hits.json --folder E:\TakingBack2007 [--out reel.json]
//	becky-hits --hits - --folder <dir>      (read the hit-list from stdin)
//	becky-hits --selftest
//
// For each hit it (1) resolves the .srt to its SOURCE VIDEO using the SAME
// forgiving index Becky Review uses (internal/footage), so the reel's clip
// sources resolve when the same folder is open, and (2) snaps the timestamp to
// the transcript CUE that contains it (internal/sidecar) to get a tight
// [in,out] window — falling back to a fixed window if no cue is found. The cue
// text becomes the clip label when the agent didn't supply a quote.
//
// JSON report to stdout, diagnostics to stderr. Pure Go, offline, deterministic;
// source media is never opened — only the .srt sidecars and small JSON are read.
// A hit whose .srt has no source video in the folder is reported as a warning and
// skipped (degrade, never crash). See SPEC-BECKY-CLIP.md (the Reel contract).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/edl"
	"becky-go/internal/footage"
	"becky-go/internal/pathx"
	"becky-go/internal/sidecar"
)

// hit is one forensic finding as the agent emits it — deliberately tiny so the
// agent's context stays cheap. Either t (a point, snapped to its cue) OR in/out
// (an explicit window) supplies the timing; q is an optional quote/label.
type hit struct {
	SRT string `json:"srt"` // transcript filename the agent is working from (basename)
	T   string `json:"t"`   // a single timestamp; snapped to the containing cue
	In  string `json:"in"`  // explicit window start (overrides t)
	Out string `json:"out"` // explicit window end (overrides t)
	Q   string `json:"q"`   // optional quote -> clip label (else the cue text)
}

// hitFile accepts either a bare JSON array of hits or an object wrapping them,
// so the agent can emit whichever is cheaper.
type hitFile struct {
	Name   string `json:"name"`
	Folder string `json:"folder"`
	Clips  []hit  `json:"clips"`
	Hits   []hit  `json:"hits"`
}

type report struct {
	Reel     string   `json:"reel"`
	Clips    int      `json:"clips"`
	Skipped  int      `json:"skipped"`
	Warnings []string `json:"warnings,omitempty"`
}

func main() {
	fs := flag.NewFlagSet("becky-hits", flag.ExitOnError)
	hitsPath := fs.String("hits", "", "path to the hit-list JSON ('-' for stdin)")
	folder := fs.String("folder", "", "case folder to resolve transcripts against (the folder Becky Review opens)")
	out := fs.String("out", "", "output reel path (default: <folder>\\becky-hits.reel.json)")
	name := fs.String("name", "", "reel name (default: derived)")
	pad := fs.Float64("pad", 0.5, "seconds of lead/tail added around a matched cue")
	window := fs.Float64("window", 4.0, "fallback half-window (seconds) when a timestamp matches no cue")
	selftest := fs.Bool("selftest", false, "run the offline self-test (creates a temp folder) and exit")
	_ = fs.Parse(os.Args[1:])

	if *selftest {
		runSelftest()
		return
	}
	if *hitsPath == "" {
		beckyio.Fatalf("--hits is required (or use --selftest)")
	}

	hits, hf, err := loadHits(*hitsPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	dir := *folder
	if dir == "" {
		dir = hf.Folder
	}
	if dir == "" {
		beckyio.Fatalf("--folder is required (the case folder Becky Review opens)")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		beckyio.Fatalf("not a folder: %s", dir)
	}

	idx, err := footage.Index(dir)
	if err != nil {
		beckyio.Fatalf("index folder: %v", err)
	}

	reelName := *name
	if reelName == "" {
		reelName = hf.Name
	}
	if reelName == "" {
		reelName = "Forensic review hits"
	}

	reel, warnings := buildReel(idx, hits, reelName, *pad, *window)

	outPath := *out
	if outPath == "" {
		outPath = filepath.Join(dir, "becky-hits.reel.json")
	}
	if err := edl.Save(outPath, reel); err != nil {
		beckyio.Fatalf("%v", err)
	}

	beckyio.PrintJSON(report{
		Reel:     outPath,
		Clips:    len(reel.Clips),
		Skipped:  len(hits) - len(reel.Clips),
		Warnings: warnings,
	})
}

// loadHits reads the hit-list from a path or stdin. It accepts a bare array or a
// {clips|hits:[...]} object and returns the hits plus the wrapper (for name/folder).
func loadHits(path string) ([]hit, hitFile, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, hitFile{}, fmt.Errorf("read hits: %w", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var arr []hit
		if e := json.Unmarshal(data, &arr); e != nil {
			return nil, hitFile{}, fmt.Errorf("parse hits array: %w", e)
		}
		return arr, hitFile{}, nil
	}
	var hf hitFile
	if e := json.Unmarshal(data, &hf); e != nil {
		return nil, hitFile{}, fmt.Errorf("parse hits object: %w", e)
	}
	clips := hf.Clips
	if len(clips) == 0 {
		clips = hf.Hits
	}
	return clips, hf, nil
}

// buildReel resolves every hit to a clip on a fresh forensic reel. It returns the
// reel and a list of plain-language warnings for hits it had to skip.
func buildReel(idx footage.FolderIndex, hits []hit, name string, pad, window float64) (edl.Reel, []string) {
	reel := edl.Reel{
		Version: "1",
		Name:    name,
		Clips:   []edl.Clip{},
		// The lower-third is ON for a forensic review reel so provenance (filename +
		// original timecode + date/link) is visible while the reviewer watches.
		Overlay: edl.Overlay{
			Enabled: true, ShowFilename: true, ShowTimecode: true,
			ShowDate: true, ShowLink: true, ShowPerson: true, ShowLocation: true,
			Position: "bottom",
		},
	}
	var warnings []string
	n := 0
	for i, h := range hits {
		srt := strings.TrimSpace(h.SRT)
		if srt == "" {
			warnings = append(warnings, fmt.Sprintf("hit %d: no 'srt' filename — skipped", i+1))
			continue
		}
		v, ok := resolveVideo(idx, srt)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("hit %d: no source video found for %q (transcript may be an orphan) — skipped", i+1, srt))
			continue
		}
		in, outv, label, werr := clipWindow(v, h, pad, window)
		if werr != "" {
			warnings = append(warnings, fmt.Sprintf("hit %d (%s): %s — skipped", i+1, srt, werr))
			continue
		}
		n++
		reel.Clips = append(reel.Clips, edl.Clip{
			ID:     fmt.Sprintf("c%d", n),
			Source: v.Path,
			In:     in,
			Out:    outv,
			Label:  label,
			Meta: edl.ClipMeta{
				Date:      v.Meta.Date,
				Link:      v.Meta.Link,
				Person:    v.Meta.Person,
				Location:  v.Meta.Location,
				SourceFPS: v.Meta.SourceFPS,
			},
		})
	}
	return reel, warnings
}

// clipWindow computes a clip's [in,out] and label for one hit. Explicit in/out
// win; otherwise the timestamp is snapped to its transcript cue (with pad), or a
// fixed window around it when no cue contains it. Returns a non-empty error
// string when the hit has no usable timing.
func clipWindow(v footage.Video, h hit, pad, window float64) (float64, float64, string, string) {
	label := strings.TrimSpace(h.Q)

	// Explicit window: trust the agent's [in,out].
	if strings.TrimSpace(h.In) != "" && strings.TrimSpace(h.Out) != "" {
		in, ok1 := parseTime(h.In)
		out, ok2 := parseTime(h.Out)
		if !ok1 || !ok2 {
			return 0, 0, "", "could not parse in/out timestamps"
		}
		if out < in {
			in, out = out, in
		}
		return clampNonNeg(in), clampNonNeg(out), label, ""
	}

	t, ok := parseTime(h.T)
	if !ok {
		return 0, 0, "", "no usable timestamp (need 't' or 'in'+'out')"
	}

	// Snap to the containing cue for a tight, accurate window.
	if v.HasTranscript {
		if sub, err := sidecar.ParseSubtitle(v.TranscriptPath); err == nil {
			for _, seg := range sub.Segments {
				if t >= seg.Start && t <= seg.End {
					if label == "" {
						label = strings.TrimSpace(seg.Text)
					}
					return clampNonNeg(seg.Start - pad), clampNonNeg(seg.End + pad), label, ""
				}
			}
		}
	}
	// No cue contained the timestamp: a fixed window around it.
	return clampNonNeg(t - window), clampNonNeg(t + window), label, ""
}

// resolveVideo finds the source video for a transcript filename, using the index
// Becky Review built. Exact transcript-base match first, then the video name,
// then a forgiving same-stem fallback that pairs only when EXACTLY one video
// matches (so it never guesses between candidates).
func resolveVideo(idx footage.FolderIndex, srt string) (footage.Video, bool) {
	want := strings.ToLower(strings.TrimSpace(filepath.Base(srt)))

	// 1) the .srt is the discovered transcript of some video.
	for _, v := range idx.Videos {
		if v.TranscriptPath != "" && strings.ToLower(pathx.Base(v.TranscriptPath)) == want {
			return v, true
		}
	}
	// 2) the agent passed the VIDEO basename by mistake — still resolve it.
	for _, v := range idx.Videos {
		if strings.ToLower(v.Name) == want {
			return v, true
		}
	}
	// 3) forgiving same-stem fallback, but only when unambiguous.
	wantStem := subStem(want)
	var match footage.Video
	count := 0
	for _, v := range idx.Videos {
		if v.TranscriptPath != "" && subStem(strings.ToLower(pathx.Base(v.TranscriptPath))) == wantStem {
			match = v
			count++
		}
	}
	if count == 1 {
		return match, true
	}
	return footage.Video{}, false
}

// subStem lowercases a subtitle filename and strips the subtitle extension plus a
// trailing language tag (".en", ".en-US"), so "foo.en.srt" and "foo.srt" share a
// stem. It is intentionally simple — only used as the last-resort matcher.
func subStem(name string) string {
	s := strings.ToLower(name)
	for _, ext := range []string{".srt", ".vtt", ".json3", ".json"} {
		if strings.HasSuffix(s, ext) {
			s = strings.TrimSuffix(s, ext)
			break
		}
	}
	if i := strings.LastIndex(s, "."); i >= 0 {
		tag := s[i+1:]
		if len(tag) >= 2 && len(tag) <= 6 { // a language tag like "en" / "en-us"
			s = s[:i]
		}
	}
	return s
}

// parseTime parses a timestamp into seconds. Accepts "HH:MM:SS", "MM:SS",
// "SS(.mmm)", SRT-style "HH:MM:SS,mmm", or a plain number of seconds.
func parseTime(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.Replace(s, ",", ".", 1) // SRT millis use a comma
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		f, err := strconv.ParseFloat(parts[0], 64)
		return f, err == nil
	case 2:
		m, e1 := strconv.ParseFloat(parts[0], 64)
		sec, e2 := strconv.ParseFloat(parts[1], 64)
		if e1 != nil || e2 != nil {
			return 0, false
		}
		return m*60 + sec, true
	case 3:
		h, e1 := strconv.ParseFloat(parts[0], 64)
		m, e2 := strconv.ParseFloat(parts[1], 64)
		sec, e3 := strconv.ParseFloat(parts[2], 64)
		if e1 != nil || e2 != nil || e3 != nil {
			return 0, false
		}
		return h*3600 + m*60 + sec, true
	default:
		return 0, false
	}
}

func clampNonNeg(f float64) float64 {
	if f < 0 {
		return 0
	}
	return f
}
