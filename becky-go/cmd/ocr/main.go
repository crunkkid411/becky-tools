// becky-ocr — offline OCR of the frames becky-osint exports (signage, plates,
// storefronts, mail, screens, livestream chat, burned-in timestamps). It reads a
// becky-osint manifest (or any folder of frame images), runs PaddleOCR PP-OCRv5
// via ONNX Runtime (RapidOCR), and emits per-frame recognized text with
// confidence + bbox + frame provenance as JSON to stdout (or --output). With
// --db it also writes one row per line into the forensic DB's ocr_text table (+
// FTS5 mirror) so `becky find "2601 Chatham"` returns the frame.
//
//	becky-ocr --manifest <osint-manifest.json> [options]
//	becky-ocr --frames-dir <dir> [options]
//
// Options:
//
//	--manifest <file>    becky-osint manifest JSON (preferred; carries provenance)
//	--frames-dir <dir>   OR a directory of frame images (jpg/png) to OCR directly
//	--engine ppocr|ppocr-v4   ppocr = PP-OCRv5 via ONNX, falling back to bundled v4
//	--min-confidence 0.5 lines below this rec-confidence go to low_confidence_lines
//	--try-rotations      try 0/90/180/270 per frame, keep the best (sideways fallback)
//	--max-frames N       cap frames OCR'd per run (corpus-scale guard; 0 = no cap)
//	--db <forensic.db>   also write ocr_text rows into this DB (FTS5-indexed)
//	--output <file>      write the manifest JSON here instead of stdout
//	--verbose            progress on stderr
//
// Per FORENSIC-OUTPUT-PHILOSOPHY (top principle): high-confidence reads are
// ASSERTED plainly (text + score + frame provenance); only genuinely low-confidence
// reads are flagged. The OCR engine, heavy compute, runs in an embedded Python
// helper (ocr_paddle.py) exec'd under PYTHONPATH exactly like internal/faceembed —
// the same anaconda interpreter + site-packages dir that already serves face OCR.
//
// Graceful degrade: missing OCR deps/models → a clear top-level note + per-frame
// skip and exit 0 (never a crash, never half-JSON), mirroring the face path.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"becky-go/internal/beckydb"
	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

const toolVersion = "becky-ocr v1.0.0"

// defaultMinConfidence splits asserted reads from flagged low-confidence ones.
// PP-OCRv5 returns ~0.9+ on clear signage/chat; 0.5 keeps confident lines in the
// asserted set and routes only genuinely shaky reads to the flagged list.
const defaultMinConfidence = 0.5

func main() {
	manifestPath := flag.String("manifest", "", "becky-osint manifest JSON (preferred)")
	framesDir := flag.String("frames-dir", "", "OR a directory of frame images to OCR directly")
	engine := flag.String("engine", "ppocr", "ppocr (PP-OCRv5->v4 ONNX) | ppocr-v4 (bundled offline)")
	minConf := flag.Float64("min-confidence", defaultMinConfidence, "lines below this confidence are flagged low-confidence")
	tryRotations := flag.Bool("try-rotations", false, "try 0/90/180/270 per frame, keep the best (sideways-frame fallback)")
	maxFrames := flag.Int("max-frames", 0, "cap frames OCR'd per run (0 = no cap)")
	dbPath := flag.String("db", "", "also write ocr_text rows into this forensic DB")
	output := flag.String("output", "", "write the manifest JSON here instead of stdout")
	verbose := flag.Bool("verbose", false, "show progress on stderr")
	flag.Parse()

	if *manifestPath == "" && *framesDir == "" {
		beckyio.Fatalf("usage: becky-ocr --manifest <osint-manifest.json> | becky-ocr --frames-dir <dir> [options]")
	}
	if *manifestPath != "" && *framesDir != "" {
		beckyio.Fatalf("pass --manifest OR --frames-dir, not both")
	}

	cfg := config.Load()

	// Gather the frames to OCR (with provenance) from whichever input was given.
	frames, sourceLabel, err := gatherFrames(*manifestPath, *framesDir, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if *maxFrames > 0 && len(frames) > *maxFrames {
		beckyio.Logf(*verbose, "capping %d frames to --max-frames=%d", len(frames), *maxFrames)
		frames = frames[:*maxFrames]
	}
	beckyio.Logf(*verbose, "OCR'ing %d frame(s) from %s", len(frames), sourceLabel)

	out := Output{
		Tool:           toolVersion,
		SourceManifest: sourceLabel,
		Results:        []FrameResult{},
		Skipped:        []SkipRecord{},
		Notes:          map[string]string{},
	}

	if len(frames) == 0 {
		out.Notes["input"] = "no frame images found to OCR"
		out.Engine = engineLabel(*engine)
		emit(out, *output)
		return
	}

	// Run the OCR helper over all frames in ONE invocation (the helper loads the
	// ONNX models once, then OCRs the batch — the warm-model pattern the spec
	// requires for corpus scale).
	paths := make([]string, len(frames))
	for i, f := range frames {
		paths[i] = f.FramePath
	}
	helperOut, herr := RunOCR(cfg, paths, *engine, *tryRotations, *verbose)
	if herr != nil {
		// Graceful degrade: surface a clean note + per-frame skip, exit 0.
		out.Notes["ocr"] = "OCR engine unavailable: " + herr.Error()
		out.Engine = engineLabel(*engine)
		for _, f := range frames {
			out.Skipped = append(out.Skipped, SkipRecord{
				FramePath: f.FramePath, Reason: "ocr engine unavailable",
			})
		}
		beckyio.Logf(true, "warning: OCR degraded gracefully: %v", herr)
		emit(out, *output)
		return
	}
	out.Engine = helperOut.Engine

	// Optional DB writer: open + ensure the ocr_text schema once, cache FTS state.
	var db *beckydb.DB
	ftsLive := false
	if *dbPath != "" {
		db, err = openOCRDB(cfg, *dbPath)
		if err != nil {
			beckyio.Fatalf("%v", err)
		}
		ftsLive = db.FTS5Available()
		beckyio.Logf(*verbose, "db: %s (fts5 %v)", *dbPath, ftsLive)
	}

	// Index helper records by path so we can re-attach each result to its frame's
	// provenance (the helper only knows the path it was handed).
	byPath := make(map[string]HelperFrame, len(helperOut.Results))
	for _, r := range helperOut.Results {
		byPath[r.Path] = r
	}

	rowsWritten := 0
	for _, fr := range frames {
		hr, ok := byPath[fr.FramePath]
		if !ok {
			out.Skipped = append(out.Skipped, SkipRecord{
				FramePath: fr.FramePath, Reason: "no OCR result returned",
			})
			continue
		}
		res := buildFrameResult(fr, hr, *minConf)
		out.Results = append(out.Results, res)
		out.FramesOCRd++

		if db != nil {
			n, werr := writeFrameRows(db, res, ftsLive)
			if werr != nil {
				beckyio.Logf(true, "warning: db write for %s failed: %v", fr.FramePath, werr)
				out.Notes["db"] = "one or more db writes failed: " + werr.Error()
			}
			rowsWritten += n
		}
	}
	if db != nil {
		out.RowsWritten = rowsWritten
		total, _ := db.CountOCRLines()
		beckyio.Logf(*verbose, "wrote %d ocr_text row(s); table now holds %d", rowsWritten, total)
	}

	beckyio.Logf(*verbose, "OCR'd %d frame(s) with engine %s", out.FramesOCRd, out.Engine)
	emit(out, *output)
}

// openOCRDB opens the forensic DB and ensures ONLY the additive ocr_text schema
// (never the canonical schema — that belongs to becky-embed). Idempotent.
func openOCRDB(cfg config.Config, dbPath string) (*beckydb.DB, error) {
	db, err := beckydb.Open(cfg, dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.EnsureOCRSchema(); err != nil {
		return nil, fmt.Errorf("ensure ocr schema: %w", err)
	}
	return db, nil
}

// writeFrameRows persists every recognized line (asserted + low-confidence) for one
// frame into ocr_text, keyed deterministically so re-runs are idempotent. Returns
// the number of rows written.
func writeFrameRows(db *beckydb.DB, res FrameResult, ftsLive bool) (int, error) {
	all := append(append([]Line{}, res.Lines...), res.LowConfidenceLines...)
	written := 0
	for ord, ln := range all {
		bbox := "[]"
		if len(ln.BBox) == 4 {
			bbox = fmt.Sprintf("[%d,%d,%d,%d]", ln.BBox[0], ln.BBox[1], ln.BBox[2], ln.BBox[3])
		}
		row := beckydb.OCRLine{
			OCRID:        ocrID(res.SourceFile, res.FrameIndex, ord),
			SourceFile:   res.SourceFile,
			SourceSHA256: res.SourceSHA256,
			FramePath:    res.FramePath,
			Timestamp:    res.Timestamp,
			FrameIndex:   res.FrameIndex,
			Text:         ln.Text,
			Confidence:   ln.Confidence,
			Category:     ln.Category,
			BBoxJSON:     bbox,
		}
		if err := db.InsertOCRLine(row, ftsLive); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// emit writes the output JSON to stdout (default) or to --output, with a stable
// ordering (timestamp, then frame path) for deterministic output.
func emit(o Output, outPath string) {
	sort.SliceStable(o.Results, func(i, j int) bool {
		if o.Results[i].Timestamp != o.Results[j].Timestamp {
			return o.Results[i].Timestamp < o.Results[j].Timestamp
		}
		return o.Results[i].FramePath < o.Results[j].FramePath
	})
	if outPath == "" {
		beckyio.PrintJSON(o)
		return
	}
	b, err := marshalIndent(o)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		beckyio.Fatalf("write output: %v", err)
	}
}

// engineLabel returns the human label for the requested engine, used when the
// helper never ran (so the degraded output still names what was attempted).
func engineLabel(engine string) string {
	if engine == "ppocr-v4" {
		return "ppocr-v4-onnx"
	}
	return "ppocr-v5-onnx"
}
