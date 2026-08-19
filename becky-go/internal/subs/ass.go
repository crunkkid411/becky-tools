package subs

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// ASSStyle is the "jordan" caption look (research/jordan-edit-reverse-engineered.md,
// the CAPTIONS section): stacked short lines with ONE coloured emphasis word
// per block, thick outline, a drop shadow standing in for the reference's
// glow.
//
// This needs a REAL .ass file, reversing style.go's deliberate choice of
// ffmpeg force_style on a plain .srt. That comment explains force_style's
// upside: it restyles a plain .srt in place, no ASS writer, no second file
// format. The reason it does not work here: force_style patches ONE style
// onto the WHOLE line — it has no way to recolour a single WORD inside it,
// because a .srt carries no per-run formatting at all to hang an override on.
// Colouring exactly one stressed word per block is this style's entire
// point, so the second file format style.go avoided is now unavoidable. The
// cli-cut/.srt path (style.go, Build/BuildFromChunks returning []Cue) is
// completely untouched by this — this is an ADDITIONAL writer, not a
// replacement.
type ASSStyle struct {
	FontName      string
	FontSize      int
	Bold          int
	Outline       int
	Shadow        int
	Alignment     int // libass numpad convention; 2 = bottom-centre, matches Style
	MarginV       int
	EmphasisColor string // ASS inline colour code, &HBBGGRR& (BLUE-GREEN-RED — reverse of HTML's RRGGBB)
}

// DefaultJordanStyle is a FIRST GUESS at the jordan look, sized off the
// actual output frame height so the relative size holds across aspects.
// Jordan has approved the SHAPE of this look (stacked lines, one coloured
// word, from the reverse-engineered reference) but not these exact numbers —
// they are meant to be looked at on a real render and adjusted, per the
// task that added this style: "it must be something he can LOOK at and then
// adjust, not a new default."
func DefaultJordanStyle(outputHeight int) ASSStyle {
	fontSize := outputHeight * 45 / 1000 // ~4.5% of frame height per line
	if fontSize < 28 {
		fontSize = 28
	}
	return ASSStyle{
		FontName:      "ProximaNova-Semibold", // same family as DefaultStyle; "heavy rounded sans" is the ask
		FontSize:      fontSize,
		Bold:          1,
		Outline:       3,
		Shadow:        1,
		Alignment:     2,
		MarginV:       90,
		EmphasisColor: "&HFFFF00&", // cyan (R=00 G=FF B=FF -> BB GG RR = FF FF 00)
	}
}

// emphasisReset is the colour every emphasised word reverts to — the style's
// own PrimaryColour (opaque white), so text after the coloured word matches
// the rest of the line exactly.
const emphasisReset = `{\c&HFFFFFF&}`

// jordanWordsPerLine is the target words-per-line for the stacked look
// (research: "roughly 2-4 words per line"). A fixed group size, not a
// text-width fit — becky has no font metrics to fit against here, and a
// constant this small barely needs one.
const jordanWordsPerLine = 3

// assWordText is one word as the jordan style displays it: upper-cased, and
// carrying cli-cut's own punctuation rule (normalize — . , ; : are split
// points, never printed; ? and ! survive) applied per WORD, since the
// stacked look needs each word's own text, not ChunkWords' already-joined
// Cue.Text.
func assWordText(w Word) string {
	return strings.ToUpper(normalize(w.Word, false))
}

// PlainStackedText is the jordan style's words as plain text — upper-cased,
// same per-word punctuation rule as the ASS writer, but with no stacking and
// no colour markup. It exists for a caller that needs to know what a jordan
// block DISPLAYS without also carrying .ass syntax: becky-short's --review
// sidecar parses plain SRT text against a fresh transcription, and the words
// shown are identical between the burned .ass and this plain form — only the
// colour differs, which review does not check.
func PlainStackedText(words []Word) string {
	parts := make([]string, 0, len(words))
	for _, w := range words {
		if t := assWordText(w); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// WriteASS writes cues in the jordan look as an .ass file: stacked lines,
// one coloured emphasis word per block, and st's outline/shadow/margin.
//
// emphasis[i] is the index into cues[i].Words to colour, or any value
// outside [0,len(Words)) to colour nothing — Part B of the jordan style:
// "if no event falls inside the block, colour nothing" is a correct outcome,
// not a failure this writer papers over. Picking WHICH word (the
// audio-driven emphasis rule) is the caller's job; this file only knows how
// to draw the result, matching this package's existing split (style.go
// draws, callers decide what to burn).
//
// width/height set PlayResX/PlayResY so MarginV and FontSize are relative to
// the ACTUAL rendered frame — captions are burned after crop+scale (see
// captions.go), so this must be the short's own output size, not the source's.
func WriteASS(w io.Writer, cues []CueWords, emphasis []int, st ASSStyle, width, height int) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "[Script Info]\r\nScriptType: v4.00+\r\nPlayResX: %d\r\nPlayResY: %d\r\nScaledBorderAndShadow: yes\r\n\r\n",
		width, height)
	fmt.Fprintf(bw, "[V4+ Styles]\r\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, "+
		"BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, "+
		"Alignment, MarginL, MarginR, MarginV, Encoding\r\n")
	// PrimaryColour &H00FFFFFF opaque white, OutlineColour/BackColour
	// &H00000000 opaque black — same AABBGGRR convention style.go's
	// ForceStyle documents. BorderStyle=1 is a true outline+shadow (3 would
	// be an opaque box, wrong for this look).
	fmt.Fprintf(bw, "Style: Jordan,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,%d,0,0,0,100,100,0,0,1,%d,%d,%d,10,10,%d,1\r\n\r\n",
		st.FontName, st.FontSize, st.Bold, st.Outline, st.Shadow, st.Alignment, st.MarginV)
	fmt.Fprintf(bw, "[Events]\r\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\r\n")

	for i, cw := range cues {
		idx := -1
		if i < len(emphasis) {
			idx = emphasis[i]
		}
		text := jordanLines(cw.Words, idx, st.EmphasisColor)
		if text == "" {
			continue
		}
		fmt.Fprintf(bw, "Dialogue: 0,%s,%s,Jordan,,0,0,0,,%s\r\n", ASSTime(cw.Start), ASSTime(cw.End), text)
	}
	return bw.Flush()
}

// jordanLines lays a cue's words out as jordanWordsPerLine-word lines joined
// by ASS's \N break, with emphasisIdx (an index into words, or any
// out-of-range value for "nothing") wrapped in emphasisColor and reset back
// to white right after.
func jordanLines(words []Word, emphasisIdx int, emphasisColor string) string {
	var lines []string
	for i := 0; i < len(words); i += jordanWordsPerLine {
		end := i + jordanWordsPerLine
		if end > len(words) {
			end = len(words)
		}
		var parts []string
		for j := i; j < end; j++ {
			text := assWordText(words[j])
			if text == "" {
				continue
			}
			if j == emphasisIdx {
				text = `{\c` + emphasisColor + `}` + text + emphasisReset
			}
			parts = append(parts, text)
		}
		if len(parts) > 0 {
			lines = append(lines, strings.Join(parts, " "))
		}
	}
	return strings.Join(lines, `\N`)
}

// ASSTime renders seconds as ASS's H:MM:SS.cc (centiseconds).
func ASSTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	totalCS := int(sec*100 + 0.5)
	h := totalCS / 360000
	totalCS -= h * 360000
	m := totalCS / 6000
	totalCS -= m * 6000
	s := totalCS / 100
	cs := totalCS - s*100
	return fmt.Sprintf("%d:%02d:%02d.%02d", h, m, s, cs)
}
