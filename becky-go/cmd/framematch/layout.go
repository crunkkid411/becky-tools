// layout.go — the human-facing exhibit. Two outputs per run:
//  1. A labeled side-by-side comparison PNG per pair (ffmpeg: scale both frames
//     to a common height, add a header band, hstack, drawtext the source file,
//     timestamp, distance, and the one-line "what to look for"). Label text is
//     passed via `textfile=` so arbitrary punctuation never breaks the
//     filtergraph.
//  2. A single self-contained HTML exhibit listing every pair with both frames,
//     their source/timestamp/hash, the "what to look for" line, and the honest
//     edit log — so a detective instantly SEES the match and is TOLD where it
//     came from. One clear comparison per pair. It never concludes "same place".
package main

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/config"
)

// comparisonHeight is the common height (px) both frames are scaled to before
// stacking, so a portrait + a landscape clip still line up cleanly.
const comparisonHeight = 540

// buildComparison renders the labeled side-by-side PNG for one pair into outDir
// and returns its path. The frame images used are the (possibly enhanced) copies
// passed in aImg/bImg; the originals are untouched. srcNameA/srcNameB are the
// ORIGINAL source file names shown in the header band (so the label TELLS the
// reviewer where each frame came from, not the internal copy name).
func buildComparison(cfg config.Config, outDir string, p Pair, aImg, bImg, srcNameA, srcNameB string, verbose bool) (string, error) {
	out := filepath.Join(outDir, fmt.Sprintf("pair_%02d.png", p.Rank))

	// Write the two header labels to textfiles (robust against punctuation).
	labelA := fmt.Sprintf("A  %s  @ %s", srcNameA, p.A.TimeLabel)
	labelB := fmt.Sprintf("B  %s  @ %s", srcNameB, p.B.TimeLabel)
	footer := fmt.Sprintf("%s  (conf %.2f)  -  %s",
		p.RoomCallText, p.Confidence, p.WhatToLookFor)

	tfA := filepath.Join(outDir, fmt.Sprintf(".lblA_%02d.txt", p.Rank))
	tfB := filepath.Join(outDir, fmt.Sprintf(".lblB_%02d.txt", p.Rank))
	tfF := filepath.Join(outDir, fmt.Sprintf(".lblF_%02d.txt", p.Rank))
	for path, text := range map[string]string{tfA: labelA, tfB: labelB, tfF: footer} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return "", fmt.Errorf("write label file: %w", err)
		}
	}
	defer func() { _ = os.Remove(tfA); _ = os.Remove(tfB); _ = os.Remove(tfF) }()

	font := ffEscapePath(fontFile())
	// Per-frame: scale to a common height, pad a 70px header band on top, draw
	// the source/time label into the band. Then hstack the two, and draw the
	// footer ("what to look for") across the bottom.
	band := 70
	chain := fmt.Sprintf(
		"[0:v]scale=-2:%d,pad=iw:ih+%d:0:%d:color=black,"+
			"drawtext=fontfile='%s':textfile='%s':x=10:y=20:fontsize=26:fontcolor=white[a];"+
			"[1:v]scale=-2:%d,pad=iw:ih+%d:0:%d:color=black,"+
			"drawtext=fontfile='%s':textfile='%s':x=10:y=20:fontsize=26:fontcolor=white[b];"+
			"[a][b]hstack=inputs=2,pad=iw:ih+50:0:0:color=black,"+
			"drawtext=fontfile='%s':textfile='%s':x=10:y=h-38:fontsize=22:fontcolor=yellow",
		comparisonHeight, band, band, font, ffEscapePath(tfA),
		comparisonHeight, band, band, font, ffEscapePath(tfB),
		font, ffEscapePath(tfF),
	)

	cmd := exec.Command(cfg.FFmpeg, "-y",
		"-i", aImg, "-i", bImg,
		"-filter_complex", chain,
		"-frames:v", "1",
		"-loglevel", "error", out)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg hstack/drawtext pair %d: %v: %s", p.Rank, err, tailStr(errBuf.String()))
	}
	logf(verbose, "  built comparison image %s", out)
	return filepath.ToSlash(out), nil
}

// fontFile returns a TTF for drawtext. Arial is present on every Windows box;
// Consolas is the fallback. An empty string lets ffmpeg use its default.
func fontFile() string {
	for _, f := range []string{`C:\Windows\Fonts\arial.ttf`, `C:\Windows\Fonts\consola.ttf`} {
		if _, err := os.Stat(f); err == nil {
			return f
		}
	}
	return ""
}

// ffEscapePath makes a Windows path safe inside an ffmpeg filtergraph option
// (backslashes and the drive colon must be escaped).
func ffEscapePath(p string) string {
	p = strings.ReplaceAll(p, `\`, `/`)
	p = strings.ReplaceAll(p, `:`, `\:`)
	return p
}

// writeExhibitHTML writes the self-contained HTML exhibit and returns its path.
// It references the comparison PNGs (and the individual frames) by RELATIVE path
// so the whole output dir can be zipped and opened anywhere.
func writeExhibitHTML(outDir, htmlPath string, m Manifest) error {
	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	b.WriteString("<title>Frame-match exhibit</title>")
	b.WriteString(exhibitCSS)
	b.WriteString("</head><body>")

	b.WriteString("<header class=\"hdr\"><h1>Frame-match comparison exhibit</h1>")
	fmt.Fprintf(&b, "<p class=\"sub\">%s &middot; generated %s</p>",
		html.EscapeString(m.Tool), html.EscapeString(m.GeneratedAt))
	b.WriteString("<p class=\"warn\">" + html.EscapeString(ManifestNote) + "</p>")
	b.WriteString("</header>")

	// Source provenance block — TELL the reviewer where everything came from.
	b.WriteString("<section class=\"sources\">")
	writeSourceCard(&b, "Source A", m.SourceA)
	writeSourceCard(&b, "Source B", m.SourceB)
	kpStr := "off"
	if m.KeypointsOn {
		kpStr = fmt.Sprintf("on (min-inliers=%d)", m.MinInliers)
	}
	fmt.Fprintf(&b, "<div class=\"params\">interval=%gs &middot; fps=%g &middot; "+
		"roi=%s [%s] &middot; roi-threshold=%d bits &middot; keypoints=%s &middot; "+
		"whole-frame threshold=%d bits &middot; pairs=%d</div>",
		m.Interval, m.FPS, html.EscapeString(m.ROIMode), html.EscapeString(m.ROISpec),
		m.ROIThreshold, html.EscapeString(kpStr), m.Threshold, m.PairCount)
	b.WriteString("</section>")

	if len(m.Pairs) == 0 {
		b.WriteString("<section class=\"pair\"><p class=\"none\">No candidate pairs within the " +
			"threshold. Raise <code>--threshold</code> to surface looser matches, or shorten " +
			"<code>--interval</code> to sample more frames, then re-run.</p></section>")
	}

	for _, p := range m.Pairs {
		writePairCard(&b, outDir, p)
	}

	b.WriteString("<footer class=\"foot\">" + html.EscapeString(ManifestNote) +
		" Confirm each candidate by eye and corroborate with realtor / listing / witness records.</footer>")
	b.WriteString("</body></html>")

	if err := os.WriteFile(htmlPath, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write exhibit html: %w", err)
	}
	return nil
}

// writeSourceCard renders one source's provenance card.
func writeSourceCard(b *strings.Builder, title string, s SourceInfo) {
	fmt.Fprintf(b, "<div class=\"src\"><h2>%s</h2>", html.EscapeString(title))
	fmt.Fprintf(b, "<div class=\"k\">file</div><div class=\"v\">%s</div>", html.EscapeString(s.Path))
	fmt.Fprintf(b, "<div class=\"k\">kind</div><div class=\"v\">%s</div>", html.EscapeString(s.Kind))
	if s.SHA256 != "" {
		fmt.Fprintf(b, "<div class=\"k\">sha256</div><div class=\"v mono\">%s</div>", html.EscapeString(s.SHA256))
	}
	if s.Resolution != "" {
		fmt.Fprintf(b, "<div class=\"k\">resolution</div><div class=\"v\">%s</div>", html.EscapeString(s.Resolution))
	}
	if s.Duration > 0 {
		fmt.Fprintf(b, "<div class=\"k\">duration</div><div class=\"v\">%.1fs</div>", s.Duration)
	}
	fmt.Fprintf(b, "<div class=\"k\">frames</div><div class=\"v\">%d sampled</div>", s.FrameCount)
	b.WriteString("</div>")
}

// writePairCard renders one candidate pair: header, the side-by-side image (with
// a CSS fallback to the two raw frames), the "what to look for" line, and the
// honest edit log if any enhancement was applied.
func writePairCard(b *strings.Builder, outDir string, p Pair) {
	b.WriteString("<section class=\"pair\">")
	roiHamStr := "n/a"
	if p.ROIHamming >= 0 {
		roiHamStr = fmt.Sprintf("%d", p.ROIHamming)
	}
	fmt.Fprintf(b, "<div class=\"pairhead\"><span class=\"rank\">#%d</span>"+
		"<span class=\"call call-%s\">%s</span>"+
		"<span class=\"dist\">ROI hamming %s &middot; confidence %.2f &middot; whole-frame %d</span></div>",
		p.Rank, html.EscapeString(roomCallClass(p.RoomCall)),
		html.EscapeString(p.RoomCallText), html.EscapeString(roiHamStr), p.Confidence, p.Hamming)

	if p.Comparison != "" {
		rel := relTo(outDir, p.Comparison)
		fmt.Fprintf(b, "<img class=\"cmp\" src=\"%s\" alt=\"side-by-side comparison %d\">", html.EscapeString(rel), p.Rank)
	} else {
		// Fallback: show the two frames side by side directly.
		b.WriteString("<div class=\"frames\">")
		fmt.Fprintf(b, "<figure><img src=\"%s\" alt=\"A frame\"><figcaption>A &middot; %s &middot; %s</figcaption></figure>",
			html.EscapeString(relTo(outDir, frameImg(p.A, p, "A"))),
			html.EscapeString(baseName(p.A.Path, "source A")), html.EscapeString(p.A.TimeLabel))
		fmt.Fprintf(b, "<figure><img src=\"%s\" alt=\"B frame\"><figcaption>B &middot; %s &middot; %s</figcaption></figure>",
			html.EscapeString(relTo(outDir, frameImg(p.B, p, "B"))),
			html.EscapeString(baseName(p.B.Path, "source B")), html.EscapeString(p.B.TimeLabel))
		b.WriteString("</div>")
	}

	// Per-frame provenance under the image.
	fmt.Fprintf(b, "<div class=\"prov\"><div><b>A</b> %s &middot; %s &middot; phash <span class=\"mono\">%s</span></div>"+
		"<div><b>B</b> %s &middot; %s &middot; phash <span class=\"mono\">%s</span></div></div>",
		html.EscapeString(p.A.Path), html.EscapeString(p.A.TimeLabel), html.EscapeString(p.A.Hash),
		html.EscapeString(p.B.Path), html.EscapeString(p.B.TimeLabel), html.EscapeString(p.B.Hash))

	fmt.Fprintf(b, "<p class=\"look\"><b>What to look for:</b> %s</p>", html.EscapeString(p.WhatToLookFor))

	if len(p.Enhancements) > 0 {
		b.WriteString("<div class=\"edits\"><b>Honest edit log (applied to COPIES — sources untouched):</b><ul>")
		for _, e := range p.Enhancements {
			fmt.Fprintf(b, "<li>Frame %s: <code>%s</code> &mdash; %s</li>",
				html.EscapeString(e.Frame), html.EscapeString(e.Filter), html.EscapeString(e.Note))
		}
		b.WriteString("</ul></div>")
	}
	b.WriteString("</section>")
}

// frameImg picks the enhanced copy for a fallback figure when one exists,
// otherwise the raw frame path.
func frameImg(f Frame, p Pair, label string) string {
	for _, e := range p.Enhancements {
		if e.Frame == label {
			return e.OutputPath
		}
	}
	return f.Path
}

// relTo returns target relative to base (slash form), falling back to the slash
// path if a relative path can't be computed. Keeps the exhibit portable.
func relTo(base, target string) string {
	r, err := filepath.Rel(base, filepath.FromSlash(target))
	if err != nil {
		return filepath.ToSlash(target)
	}
	return filepath.ToSlash(r)
}

// baseName returns the file name of a path, or fallback if empty.
func baseName(p, fallback string) string {
	if p == "" {
		return fallback
	}
	return filepath.Base(filepath.FromSlash(p))
}

// exhibitCSS is the inline stylesheet for a clean, court-exhibit-grade page.
const exhibitCSS = `<style>
:root{--ink:#15181d;--muted:#5b6470;--line:#d8dde3;--warn:#8a5a00;--accent:#1d4ed8}
*{box-sizing:border-box}
body{margin:0;font:16px/1.5 -apple-system,Segoe UI,Roboto,Arial,sans-serif;color:var(--ink);background:#f4f6f8}
.hdr{padding:28px 32px;background:#fff;border-bottom:3px solid var(--accent)}
.hdr h1{margin:0 0 4px;font-size:24px}
.sub{margin:0;color:var(--muted);font-size:13px}
.warn{margin:12px 0 0;padding:10px 14px;background:#fff7e6;border-left:4px solid var(--warn);color:var(--warn);font-size:13px}
.sources{display:flex;gap:16px;flex-wrap:wrap;padding:20px 32px}
.src{background:#fff;border:1px solid var(--line);border-radius:10px;padding:16px 18px;min-width:320px;flex:1;
     display:grid;grid-template-columns:auto 1fr;gap:4px 14px;align-items:baseline}
.src h2{grid-column:1/3;margin:0 0 6px;font-size:15px;color:var(--accent)}
.src .k{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.04em}
.src .v{font-size:13px;word-break:break-all}
.params{flex-basis:100%;color:var(--muted);font-size:13px;padding-top:4px}
.mono{font-family:Consolas,Menlo,monospace;font-size:12px}
.pair{margin:18px 32px;background:#fff;border:1px solid var(--line);border-radius:12px;padding:18px 20px;
      box-shadow:0 1px 3px rgba(0,0,0,.05)}
.pairhead{display:flex;align-items:center;gap:14px;margin-bottom:12px}
.rank{font-size:22px;font-weight:700;color:var(--accent)}
.dist{color:var(--muted);font-size:14px}
.call{font-weight:700;font-size:13px;padding:4px 10px;border-radius:6px;letter-spacing:.02em}
.call-same_room{background:#0a3d1f;color:#39ff14;border:2px solid #39ff14}
.call-different_room{background:#3d0a0a;color:#ff5a5a;border:2px solid #ff5a5a}
.call-candidate{background:#3d320a;color:#ffd233;border:2px solid #ffd233}
.call-unknown{background:#22262b;color:#b6c2cf;border:2px solid #6b7785}
.cmp{width:100%;height:auto;border-radius:8px;border:1px solid var(--line);display:block;background:#000}
.frames{display:flex;gap:12px}.frames figure{margin:0;flex:1}
.frames img{width:100%;border-radius:8px;border:1px solid var(--line)}
.frames figcaption{font-size:12px;color:var(--muted);margin-top:4px}
.prov{display:flex;gap:24px;flex-wrap:wrap;margin-top:10px;font-size:12px;color:var(--muted);word-break:break-all}
.look{margin:12px 0 0;padding:12px 14px;background:#eef4ff;border-left:4px solid var(--accent);border-radius:4px;font-size:14px}
.edits{margin-top:12px;padding:10px 14px;background:#f0fff4;border-left:4px solid #16794c;border-radius:4px;font-size:13px}
.edits ul{margin:6px 0 0;padding-left:20px}
.edits code{background:#e5f3ea;padding:1px 5px;border-radius:4px}
.none{color:var(--muted);font-size:15px;margin:0}
.foot{margin:24px 32px;padding:16px;color:var(--muted);font-size:12px;border-top:1px solid var(--line)}
</style>`
