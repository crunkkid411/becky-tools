// xml.go — the encoding/xml model + emitter for a kdenlive/MLT project. The
// struct shape mirrors EXACTLY the document melt 7.37 accepted on real footage
// (see the package doc for the two load-bearing gotchas: validating avformat +
// the xmlns:kdenlive namespace). Marshal is pure (no exec, no disk) so it is
// fully unit-tested; WriteProject is the thin file-writing wrapper.
package kdenlive

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
)

// mltDoc is the root <mlt> element. The xmlns:kdenlive attribute is REQUIRED for
// strict Gen-2 kdenlive parsers; it is emitted as a literal attr field below.
type mltDoc struct {
	XMLName       xml.Name   `xml:"mlt"`
	LCNumeric     string     `xml:"LC_NUMERIC,attr"`
	Version       string     `xml:"version,attr"`
	Producer      string     `xml:"producer,attr"`       // "main_bin" — kdenlive's bin entry point
	Root          string     `xml:"root,attr"`           // "" = paths are absolute/self-contained
	XMLNSKdenlive string     `xml:"xmlns:kdenlive,attr"` // https://kdenlive.org/projectfile (the gotcha)
	Profile       profile    `xml:"profile"`
	Producers     []producer `xml:"producer"`
	Playlists     []playlist `xml:"playlist"`
	Tractor       tractor    `xml:"tractor"`
}

// profile is the timeline geometry (the render width/height/fps).
type profile struct {
	Description      string `xml:"description,attr"`
	Width            int    `xml:"width,attr"`
	Height           int    `xml:"height,attr"`
	Progressive      int    `xml:"progressive,attr"`
	SampleAspectNum  int    `xml:"sample_aspect_num,attr"`
	SampleAspectDen  int    `xml:"sample_aspect_den,attr"`
	DisplayAspectNum int    `xml:"display_aspect_num,attr"`
	DisplayAspectDen int    `xml:"display_aspect_den,attr"`
	FrameRateNum     int    `xml:"frame_rate_num,attr"`
	FrameRateDen     int    `xml:"frame_rate_den,attr"`
	Colorspace       int    `xml:"colorspace,attr"`
}

// property is one <property name="...">value</property> child.
type property struct {
	Name  string `xml:"name,attr"`
	Value string `xml:",chardata"`
}

// producer is one source file exposed to the timeline. in/out are the frame
// range the producer makes available; mlt_service MUST be "avformat" (validating)
// so melt reads the real length and the playlist cuts land.
type producer struct {
	ID         string     `xml:"id,attr"`
	In         int        `xml:"in,attr"`
	Out        int        `xml:"out,attr"`
	Properties []property `xml:"property"`
}

// entry is one clip placed on the playlist: producer ref + the actual CUT in/out
// (frame indices into the producer).
type entry struct {
	Producer string `xml:"producer,attr"`
	In       int    `xml:"in,attr"`
	Out      int    `xml:"out,attr"`
}

// blank is a gap on the playlist (silence/black between clips). length is in
// frames. Emitted only when a caller leaves an explicit gap.
type blank struct {
	Length int `xml:"length,attr"`
}

// playlist is an ordered list of entries/blanks — the timeline track content.
type playlist struct {
	ID         string     `xml:"id,attr"`
	Properties []property `xml:"property,omitempty"`
	Entries    []entry    `xml:"entry"`
}

// track binds a playlist into the tractor.
type track struct {
	Producer string `xml:"producer,attr"`
}

// tractor is the timeline: it composes the track(s). One video track is enough
// for a forensic compilation; multitrack is a later concern.
type tractor struct {
	ID         string     `xml:"id,attr"`
	Properties []property `xml:"property,omitempty"`
	Tracks     []track    `xml:"track"`
}

// kdenliveNamespace is the project-file namespace strict kdenlive builds require
// on the root element. Without it, opening the project can be rejected.
const kdenliveNamespace = "https://kdenlive.org/projectfile"

// meltVersion stamps the <mlt version> — matches the bundled melt we render with.
const meltVersion = "7.37.0"

// Marshal renders the project to kdenlive/MLT XML bytes (with an XML declaration).
// Pure: it touches no disk and runs no process, so it is fully unit-tested. It
// validates the project first and returns a typed error on a bad cut-list.
func (p *Project) Marshal() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}

	fps := p.fps()
	w, h := p.dims()

	// Stable, distinct producer per source path, in first-seen order so the XML
	// is deterministic for a given cut-list.
	order, idForSource := assignProducerIDs(p.Clips)

	producers := make([]producer, 0, len(order))
	for _, src := range order {
		lenFrames := p.producerLengthFrames(src, fps)
		props := []property{
			{Name: "length", Value: strconv.Itoa(lenFrames)},
			{Name: "eof", Value: "pause"},
			{Name: "resource", Value: resourcePath(src)},
			{Name: "mlt_service", Value: "avformat"}, // VALIDATING reader — the gotcha
		}
		if name := clipNameForSource(p.Clips, src); name != "" {
			props = append(props, property{Name: "kdenlive:clipname", Value: name})
		}
		producers = append(producers, producer{
			ID:         idForSource[src],
			In:         0,
			Out:        lenFrames - 1, // inclusive last frame
			Properties: props,
		})
	}

	// One playlist (track 0) holding the ordered cuts.
	entries := make([]entry, 0, len(p.Clips))
	for _, c := range p.Clips {
		inF := secToFrame(c.In, fps)
		outF := secToFrame(c.Out, fps) - 1 // inclusive last frame of the cut
		if outF < inF {
			outF = inF
		}
		entries = append(entries, entry{
			Producer: idForSource[c.Source],
			In:       inF,
			Out:      outF,
		})
	}

	doc := mltDoc{
		LCNumeric:     "C",
		Version:       meltVersion,
		Producer:      "main_bin",
		Root:          "",
		XMLNSKdenlive: kdenliveNamespace,
		Profile: profile{
			Description:      fmt.Sprintf("%dx%d %g fps (becky)", w, h, fps),
			Width:            w,
			Height:           h,
			Progressive:      1,
			SampleAspectNum:  1,
			SampleAspectDen:  1,
			DisplayAspectNum: w,
			DisplayAspectDen: h,
			FrameRateNum:     int(round(fps * 1000)),
			FrameRateDen:     1000,
			Colorspace:       709,
		},
		Producers: producers,
		Playlists: []playlist{{
			ID:      "playlist0",
			Entries: entries,
		}},
		Tractor: tractor{
			ID: "tractor0",
			Tracks: []track{
				{Producer: "playlist0"},
			},
		},
	}

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("kdenlive: marshal project: %w", err)
	}
	out := append([]byte(xml.Header), body...)
	out = append(out, '\n')
	return out, nil
}

// WriteProject marshals the project and writes it to path (0644). It is the only
// disk side-effect of the builder; source videos are never touched.
func WriteProject(p *Project, path string) error {
	data, err := p.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("kdenlive: write %s: %w", path, err)
	}
	return nil
}

// assignProducerIDs returns the distinct source paths in first-seen order plus a
// map source->producerID ("producer0", "producer1", ...). Deterministic.
func assignProducerIDs(clips []Clip) (order []string, ids map[string]string) {
	ids = map[string]string{}
	for _, c := range clips {
		if _, seen := ids[c.Source]; !seen {
			ids[c.Source] = fmt.Sprintf("producer%d", len(order))
			order = append(order, c.Source)
		}
	}
	return order, ids
}

// clipNameForSource returns the first non-empty Name among clips that use src,
// else the source's base filename — a friendly bin label for the kdenlive GUI.
func clipNameForSource(clips []Clip, src string) string {
	for _, c := range clips {
		if c.Source == src && c.Name != "" {
			return c.Name
		}
	}
	return baseName(src)
}

// baseName is a separator-agnostic basename (sources may be C:\... on Linux CI).
func baseName(p string) string {
	s := resourcePath(p) // forward slashes
	if i := lastSlash(s); i >= 0 {
		s = s[i+1:]
	}
	return s
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

// round is a tiny float->nearest-int helper (avoids importing math just for this
// in the marshal path; mirrors math.Round semantics for non-negative input).
func round(f float64) float64 {
	if f < 0 {
		return -round(-f)
	}
	return float64(int64(f + 0.5))
}
