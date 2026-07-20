package edl

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
)

// Importing an already-cut timeline back INTO becky was originally a declared
// non-goal (SPEC-BECKY-OTIO.md "Non-goals"): export was one-way. That is
// reversed here on Jordan's request — he edits in Vegas Pro and needs those cuts
// inside becky (native review, captions, re-render) without recutting by hand.
//
// Two formats, because Vegas exports both and they carry the same edit:
//
//   - Vegas "EDL TXT" (.txt): semicolon-delimited, one row per event, times in
//     MILLISECONDS. Carries an absolute FileName per event — the more robust of
//     the two.
//   - Final Cut Pro 7 XML (.xml, <xmeml>): times in source FRAMES at the
//     sequence rate, media paths as file:// URLs behind a file-id table.
//
// Both collapse to the same thing: an ordered list of [in,out] source spans =
// a becky Reel. Reels are single-track and gapless (SPEC-BECKY-OTIO non-goals),
// which is exactly the shape of a cuts-only edit like this one.

// ImportResult is a parsed edit plus the honesty fields a caller needs to warn
// on. Unresolved lists any clip source that could not be found on disk — the
// reel is still returned so the caller can decide, rather than failing whole.
type ImportResult struct {
	Reel       Reel
	FPS        float64
	Format     string
	Unresolved []string
}

// ImportTimeline reads a Vegas EDL TXT or FCP7 XML edit and returns it as a
// becky Reel. Format is chosen by extension.
func ImportTimeline(path string) (ImportResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return ImportResult{}, err
	}
	defer f.Close()

	var clips []Clip
	var fps float64
	var format string
	// The .txt states times in milliseconds (a lossy view of a frame index), so
	// it needs snapping back onto the frame grid. The .xml already gives exact
	// integer frames - snapping it would be a no-op at best and could only add
	// error, so it is left alone.
	var snapToFrames bool

	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt":
		format = "vegas-edl-txt"
		snapToFrames = true
		clips, fps, err = parseVegasTXT(f)
	case ".xml":
		format = "fcp7-xml"
		clips, fps, err = parseFCP7XML(f)
	default:
		return ImportResult{}, fmt.Errorf("unsupported edit format %q (want .txt Vegas EDL or .xml FCP7)", filepath.Ext(path))
	}
	if err != nil {
		return ImportResult{}, err
	}
	if len(clips) == 0 {
		return ImportResult{}, fmt.Errorf("no video events found in %s", filepath.Base(path))
	}

	// Resolve media that moved: a Vegas export often names the folder the media
	// sat in when the project was built, not where it lives now.
	var unresolved []string
	seenMissing := map[string]bool{}
	for i := range clips {
		src := clips[i].Source
		if _, e := os.Stat(src); e == nil {
			continue
		}
		if alt := findBeside(path, filepath.Base(src)); alt != "" {
			clips[i].Source = alt
			continue
		}
		if !seenMissing[src] {
			seenMissing[src] = true
			unresolved = append(unresolved, src)
		}
	}

	// The frame rate is NOT optional. Jordan edits frame by frame in Vegas and a
	// single frame at 29.97 is 33ms - enough to clip a consonant off the front of
	// a word. A reel with no rate makes every consumer fall back to a default 30,
	// which mis-seeks EVERY cut on 29.97 media. Vegas EDL TXT carries no rate, so
	// the media itself is probed; only if that fails is the reel left rateless.
	if fps <= 0 {
		fps = probeFPS(clips)
	}
	// One grid for everyone: the edit's stated rate and the media's container tag
	// must resolve to the SAME rational or the two drift apart across the file.
	fps = normalizeRate(fps)

	// Vegas edited on FRAMES. The .txt expresses those frames as milliseconds,
	// which is a lossy rendering of a frame index - snapping back to the nearest
	// whole frame at the true rate RECOVERS the exact frame Jordan cut on, making
	// the .txt as accurate as the .xml instead of ~0.3ms off.
	if fps > 0 && snapToFrames {
		for i := range clips {
			clips[i].In = snapFrame(clips[i].In, fps)
			clips[i].Out = snapFrame(clips[i].Out, fps)
		}
	}

	for i := range clips {
		clips[i].ID = fmt.Sprintf("c%d", i+1)
		if fps > 0 {
			clips[i].Meta.SourceFPS = fps
		}
	}

	return ImportResult{
		Reel: Reel{
			Version: "1",
			Name:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
			Clips:   clips,
			Overlay: Overlay{Position: "bottom"},
		},
		FPS:        fps,
		Format:     format,
		Unresolved: unresolved,
	}, nil
}

// normalizeRate pulls a frame rate onto the exact rational it is really meant to
// be. Containers routinely store a ROUNDED tag - Jordan's source is tagged
// 2997/100 (29.970000) while the edit that cut it uses true NTSC 30000/1001
// (29.970030). Those are two different frame grids: they agree at the start and
// drift ~0.3ms apart by the end of a 5-minute file, which lands cut points
// between frames and clips consonants. Snapping the RATE makes every consumer
// share one grid.
func normalizeRate(fps float64) float64 {
	if fps <= 0 {
		return 0
	}
	for _, exact := range []float64{
		24000.0 / 1001.0, // 23.976
		30000.0 / 1001.0, // 29.97
		60000.0 / 1001.0, // 59.94
		120000.0 / 1001.0,
		24, 25, 30, 50, 60, 120,
	} {
		if math.Abs(fps-exact) < 0.01 {
			return exact
		}
	}
	return fps
}

// snapFrame puts a time exactly on the frame grid at fps. Vegas cut on whole
// frames; anything between them is rounding noise from the export format, not
// an edit decision.
func snapFrame(sec, fps float64) float64 {
	if fps <= 0 {
		return sec
	}
	return math.Round(sec*fps) / fps
}

// probeFPS asks the media what its real frame rate is, for edit formats that do
// not state one. Returns 0 when the media cannot be read, so the caller can say
// so rather than invent a rate.
func probeFPS(clips []Clip) float64 {
	cfg := config.Load()
	seen := map[string]bool{}
	for _, c := range clips {
		if c.Source == "" || seen[c.Source] {
			continue
		}
		seen[c.Source] = true
		if info, err := mediainfo.Probe(cfg.FFprobe, c.Source); err == nil && info.FPS > 0 {
			return info.FPS
		}
	}
	return 0
}

// findBeside looks for name in the edit file's own folder, then in a sibling
// folder of it. Returns "" when not found.
func findBeside(editPath, name string) string {
	dir := filepath.Dir(editPath)
	if p := filepath.Join(dir, name); fileExists(p) {
		return p
	}
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if p := filepath.Join(filepath.Dir(dir), e.Name(), name); fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// --- Vegas EDL TXT ---------------------------------------------------------

// parseVegasTXT reads Vegas Pro's "EDL TXT" export. Columns are looked up by
// HEADER NAME, not by position, so a different Vegas version that adds or
// reorders columns still imports correctly.
//
// Times are milliseconds. StartTime/Length are the TIMELINE event; StreamStart
// is the offset into the source media. Source out is StreamStart+Length —
// NOT StreamStart+StreamLength, which is the remaining media length and is
// often the whole file.
func parseVegasTXT(r io.Reader) ([]Clip, float64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return nil, 0, fmt.Errorf("vegas EDL is empty")
	}

	col := map[string]int{}
	for i, name := range splitVegasRow(lines[0]) {
		col[strings.ToLower(name)] = i
	}
	for _, need := range []string{"starttime", "length", "streamstart", "filename", "mediatype"} {
		if _, ok := col[need]; !ok {
			return nil, 0, fmt.Errorf("vegas EDL missing %q column (header: %s)", need, lines[0])
		}
	}

	type event struct {
		start float64
		clip  Clip
	}
	var video, audio []event

	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := splitVegasRow(line)
		if len(f) <= col["filename"] {
			continue
		}
		startMS, e1 := strconv.ParseFloat(f[col["starttime"]], 64)
		lenMS, e2 := strconv.ParseFloat(f[col["length"]], 64)
		srcMS, e3 := strconv.ParseFloat(f[col["streamstart"]], 64)
		if e1 != nil || e2 != nil || e3 != nil || lenMS <= 0 {
			continue
		}
		src := f[col["filename"]]
		if src == "" {
			continue
		}
		ev := event{
			start: startMS,
			clip: Clip{
				Source: src,
				In:     srcMS / 1000.0,
				Out:    (srcMS + lenMS) / 1000.0,
			},
		}
		switch strings.ToUpper(f[col["mediatype"]]) {
		case "VIDEO":
			video = append(video, ev)
		case "AUDIO":
			audio = append(audio, ev)
		}
	}

	// Video and audio rows describe the same cuts; take video, fall back to
	// audio for an audio-only edit.
	events := video
	if len(events) == 0 {
		events = audio
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].start < events[j].start })

	clips := make([]Clip, 0, len(events))
	for _, ev := range events {
		clips = append(clips, ev.clip)
	}
	// Vegas EDL TXT carries no frame rate; the caller probes the media instead.
	return clips, 0, nil
}

// splitVegasRow splits a semicolon-delimited Vegas row, respecting the double
// quotes around file paths (a path may legally contain a semicolon) and
// trimming the surrounding whitespace Vegas pads fields with.
func splitVegasRow(line string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, r := range line {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ';' && !inQuote:
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out
}

// --- Final Cut Pro 7 XML (<xmeml>) -----------------------------------------

type xRate struct {
	Timebase float64 `xml:"timebase"`
	NTSC     string  `xml:"ntsc"`
}

type xFile struct {
	ID      string `xml:"id,attr"`
	PathURL string `xml:"pathurl"`
}

type xClipItem struct {
	Name  string   `xml:"name"`
	Rate  xRate    `xml:"rate"`
	In    *float64 `xml:"in"`
	Out   *float64 `xml:"out"`
	Start *float64 `xml:"start"`
	File  xFile    `xml:"file"`
}

type xTrack struct {
	ClipItem []xClipItem `xml:"clipitem"`
}

type xSequence struct {
	Rate  xRate `xml:"rate"`
	Media struct {
		Video struct {
			Track []xTrack `xml:"track"`
		} `xml:"video"`
		Audio struct {
			Track []xTrack `xml:"track"`
		} `xml:"audio"`
	} `xml:"media"`
}

// parseFCP7XML reads the SEQUENCE out of an <xmeml> document.
//
// It deliberately builds clips from only the <sequence> subtree. A Vegas export
// also carries the media in a bin as a top-level <clip> whose own <clipitem>
// spans the WHOLE file — parsing every <clipitem> in the document (as
// becky-cut's auto-editor parser does, which is correct for auto-editor's
// bin-less output) would import that as a bogus full-length event.
//
// Media paths, however, ARE collected document-wide first. FCP7 declares a file
// once with its <pathurl> and every later clipitem refers to it by id alone —
// and in a Vegas export that one declaration usually lives in the bin we skip.
// Resolving only from the sequence would import zero clips.
func parseFCP7XML(r io.Reader) ([]Clip, float64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}

	paths := collectFilePaths(data)

	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil, 0, fmt.Errorf("no <sequence> found — not a timeline export")
		}
		if err != nil {
			return nil, 0, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "sequence" {
			continue
		}
		var seq xSequence
		if err := dec.DecodeElement(&seq, &se); err != nil {
			return nil, 0, fmt.Errorf("parse sequence: %w", err)
		}
		return clipsFromSequence(seq, paths)
	}
}

// collectFilePaths scans the WHOLE document for <file id="..."><pathurl>...
// declarations and returns the id -> native path table.
func collectFilePaths(data []byte) map[string]string {
	paths := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return paths
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "file" {
			continue
		}
		var f xFile
		if err := dec.DecodeElement(&f, &se); err != nil {
			continue
		}
		if f.ID != "" && f.PathURL != "" {
			paths[f.ID] = pathFromURL(f.PathURL)
		}
	}
}

func clipsFromSequence(seq xSequence, paths map[string]string) ([]Clip, float64, error) {
	fps := resolveFPS(seq.Rate)

	tracks := seq.Media.Video.Track
	if len(tracks) == 0 {
		tracks = seq.Media.Audio.Track
	}

	if paths == nil {
		paths = map[string]string{}
	}
	type event struct {
		start float64
		clip  Clip
	}
	var events []event

	for _, tr := range tracks {
		for _, ci := range tr.ClipItem {
			if ci.File.PathURL != "" && ci.File.ID != "" {
				paths[ci.File.ID] = pathFromURL(ci.File.PathURL)
			}
			if ci.In == nil || ci.Out == nil || *ci.Out <= *ci.In {
				continue
			}
			src := paths[ci.File.ID]
			if src == "" {
				src = pathFromURL(ci.File.PathURL)
			}
			if src == "" {
				continue
			}
			// A clipitem may carry its own rate; prefer it over the sequence's.
			rate := fps
			if r := resolveFPS(ci.Rate); r > 0 {
				rate = r
			}
			if rate <= 0 {
				return nil, 0, fmt.Errorf("no frame rate in sequence or clipitem")
			}
			start := 0.0
			if ci.Start != nil {
				start = *ci.Start
			}
			events = append(events, event{
				start: start,
				clip: Clip{
					Source: src,
					In:     *ci.In / rate,
					Out:    *ci.Out / rate,
					Label:  ci.Name,
				},
			})
		}
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].start < events[j].start })
	clips := make([]Clip, 0, len(events))
	for _, ev := range events {
		clips = append(clips, ev.clip)
	}
	return clips, fps, nil
}

// resolveFPS turns a <rate> into a real frame rate. ntsc TRUE means the timebase
// is the ROUNDED rate and the true rate is timebase*1000/1001 — 30 -> 29.97.
// Getting this wrong drifts every cut point, which is exactly what makes
// captions land off the cut.
func resolveFPS(r xRate) float64 {
	if r.Timebase <= 0 {
		return 0
	}
	if strings.EqualFold(strings.TrimSpace(r.NTSC), "TRUE") {
		return r.Timebase * 1000.0 / 1001.0
	}
	return r.Timebase
}

// pathFromURL converts an FCP7 pathurl ("file://localhost/X:/a/b.mp4") into a
// native path.
func pathFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	s := raw
	for _, prefix := range []string{"file://localhost/", "file:///", "file://"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	if dec, err := url.PathUnescape(s); err == nil {
		s = dec
	}
	// "X:/a/b.mp4" is already absolute on Windows; a leading slash on a drive
	// path ("/X:/a") is a URL artefact.
	if len(s) > 2 && s[0] == '/' && s[2] == ':' {
		s = s[1:]
	}
	return filepath.FromSlash(s)
}
