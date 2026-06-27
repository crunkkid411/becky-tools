package otio

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
)

// fcpParse is the minimal view of the emitted FCPXML the tests assert on.
type fcpParse struct {
	XMLName   xml.Name `xml:"fcpxml"`
	Version   string   `xml:"version,attr"`
	Resources struct {
		Formats []struct {
			ID            string `xml:"id,attr"`
			FrameDuration string `xml:"frameDuration,attr"`
		} `xml:"format"`
		Assets []struct {
			ID       string `xml:"id,attr"`
			Duration string `xml:"duration,attr"`
			MediaRep struct {
				Src string `xml:"src,attr"`
			} `xml:"media-rep"`
		} `xml:"asset"`
	} `xml:"resources"`
	Library struct {
		Event struct {
			Project struct {
				Sequence struct {
					Format   string `xml:"format,attr"`
					Duration string `xml:"duration,attr"`
					Spine    struct {
						Clips []struct {
							Ref      string `xml:"ref,attr"`
							Offset   string `xml:"offset,attr"`
							Start    string `xml:"start,attr"`
							Duration string `xml:"duration,attr"`
						} `xml:"asset-clip"`
					} `xml:"spine"`
				} `xml:"sequence"`
			} `xml:"project"`
		} `xml:"event"`
	} `xml:"library"`
}

func TestWriteFCPXML_StructureAndRationalTimes(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFCPXML(&buf, twoClipReel(), Options{}); err != nil {
		t.Fatalf("WriteFCPXML: %v", err)
	}

	// The DOCTYPE must be present and precede the root.
	s := buf.String()
	if !strings.Contains(s, "<!DOCTYPE fcpxml>") {
		t.Error("missing <!DOCTYPE fcpxml>")
	}

	var doc fcpParse
	if err := xml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("emitted FCPXML is not valid XML: %v", err)
	}
	if doc.Version != "1.10" {
		t.Errorf("version = %q, want 1.10", doc.Version)
	}

	// Two distinct frame rates -> two formats; two distinct sources -> two assets.
	if len(doc.Resources.Formats) != 2 {
		t.Fatalf("format count = %d, want 2", len(doc.Resources.Formats))
	}
	if doc.Resources.Formats[0].FrameDuration != "1/30s" {
		t.Errorf("format[0] frameDuration = %q, want 1/30s", doc.Resources.Formats[0].FrameDuration)
	}
	if doc.Resources.Formats[1].FrameDuration != "1/25s" {
		t.Errorf("format[1] frameDuration = %q, want 1/25s", doc.Resources.Formats[1].FrameDuration)
	}
	if len(doc.Resources.Assets) != 2 {
		t.Fatalf("asset count = %d, want 2", len(doc.Resources.Assets))
	}
	// Asset media length: cam1 (73.5+1)*30 = 2235; cam2 (128+1)*25 = 3225.
	if doc.Resources.Assets[0].Duration != "2235/30s" {
		t.Errorf("asset[0] duration = %q, want 2235/30s", doc.Resources.Assets[0].Duration)
	}
	if doc.Resources.Assets[0].MediaRep.Src != "file:///C:/Videos/cam1.mp4" {
		t.Errorf("asset[0] media src = %q, want file:///C:/Videos/cam1.mp4", doc.Resources.Assets[0].MediaRep.Src)
	}
	if doc.Resources.Assets[1].Duration != "3225/25s" {
		t.Errorf("asset[1] duration = %q, want 3225/25s", doc.Resources.Assets[1].Duration)
	}

	seq := doc.Library.Event.Project.Sequence
	// Timeline format = first clip's rate.
	if seq.Format != doc.Resources.Formats[0].ID {
		t.Errorf("sequence format = %q, want first format id %q", seq.Format, doc.Resources.Formats[0].ID)
	}
	// Total timeline length: 255 + 240 = 495 frames at 30fps.
	if seq.Duration != "495/30s" {
		t.Errorf("sequence duration = %q, want 495/30s", seq.Duration)
	}

	clips := seq.Spine.Clips
	if len(clips) != 2 {
		t.Fatalf("spine clip count = %d, want 2", len(clips))
	}
	// Clip A: offset 0 -> "0s"; start 65s@30 -> 1950/30s; dur 8.5s@30 -> 255/30s.
	if clips[0].Offset != "0s" || clips[0].Start != "1950/30s" || clips[0].Duration != "255/30s" {
		t.Errorf("clip A = {offset:%q start:%q dur:%q}, want {0s 1950/30s 255/30s}",
			clips[0].Offset, clips[0].Start, clips[0].Duration)
	}
	// Clip B: offset 255@30 -> 255/30s; start 120s@25 -> 3000/25s (asset rate);
	// dur 8s@30 -> 240/30s (timeline rate).
	if clips[1].Offset != "255/30s" || clips[1].Start != "3000/25s" || clips[1].Duration != "240/30s" {
		t.Errorf("clip B = {offset:%q start:%q dur:%q}, want {255/30s 3000/25s 240/30s}",
			clips[1].Offset, clips[1].Start, clips[1].Duration)
	}
}

func TestWriteFCPXML_NTSCRational(t *testing.T) {
	r := twoClipReel()
	r.Clips = r.Clips[:1]
	r.Clips[0].Meta.SourceFPS = 29.97
	r.Clips[0].In = 0
	r.Clips[0].Out = 1 // one second
	var buf bytes.Buffer
	if err := WriteFCPXML(&buf, r, Options{}); err != nil {
		t.Fatalf("WriteFCPXML: %v", err)
	}
	var doc fcpParse
	if err := xml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid XML: %v", err)
	}
	if doc.Resources.Formats[0].FrameDuration != "1001/30000s" {
		t.Errorf("29.97 frameDuration = %q, want 1001/30000s", doc.Resources.Formats[0].FrameDuration)
	}
	// 1 second at 29.97 = round(1*29.97)=30 frames -> 30*1001/30000 = 30030/30000s.
	if d := doc.Library.Event.Project.Sequence.Spine.Clips[0].Duration; d != "30030/30000s" {
		t.Errorf("1s @29.97 duration = %q, want 30030/30000s", d)
	}
}

func TestWriteFCPXML_ErrorsOnAllDegenerate(t *testing.T) {
	r := twoClipReel()
	r.Clips[0].Out = r.Clips[0].In
	r.Clips[1].Out = r.Clips[1].In
	var buf bytes.Buffer
	if err := WriteFCPXML(&buf, r, Options{}); err == nil {
		t.Error("expected an error for an all-degenerate Reel, got nil")
	}
}
