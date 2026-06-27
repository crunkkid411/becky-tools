// fcpxml.go — the Reel -> FCPXML 1.10 output (SPEC-BECKY-OTIO §7, Phase 2). FCPXML
// is the universal fallback Final Cut Pro and Premiere (via the X27 plugin) read,
// and a Resolve alternative when .otio isn't convenient. This emits FLAT v1.10 XML
// (not the .fcpxmld bundle): a <resources> block with one <format> per distinct
// frame rate and one <asset> per distinct source, then a
// library > event > project > sequence > spine of <asset-clip>s placed end-to-end.
//
// Times are rational frame strings ("1950/30s"): on the timeline (offset/duration)
// they are in the SEQUENCE frame rate; a clip's source in-point (start) is in that
// source's OWN frame rate, so mixed-rate sources line up correctly. Pure Go: no
// exec, no probe, no models. Honest limits (this is a review fallback, not a
// finished conform): the canvas width/height are nominal defaults (the real
// resolution comes from the media on import), and assets are marked hasVideo +
// hasAudio (most forensic footage is A/V; a video-only or audio-only source yields
// a benign FCP import warning, never a failure).
package otio

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strings"

	"becky-go/internal/edl"
	"becky-go/internal/pathx"
)

// fcpxmlVersion is the schema version emitted on the root element.
const fcpxmlVersion = "1.10"

// Nominal review-canvas geometry. The asset's media-rep points at the real file,
// so FCP reads true per-clip resolution on import; this only sizes the sequence.
const (
	fcpDefaultWidth  = 1920
	fcpDefaultHeight = 1080
)

type fcpxmlDoc struct {
	XMLName   xml.Name     `xml:"fcpxml"`
	Version   string       `xml:"version,attr"`
	Resources fcpResources `xml:"resources"`
	Library   fcpLibrary   `xml:"library"`
}

type fcpResources struct {
	Formats []fcpFormat `xml:"format"`
	Assets  []fcpAsset  `xml:"asset"`
}

type fcpFormat struct {
	ID            string `xml:"id,attr"`
	Name          string `xml:"name,attr"`
	FrameDuration string `xml:"frameDuration,attr"`
	Width         int    `xml:"width,attr"`
	Height        int    `xml:"height,attr"`
}

type fcpAsset struct {
	ID       string      `xml:"id,attr"`
	Name     string      `xml:"name,attr"`
	Start    string      `xml:"start,attr"` // media's own start tc, "0s"
	Duration string      `xml:"duration,attr"`
	HasVideo string      `xml:"hasVideo,attr"`
	HasAudio string      `xml:"hasAudio,attr"`
	Format   string      `xml:"format,attr"`
	MediaRep fcpMediaRep `xml:"media-rep"`
}

type fcpMediaRep struct {
	Kind string `xml:"kind,attr"`
	Src  string `xml:"src,attr"`
}

type fcpLibrary struct {
	Event fcpEvent `xml:"event"`
}

type fcpEvent struct {
	Name    string     `xml:"name,attr"`
	Project fcpProject `xml:"project"`
}

type fcpProject struct {
	Name     string      `xml:"name,attr"`
	Sequence fcpSequence `xml:"sequence"`
}

type fcpSequence struct {
	Format   string   `xml:"format,attr"`
	Duration string   `xml:"duration,attr"`
	TCStart  string   `xml:"tcStart,attr"`
	TCFormat string   `xml:"tcFormat,attr"`
	Spine    fcpSpine `xml:"spine"`
}

type fcpSpine struct {
	Clips []fcpAssetClip `xml:"asset-clip"`
}

type fcpAssetClip struct {
	Ref      string `xml:"ref,attr"`
	Name     string `xml:"name,attr"`
	Offset   string `xml:"offset,attr"`
	Start    string `xml:"start,attr"`
	Duration string `xml:"duration,attr"`
	Format   string `xml:"format,attr"`
}

// WriteFCPXML writes the Reel as a flat FCPXML 1.10 document. Degenerate clips
// (out <= in) are skipped, matching the other writers. opts.IncludeAudio is not
// used (an asset-clip carries the source's audio inherently). Returns an error
// only on a marshal failure or a Reel with no playable clips.
func WriteFCPXML(w io.Writer, r edl.Reel, opts Options) error {
	// First-seen-ordered distinct frame rates -> formats, and sources -> assets,
	// so the output is deterministic for a given Reel.
	type fmtEntry struct {
		id, fd string
	}
	fmtByDur := map[string]string{} // frameDuration string -> format id
	var fmts []fmtEntry
	formatIDForFPS := func(fps float64) string {
		num, den := frameDurRational(fps)
		fd := fmt.Sprintf("%d/%ds", num, den)
		if id, ok := fmtByDur[fd]; ok {
			return id
		}
		id := fmt.Sprintf("r%d", len(fmts))
		fmtByDur[fd] = id
		fmts = append(fmts, fmtEntry{id: id, fd: fd})
		return id
	}

	assetIDForSrc := map[string]string{}
	var assetOrder []string             // source paths, first-seen order
	assetFPS := map[string]float64{}    // source -> its fps
	assetMaxOut := map[string]float64{} // source -> farthest out-point used
	clipFmtID := map[string]string{}    // source -> format id

	type placed struct {
		ref, name, offset, start, duration, format string
	}
	var spine []placed
	var totalTLFrames int

	timelineFPS := 0.0
	var tNum, tDen int

	for _, c := range r.Clips {
		if c.Dur() <= 0 {
			continue
		}
		cf := fps(c, opts)
		if timelineFPS == 0 { // first playable clip sets the timeline rate
			timelineFPS = cf
			tNum, tDen = frameDurRational(cf)
		}
		fmtID := formatIDForFPS(cf)

		if _, seen := assetIDForSrc[c.Source]; !seen {
			id := fmt.Sprintf("a%d", len(assetOrder))
			assetIDForSrc[c.Source] = id
			assetOrder = append(assetOrder, c.Source)
			assetFPS[c.Source] = cf
			clipFmtID[c.Source] = fmtID
		}
		if c.Out > assetMaxOut[c.Source] {
			assetMaxOut[c.Source] = c.Out
		}

		name := strings.TrimSpace(c.Label)
		if name == "" {
			name = pathx.Base(c.Source)
		}

		// Timeline-rate frames for offset + duration; asset-rate frames for start.
		durTL := int(math.Round(c.Dur() * timelineFPS))
		aNum, aDen := frameDurRational(cf)
		startFr := int(math.Round(c.In * cf))

		spine = append(spine, placed{
			ref:      assetIDForSrc[c.Source],
			name:     name,
			offset:   fcpTime(totalTLFrames, tNum, tDen),
			start:    fcpTime(startFr, aNum, aDen),
			duration: fcpTime(durTL, tNum, tDen),
			format:   fmtID,
		})
		totalTLFrames += durTL
	}

	if len(spine) == 0 {
		return fmt.Errorf("fcpxml: reel %q has no playable clips", r.Name)
	}

	// Build the resources.
	formats := make([]fcpFormat, 0, len(fmts))
	for _, f := range fmts {
		formats = append(formats, fcpFormat{
			ID:            f.id,
			Name:          "becky-" + strings.TrimSuffix(f.fd, "s"),
			FrameDuration: f.fd,
			Width:         fcpDefaultWidth,
			Height:        fcpDefaultHeight,
		})
	}
	assets := make([]fcpAsset, 0, len(assetOrder))
	for _, src := range assetOrder {
		afps := assetFPS[src]
		aNum, aDen := frameDurRational(afps)
		// Available media length: cover the farthest cut + 1s pad, in asset frames.
		lenFr := int(math.Round((assetMaxOut[src] + 1.0) * afps))
		if lenFr < 1 {
			lenFr = 1
		}
		assets = append(assets, fcpAsset{
			ID:       assetIDForSrc[src],
			Name:     pathx.Base(src),
			Start:    "0s",
			Duration: fcpTime(lenFr, aNum, aDen),
			HasVideo: "1",
			HasAudio: "1",
			Format:   clipFmtID[src],
			MediaRep: fcpMediaRep{Kind: "original-media", Src: FileURL(src)},
		})
	}

	// Spine.
	clips := make([]fcpAssetClip, 0, len(spine))
	for _, p := range spine {
		clips = append(clips, fcpAssetClip{
			Ref: p.ref, Name: p.name, Offset: p.offset,
			Start: p.start, Duration: p.duration, Format: p.format,
		})
	}

	eventName := strings.TrimSpace(r.Name)
	if eventName == "" {
		eventName = "becky-review"
	}
	doc := fcpxmlDoc{
		Version:   fcpxmlVersion,
		Resources: fcpResources{Formats: formats, Assets: assets},
		Library: fcpLibrary{Event: fcpEvent{
			Name: "becky-review",
			Project: fcpProject{
				Name: eventName,
				Sequence: fcpSequence{
					Format:   formats[0].ID, // timeline format = first clip's rate
					Duration: fcpTime(totalTLFrames, tNum, tDen),
					TCStart:  "0s",
					TCFormat: "NDF",
					Spine:    fcpSpine{Clips: clips},
				},
			},
		}},
	}

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("fcpxml: marshal: %w", err)
	}
	out := []byte(xml.Header + "<!DOCTYPE fcpxml>\n")
	out = append(out, body...)
	out = append(out, '\n')
	_, err = w.Write(out)
	return err
}

// frameDurRational returns the FCPXML <format> frameDuration as (numerator,
// denominator) seconds-per-frame. Whole rates are 1/N; the common NTSC fractional
// rates use their exact 1001/x000 rationals; anything else falls back to a
// millihertz timebase so an unusual fps still emits a valid (frame-aligned) time.
func frameDurRational(fps float64) (num, den int) {
	switch {
	case math.Abs(fps-23.976) < 0.02:
		return 1001, 24000
	case math.Abs(fps-29.97) < 0.02:
		return 1001, 30000
	case math.Abs(fps-47.952) < 0.03:
		return 1001, 48000
	case math.Abs(fps-59.94) < 0.03:
		return 1001, 60000
	case math.Abs(fps-119.88) < 0.06:
		return 1001, 120000
	}
	r := int(math.Round(fps))
	if r >= 1 && math.Abs(fps-float64(r)) < 0.001 {
		return 1, r
	}
	if fps <= 0 {
		return 1, int(edl.DefaultFPS)
	}
	return 1000, int(math.Round(fps * 1000))
}

// fcpTime formats a frame count as an FCPXML rational time string. Zero is the
// canonical "0s"; otherwise it is frames * frameDurNum / frameDurDen seconds, kept
// on the format's timebase (unreduced) so it stays exactly frame-aligned —
// e.g. 1950 frames at (1,30) -> "1950/30s".
func fcpTime(frames, fdNum, fdDen int) string {
	if frames == 0 {
		return "0s"
	}
	return fmt.Sprintf("%d/%ds", frames*fdNum, fdDen)
}
