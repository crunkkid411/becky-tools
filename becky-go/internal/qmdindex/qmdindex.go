// Package qmdindex converts a becky transcript sidecar (.srt) into the qmd
// markdown LOCATOR file the smart-search corpus (internal/qmd) reads. Per
// internal/qmd's own doc comment, the .md corpus qmd indexes is only a
// locator with coarser timestamps — the .srt stays the precise truth
// cmd/clip/qmd.go snaps every hit back to. This package is the WRITE half of
// that contract.
//
// Why this exists: nothing auto-converted a new transcript sidecar into its
// qmd locator, so the forensic search index went three weeks stale before a
// manual 58-file backfill (2026-07-22, ac74c09) fixed the immediate gap. This
// package is the automation: Convert runs once per transcript (wired into
// cmd/clip/transcribe.go right after a transcript is produced), and Sweep is
// the catch-up net for transcripts that landed some other way (an external
// capture pipeline, a manual copy, anything that predates this wiring) —
// wired into cmd/clip/forensic.go's ForensicQuery so a forensic search can
// never silently run over a stale index again.
package qmdindex

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"becky-go/internal/footage"
	"becky-go/internal/sidecar"
)

// WindowSeconds groups consecutive cues into this many seconds per md
// paragraph, matching the density of the already-indexed corpus (the
// 2026-07-22 backfill). The window size only affects search RECALL
// granularity, never precision — qmd.go's resolveCue always snaps a hit back
// to the exact .srt segment nearest the window's timecode.
const WindowSeconds = 90.0

// maxStemLen bounds a FRESH .md filename so a long yt-dlp title plus the
// "_md\" directory never risks Windows' ~260-char path limit; the hash
// suffix keeps two transcripts that truncate to the same prefix from
// colliding. Only used when no existing locator is found (see MDPath).
const maxStemLen = 150

// MDDir returns the qmd locator directory for a case folder — the single
// place this contract writes to ("<folder>/_md", the qmd "transcripts"
// collection's configured path). Never the case folder's originals.
func MDDir(folder string) string {
	return filepath.Join(folder, "_md")
}

// MDPath returns the .md path a BRAND-NEW conversion of srtPath would get
// inside mdDir: the source stem (truncated if very long) plus an 8-hex-char
// hash of the full stem, so two long, truncated names never collide. This is
// only the path for a transcript with NO existing locator — Convert/Sweep
// first check mdIndex (matched by frontmatter "source:", not by filename) and
// update an existing file IN PLACE if one is found under any name, because an
// earlier converter generation (the 2026-07-22 manual backfill) used a
// different filename scheme for the same transcripts. Skipping that check
// and always writing here would silently duplicate the entire corpus under
// two names — measured live, once: 1128 duplicates on a single Sweep before
// this lookup existed.
func MDPath(srtPath, mdDir string) string {
	base := filepath.Base(srtPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	short := stem
	if len(short) > maxStemLen {
		short = short[:maxStemLen]
	}
	return filepath.Join(mdDir, fmt.Sprintf("%s_%s.md", short, stemHash(stem)))
}

func stemHash(stem string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(stem))
	return fmt.Sprintf("%08x", h.Sum32())
}

// mdLocator is one already-converted .md's identity: where it lives and when
// it was last written, keyed (in mdIndex) by its frontmatter "source:" value
// — the transcript's exact basename, the one stable identity across however
// many different filename schemes have written into mdDir over time.
type mdLocator struct {
	Path    string
	ModTime time.Time
}

// frontmatterSourceRe pulls the "source:" line out of a .md's YAML
// frontmatter, e.g. `source: "2026-06-14_ring_parakeet_transcription.srt"`.
var frontmatterSourceRe = regexp.MustCompile(`(?m)^source:\s*"([^"]*)"`)

// buildMDIndex reads every .md in mdDir ONCE and returns a map from each
// file's recorded source transcript to its locator, so Sweep (checking
// hundreds of transcripts) never re-scans the directory per transcript — an
// O(n) directory pass instead of O(n^2). A missing/unreadable mdDir degrades
// to an empty map (everything looks new; Convert then creates fresh files,
// never a crash).
func buildMDIndex(mdDir string) map[string]mdLocator {
	idx := map[string]mdLocator{}
	entries, err := os.ReadDir(mdDir)
	if err != nil {
		return idx
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		p := filepath.Join(mdDir, e.Name())
		src, ok := readFrontmatterSource(p)
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		idx[src] = mdLocator{Path: p, ModTime: info.ModTime()}
	}
	return idx
}

// readFrontmatterSource reads just the head of path (frontmatter is always
// first) to pull its "source:" value, without parsing the whole transcript.
func readFrontmatterSource(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, 1024)
	n, _ := f.Read(buf)
	m := frontmatterSourceRe.FindSubmatch(buf[:n])
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// IsIndexed reports whether srtPath already has a qmd .md locator in mdDir,
// matched the SAME way Convert/Sweep match one (frontmatter "source:", via
// buildMDIndex) — not by checking whether MDPath(srtPath, mdDir) exists,
// since an older locator can live under a different filename (see MDPath's
// doc comment). Used by becky-review's "not yet indexed" search-result icon.
func IsIndexed(srtPath, mdDir string) bool {
	_, ok := buildMDIndex(mdDir)[filepath.Base(srtPath)]
	return ok
}

// Convert reads srtPath and writes (or, if mdDir already has a locator for
// this exact transcript under any name, UPDATES) its qmd .md locator,
// returning the path written. Read-only on srtPath. Degrade, never crash: an
// unparseable or empty transcript writes nothing and returns a plain error so
// a caller sweeping many transcripts can log-and-continue.
//
// This does its own one-time mdDir scan (buildMDIndex) — fine for converting
// a single just-finished transcript. Sweep, converting many, builds the index
// once up front and calls convertUsing directly so a whole-folder sweep stays
// O(n), not O(n^2).
func Convert(srtPath, mdDir string) (string, error) {
	return convertUsing(srtPath, mdDir, buildMDIndex(mdDir))
}

// convertUsing is Convert's body, taking a pre-built mdIndex so Sweep can
// share ONE directory scan across every transcript it converts.
func convertUsing(srtPath, mdDir string, existing map[string]mdLocator) (string, error) {
	sub, err := sidecar.ParseSubtitle(srtPath)
	if err != nil {
		return "", fmt.Errorf("qmdindex: parse %s: %w", srtPath, err)
	}
	if len(sub.Segments) == 0 {
		return "", fmt.Errorf("qmdindex: no segments in %s", srtPath)
	}

	base := filepath.Base(srtPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	duration := hmsFromSeconds(sub.Segments[len(sub.Segments)-1].End)

	var b strings.Builder
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "source: %q\n", base)
	fmt.Fprintf(&b, "video_id: %q\n", footage.VideoIDFromName(stem))
	fmt.Fprintf(&b, "date: %q\n", footage.DateFromName(stem))
	fmt.Fprintf(&b, "title: %q\n", stem)
	fmt.Fprintf(&b, "duration: %q\n", duration)
	fmt.Fprintf(&b, "collection: transcripts\n")
	fmt.Fprintf(&b, "---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", stem)
	writeWindows(&b, sub.Segments)

	out := MDPath(srtPath, mdDir)
	if loc, ok := existing[base]; ok {
		out = loc.Path // update the transcript's EXISTING locator in place, never duplicate it
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", fmt.Errorf("qmdindex: create md dir %s: %w", filepath.Dir(out), err)
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("qmdindex: write %s: %w", out, err)
	}
	return out, nil
}

// writeWindows groups segs into WindowSeconds-wide spans and appends one
// "## [start - end]" heading + "**[start]** text..." paragraph per span, the
// same shape the 2026-07-22 backfill produced.
func writeWindows(b *strings.Builder, segs []sidecar.Segment) {
	i := 0
	for i < len(segs) {
		winStart := segs[i].Start
		var texts []string
		winEnd := segs[i].End
		j := i
		for j < len(segs) && segs[j].Start < winStart+WindowSeconds {
			if t := strings.TrimSpace(segs[j].Text); t != "" {
				texts = append(texts, t)
			}
			winEnd = segs[j].End
			j++
		}
		if len(texts) > 0 {
			fmt.Fprintf(b, "## [%s - %s]\n\n", hmsFromSeconds(winStart), hmsFromSeconds(winEnd))
			fmt.Fprintf(b, "**[%s]** %s\n\n", hmsFromSeconds(winStart), strings.Join(texts, " "))
		}
		i = j
	}
}

func hmsFromSeconds(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	total := int(sec + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// Sweep walks index (a folder's videos + orphan transcripts) and (re)converts
// any transcript missing its qmd .md locator, or whose .md predates a
// re-transcribed .srt. It is the catch-up half of the auto-conversion
// contract — transcribeOne converts on the spot for transcripts THIS session
// produces; Sweep is the safety net for transcripts that landed any other way
// (an external capture pipeline, a manual copy, anything older than this
// wiring), so the index can never silently go stale again the way it did for
// three weeks before the 2026-07-22 manual backfill. One bad transcript never
// blocks the rest — its error is collected, not fatal. Builds ONE mdIndex up
// front (see buildMDIndex) so a folder of hundreds of transcripts stays O(n).
func Sweep(index footage.FolderIndex, mdDir string) (converted int, errs []error) {
	existing := buildMDIndex(mdDir)

	paths := make([]string, 0, len(index.Videos)+len(index.Orphans))
	for _, v := range index.Videos {
		if v.HasTranscript {
			paths = append(paths, v.TranscriptPath)
		}
	}
	for _, o := range index.Orphans {
		paths = append(paths, o.Path)
	}
	for _, p := range paths {
		stale, err := needsConvert(p, existing)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !stale {
			continue
		}
		if _, err := convertUsing(p, mdDir, existing); err != nil {
			errs = append(errs, err)
			continue
		}
		converted++
	}
	return converted, errs
}

// needsConvert reports whether srtPath has no .md locator yet (no entry in
// existing, keyed by frontmatter source — see buildMDIndex), or its .srt was
// modified after that locator was last written (a re-transcribe).
func needsConvert(srtPath string, existing map[string]mdLocator) (bool, error) {
	srtInfo, err := os.Stat(srtPath)
	if err != nil {
		return false, fmt.Errorf("qmdindex: stat %s: %w", srtPath, err)
	}
	loc, ok := existing[filepath.Base(srtPath)]
	if !ok {
		return true, nil
	}
	return srtInfo.ModTime().After(loc.ModTime), nil
}
