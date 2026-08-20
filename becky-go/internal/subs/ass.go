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
	Blur          int // libass \blur radius on the border — his soft dark halo
	Alignment     int // libass numpad convention; 2 = bottom-centre, matches Style
	MarginV       int
	MarginH       int    // left AND right margin; what is left is the text column width
	EmphasisColor string // ASS inline colour code, &HBBGGRR& (BLUE-GREEN-RED — reverse of HTML's RRGGBB)
}

// DefaultJordanStyle is the jordan look MEASURED off his own rendered short
// (the 1080x1920 `spitters_are_quitters` clip), not guessed at. Every number
// below came off pixels in that file:
//
//	cap height        57px, dead consistent across 53 captioned frames
//	line pitch        106px (lines at y=1245 and y=1351)
//	block bottom      512px up from the frame bottom = 26.7% of height
//	hard black border ~10px, then a 3px ramp — a thick outline, softly edged
//	longest line      715px = 66% of the frame width, so the text column is 66%
//	emphasis colour   #14F2EF measured — pure cyan
//
// The font was settled by rendering every heavy sans on this machine at that
// exact cap height and scoring glyph IoU against his actual pixels:
//
//	Montserrat ExtraBold 0.803   Gotham Black 0.801   Segoe UI Black 0.796
//	Montserrat Black     0.794   Arial Black  0.783
//	ProximaNova-Semibold 0.609  <- what this style used to ship
//
// The top four are a statistical tie; Montserrat ExtraBold wins on the number
// AND is a real installed family name libass resolves, so it is the pick.
// ProximaNova-Semibold was the outlier Jordan spotted on sight ("the font used
// is very different than the ground truth") — a SEMIBOLD where his is an
// EXTRABOLD.
//
// NOT reproduced here, and deliberately: he also uses a WHOLE-BLOCK yellow
// (sometimes italic) for a directive — "FRENCH FRY FIRST", "PUT THE". That is a
// semantic call, not an audio one, so it stays out until it can be grounded.
// ASS Fontsize is NOT the em square — libass sizes a face by its
// ascent+descent, so the cap height you actually get depends on the font's own
// metrics. Measured for Montserrat ExtraBold by rendering this exact style at
// 80/100/110/114/120 and reading the pixels back:
//
//	Fontsize  80 -> cap 40    110 -> cap 55
//	Fontsize 100 -> cap 50    114 -> cap 57   <- Jordan's
//	Fontsize 120 -> cap 60
//
// i.e. cap = Fontsize / 2 exactly, for this face. Shipping 80 because "his cap
// height is 57" would have been 30% too small, and it was: the first render at
// 80 measured cap 40 against his 57.
func DefaultJordanStyle(outputHeight, outputWidth int) ASSStyle {
	fontSize := outputHeight * 594 / 10000 // 114 at 1920 -> cap height 57px, measured
	if fontSize < 40 {
		fontSize = 40
	}
	return ASSStyle{
		FontName:  "Montserrat ExtraBold",
		FontSize:  fontSize,
		Bold:      0,                    // the ExtraBold face IS the weight; Bold=1 would fake-embolden it further
		Outline:   fontSize * 95 / 1000, // 10 at 114, against his measured 11
		Shadow:    0,
		Blur:      2,
		Alignment: 2,
		// 487 at 1920, which puts the BOTTOM OF THE TEXT 512px up from the frame
		// bottom — his number. MarginV is not that distance: libass adds the
		// face's descender below the baseline even on all-caps text, measured
		// here at 11px, so a MarginV of 499 rendered a 523px gap.
		MarginV:       outputHeight * 254 / 1000,
		MarginH:       outputWidth * 17 / 100, // both sides -> a 66%-wide text column
		EmphasisColor: "&HFFFF00&",            // cyan (R=00 G=FF B=FF -> BB GG RR = FF FF 00)
	}
}

// emphasisReset is the colour every emphasised word reverts to — the style's
// own PrimaryColour (opaque white), so text after the coloured word matches
// the rest of the line exactly.
const emphasisReset = `{\c&HFFFFFF&}`

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
	// WrapStyle 3 is "smart wrapping, BOTTOM line wider", which is exactly how
	// his own blocks break: AREN'T / SUPPOSED, WELL LET / ME FINISH THAT. Letting
	// libass wrap — it has the real font metrics — instead of counting words here
	// is also why the old 3-words-per-line rule is gone: it produced a three-line
	// block on his footage where he never uses more than two.
	fmt.Fprintf(bw, "[Script Info]\r\nScriptType: v4.00+\r\nPlayResX: %d\r\nPlayResY: %d\r\nScaledBorderAndShadow: yes\r\nWrapStyle: 3\r\n\r\n",
		width, height)
	fmt.Fprintf(bw, "[V4+ Styles]\r\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, "+
		"BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, "+
		"Alignment, MarginL, MarginR, MarginV, Encoding\r\n")
	// PrimaryColour &H00FFFFFF opaque white, OutlineColour/BackColour
	// &H00000000 opaque black — same AABBGGRR convention style.go's
	// ForceStyle documents. BorderStyle=1 is a true outline+shadow (3 would
	// be an opaque box, wrong for this look).
	fmt.Fprintf(bw, "Style: Jordan,%s,%d,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,%d,0,0,0,100,100,0,0,1,%d,%d,%d,%d,%d,%d,1\r\n\r\n",
		st.FontName, st.FontSize, st.Bold, st.Outline, st.Shadow, st.Alignment,
		st.MarginH, st.MarginH, st.MarginV)
	fmt.Fprintf(bw, "[Events]\r\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\r\n")

	// \blur softens the OUTER edge of the border, which is the measured halo:
	// ~10px of solid black, then a short ramp into the picture. Without it the
	// border is a hard cut-out and reads as a sticker.
	blur := ""
	if st.Blur > 0 {
		blur = fmt.Sprintf(`{\blur%d}`, st.Blur)
	}
	for i, cw := range cues {
		idx := -1
		if i < len(emphasis) {
			idx = emphasis[i]
		}
		text := jordanLines(cw.Words, idx, st.EmphasisColor)
		if text == "" {
			continue
		}
		fmt.Fprintf(bw, "Dialogue: 0,%s,%s,Jordan,,0,0,0,,%s%s\r\n", ASSTime(cw.Start), ASSTime(cw.End), blur, text)
	}
	return bw.Flush()
}

// jordanLines is one cue's words as a single ASS line, upper-cased, with
// emphasisIdx (an index into words, or any out-of-range value for "nothing")
// wrapped in emphasisColor and reset back to white right after.
//
// It no longer inserts \N breaks. libass wraps this itself, inside the style's
// MarginL/MarginR column and under WrapStyle 3 — it has the font metrics this
// package does not, and the old fixed 3-words-per-line rule put THREE lines on
// screen where Jordan never uses more than two.
func jordanLines(words []Word, emphasisIdx int, emphasisColor string) string {
	var parts []string
	for j, w := range words {
		text := assWordText(w)
		if text == "" {
			continue
		}
		if j == emphasisIdx {
			text = `{\c` + emphasisColor + `}` + text + emphasisReset
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " ")
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
