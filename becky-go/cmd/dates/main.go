// becky-dates — forensic "when was this captured?" date triangulation.
//
// For each clip it gathers several INDEPENDENT date signals (container capture
// tag via exifmeta, the untrusted filesystem mtime, a filename date token, and an
// optional burned-in on-screen OCR date from an existing becky-ocr ocr.json) and
// fuses them with corroborate-then-conclude (internal/datetri) into one verdict
// per clip: DOCUMENTED / CANDIDATE / CONFLICT / UNKNOWN, with the basis attached.
//
// It reads only; it never modifies a source file. JSON goes to stdout; one
// concise human line per clip goes to stderr (ACCESSIBILITY.md: lead with the
// answer, keep it tight). Deterministic + offline + degrade-never-crash.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/datetri"
	"becky-go/internal/exifmeta"
	"becky-go/internal/pathx"
)

const version = "becky-dates v1.0.0"

func main() {
	var (
		ocrPath    = flag.String("ocr", "", "becky-ocr ocr.json to mine for burned-in candidate_timestamp lines")
		recursive  = flag.Bool("recursive", false, "recurse into subfolders when given a folder")
		tolerance  = flag.Int("tolerance", 1, "calendar-day slop for \"signals agree\"")
		exiftool   = flag.String("exiftool", "", "override exiftool binary (else auto-detect)")
		ffprobe    = flag.String("ffprobe", "", "override ffprobe binary (default from ~/.becky/config.json)")
		minOCRConf = flag.Float64("min-ocr-conf", 0.80, "OCR timestamp read at/above this = strong signal; below = weak")
		outPath    = flag.String("output", "", "write JSON here instead of stdout")
		jsonOnly   = flag.Bool("json", false, "JSON only, suppress the per-clip human line on stderr")
		verbose    = flag.Bool("verbose", false, "progress on stderr")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: becky-dates [options] <folder | video.mp4 ...>\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	cfg := config.Load()
	ffprobeBin := *ffprobe
	if ffprobeBin == "" {
		ffprobeBin = cfg.FFprobe
	}

	files, skipped := expandInputs(flag.Args(), *recursive)
	if skipped == nil {
		skipped = []SkipRecord{}
	}

	// The OCR signal source: a file reader when --ocr is given, else nil (signal
	// C absent, degrade exit 0). Per-clip auto-discovery is also handled inside.
	var ts datetri.TimestampSource
	ocrNote := "not supplied; burned-in on-screen timestamps were not consulted (run becky-ocr and pass --ocr)"
	if *ocrPath != "" {
		src, err := newOCRSource(*ocrPath)
		if err != nil {
			beckyio.Logf(*verbose, "ocr: %v (continuing without burned-in dates)", err)
			ocrNote = "could not read --ocr file (" + err.Error() + "); burned-in dates not consulted"
		} else {
			ts = src
			ocrNote = "read from " + *ocrPath
		}
	}

	ex := exifmeta.NewExtractor(*exiftool, ffprobeBin)

	out := Output{
		Tool:    version,
		Folder:  folderOf(flag.Args()),
		Results: []ClipResult{},
		Skipped: skipped,
		Notes:   map[string]string{"ocr": ocrNote},
	}

	for _, f := range files {
		beckyio.Logf(*verbose, "dating %s", f)
		r := dateClip(ex, f, ts, *minOCRConf, *tolerance)
		out.Results = append(out.Results, r)
		if !*jsonOnly {
			fmt.Fprintln(os.Stderr, humanLine(r))
		}
	}
	out.ClipsDated = len(out.Results)

	if *outPath != "" {
		writeJSONFile(*outPath, out)
	} else {
		beckyio.PrintJSON(out)
	}
}

// folderOf returns the first arg when it is the single input (for the JSON
// "folder" field), else "" — a best-effort label, not load-bearing.
func folderOf(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return ""
}

// humanLine renders the one concise line per clip (ACCESSIBILITY.md): basename,
// verdict, status tag, and a one-phrase basis. Lead with the answer.
func humanLine(r ClipResult) string {
	base := r.SourceBase
	date := r.VerdictDate
	if date == "" {
		date = "(undated)"
	}
	switch r.Status {
	case string(datetri.StatusDocumented):
		return fmt.Sprintf("%s  ->  %s  [DOCUMENTED, conf %.2f]  (%s)", base, date, r.Confidence, shortBasis(r))
	case string(datetri.StatusConflict):
		return fmt.Sprintf("%s  ->  %s  [CONFLICT]  %s", base, date, r.Basis)
	case string(datetri.StatusCandidate):
		return fmt.Sprintf("%s  ->  %s  [CANDIDATE, conf %.2f]  %s", base, date, r.Confidence, shortBasis(r))
	default: // UNKNOWN
		return fmt.Sprintf("%s  ->  (undated)  [UNKNOWN]  %s", base, shortBasis(r))
	}
}

// shortBasis returns a compact basis phrase for the human line.
func shortBasis(r ClipResult) string {
	b := r.Basis
	if i := strings.Index(b, " ("); i > 0 {
		b = b[:i]
	}
	return b
}

// pathBase exposes pathx.Base for the cmd layer (Windows-path safe).
func pathBase(p string) string { return pathx.Base(p) }
