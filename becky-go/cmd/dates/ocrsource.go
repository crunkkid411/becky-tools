// ocrsource.go — the default TimestampSource: it reads an existing becky-ocr
// ocr.json and yields the burned-in candidate_timestamp lines per source file.
// becky-dates NEVER runs the OCR model itself (that needs PP-OCRv5 on Jordan's
// hardware); it only consumes the already-produced JSON. This keeps signal C
// optional and the whole triangulation core cloud-buildable.
//
// The ocr.json shapes below mirror cmd/ocr/ocr.go's FrameResult/Line output
// (becky-ocr is package main, so its types can't be imported — only the JSON
// contract is shared). Both `lines` (asserted, >= min-conf) and
// `low_confidence_lines` are read; trust is scaled later by --min-ocr-conf.
package main

import (
	"encoding/json"
	"os"
	"strings"

	"becky-go/internal/datetri"
	"becky-go/internal/pathx"
)

// catTimestamp is the becky-ocr category label for a burned-in date/clock line.
// (Mirrors cmd/ocr/categorize.go's catTimestamp constant.)
const catTimestamp = "candidate_timestamp"

// ocrLine mirrors one line in an ocr.json FrameResult.
type ocrLine struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
	Category   string  `json:"category"`
}

// ocrFrame mirrors one FrameResult in ocr.json.
type ocrFrame struct {
	SourceFile         string    `json:"source_file"`
	Timestamp          float64   `json:"timestamp"`
	Lines              []ocrLine `json:"lines"`
	LowConfidenceLines []ocrLine `json:"low_confidence_lines"`
}

// ocrFile mirrors the top-level ocr.json document (only the fields we need).
type ocrFile struct {
	Results []ocrFrame `json:"results"`
}

// ocrSource indexes burned-in timestamp candidates by source-file basename.
type ocrSource struct {
	byBase map[string][]datetri.OCRDateCandidate
}

// newOCRSource reads and indexes an ocr.json file. It keys candidates by the
// source file's basename (pathx.Base) so a Windows path in the ocr.json matches
// a Windows or POSIX path passed to becky-dates.
func newOCRSource(path string) (*ocrSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc ocrFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	src := &ocrSource{byBase: map[string][]datetri.OCRDateCandidate{}}
	for _, fr := range doc.Results {
		key := strings.ToLower(pathx.Base(fr.SourceFile))
		for _, ln := range fr.Lines {
			if ln.Category == catTimestamp {
				src.byBase[key] = append(src.byBase[key], datetri.OCRDateCandidate{
					Text: ln.Text, Confidence: ln.Confidence, FrameTimestamp: fr.Timestamp,
				})
			}
		}
		for _, ln := range fr.LowConfidenceLines {
			if ln.Category == catTimestamp {
				src.byBase[key] = append(src.byBase[key], datetri.OCRDateCandidate{
					Text: ln.Text, Confidence: ln.Confidence, FrameTimestamp: fr.Timestamp,
				})
			}
		}
	}
	return src, nil
}

// BurnedInDates returns the burned-in candidates for the given source file,
// matched by basename. Returns nil when none are indexed for that clip.
func (s *ocrSource) BurnedInDates(sourceFile string) []datetri.OCRDateCandidate {
	if s == nil {
		return nil
	}
	return s.byBase[strings.ToLower(pathx.Base(sourceFile))]
}
