// Package diagram is becky's ascii-art -> SVG -> PNG -> Show Me page pipeline.
// It gives any dumb local model (or agent) a visual language: draw ascii boxes
// and arrows, hand the text to becky-diagram, get a rendered diagram plus a
// ready-to-view HTML page — one dumb call (AUTOPILOT.md P5).
//
// Two local CLIs already on Jordan's PATH do the real work, chained:
//  1. svgbob_cli (ivanceras/svgbob, cargo install) turns ascii text into SVG.
//  2. rsvg-convert (librsvg, ships with the MSYS2 mingw64 toolchain) rasterizes
//     the SVG to PNG.
//
// This package only assembles argv, chains the two calls, and writes the HTML
// wrapper; the rendering itself lives in those two binaries. The pipeline was
// hand-proven once at data\showme\svgbob-test in hj-mission-control; this is
// its deterministic, one-dumb-call form.
//
// becky-shaped: OFFLINE (two local .exe calls, no network), DETERMINISTIC (the
// same ascii text + options -> byte-identical SVG/PNG — both CLIs are pure
// renderers with no RNG), DEGRADE-NEVER-CRASH (a missing svgbob_cli or
// rsvg-convert becomes a typed Result{Degraded:true} with a plain message,
// never a panic; the tool still exits 0).
package diagram

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"becky-go/internal/proc"
)

// ToolName is the stable identifier emitted in every Result.
const ToolName = "becky-diagram"

// Defaults a caller can override via flags/env.
const (
	// DefaultSvgbobBin / DefaultRsvgBin are bare names resolved via PATH — both
	// binaries are already installed system-wide on Jordan's PC (cargo bin +
	// MSYS2 mingw64 bin), matching every other becky tool's "no hardcoded
	// absolute path" invariant.
	DefaultSvgbobBin = "svgbob_cli"
	DefaultRsvgBin   = "rsvg-convert"
	// DefaultOutDir is the current directory — the caller (an autopilot tick,
	// Whoretana, another becky tool) passes --out to point at a real Show Me
	// directory; this package stays decoupled from hj-mission-control's paths.
	DefaultOutDir = "."
)

// Env var fallbacks used when a flag is not provided.
const (
	EnvSvgbobBin = "BECKY_SVGBOB_BIN"
	EnvRsvgBin   = "BECKY_RSVG_BIN"
)

// Options configures one Generate call. Empty string / zero values mean "use
// the package default".
type Options struct {
	In    string // path to an ascii-art source file (required unless Text is set)
	Text  string // inline ascii-art text (alternative to In)
	Title string // REQUIRED: plain-language title for the Show Me page

	OutDir string // output directory (DefaultOutDir when empty)

	SvgbobBin string // svgbob_cli binary/path (DefaultSvgbobBin when empty)
	RsvgBin   string // rsvg-convert binary/path (DefaultRsvgBin when empty)

	FontSize    int     // svgbob --font-size (svgbob's own default when <= 0)
	StrokeWidth int     // svgbob --stroke-width (svgbob's own default when <= 0)
	Scale       float64 // svgbob --scale (svgbob's own default when <= 0)
}

// Result is the JSON-shaped outcome of one Generate call. On any recoverable
// failure Degraded is true and Error carries a plain-language reason; the
// tool still exits 0 so a pipeline never breaks on a missing binary.
type Result struct {
	Tool       string `json:"tool"`
	Title      string `json:"title"`
	Slug       string `json:"slug"`
	SourcePath string `json:"source_path"`
	SVGPath    string `json:"svg_path"`
	PNGPath    string `json:"png_path"`
	HTMLPath   string `json:"html_path"`
	Degraded   bool   `json:"degraded"`
	Error      string `json:"error,omitempty"`
}

// runner abstracts the two external commands so tests never spawn a real
// binary.
type runner interface {
	run(bin string, args []string) (stdout string, err error)
}

// execRunner is the production runner: it invokes svgbob_cli/rsvg-convert and
// returns stdout. Stderr is only surfaced on failure.
type execRunner struct{}

func (execRunner) run(bin string, args []string) (string, error) {
	cmd := exec.Command(bin, args...)
	proc.NoWindow(cmd)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, tail(errBuf.String()))
	}
	return out.String(), nil
}

// Generate runs one ascii -> SVG -> PNG -> HTML pipeline and returns a Result.
// It never returns an error or panics — every failure is folded into a
// degraded Result. Uses the production execRunner.
func Generate(opts Options) Result {
	return generateWith(execRunner{}, opts)
}

// generateWith is Generate with an injectable runner (the test seam).
func generateWith(r runner, opts Options) Result {
	opts = withDefaults(opts)

	source, err := loadSource(opts)
	if err != nil {
		return degrade(newResult(opts, ""), err)
	}
	if strings.TrimSpace(opts.Title) == "" {
		return degrade(newResult(opts, ""), fmt.Errorf("no --title given (required for the Show Me page)"))
	}

	slug := slugify(opts.Title)
	res := newResult(opts, slug)

	if err := checkBinary(opts.SvgbobBin, "svgbob_cli"); err != nil {
		return degrade(res, err)
	}
	if err := checkBinary(opts.RsvgBin, "rsvg-convert"); err != nil {
		return degrade(res, err)
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return degrade(res, fmt.Errorf("creating --out %s: %w", opts.OutDir, err))
	}

	if err := os.WriteFile(res.SourcePath, []byte(source), 0o644); err != nil {
		return degrade(res, fmt.Errorf("writing ascii source: %w", err))
	}

	svgbobArgs := BuildSvgbobArgs(opts, res.SourcePath, res.SVGPath)
	if _, err := r.run(opts.SvgbobBin, svgbobArgs); err != nil {
		return degrade(res, fmt.Errorf("svgbob_cli failed: %w", err))
	}
	if _, err := os.Stat(res.SVGPath); err != nil {
		return degrade(res, fmt.Errorf("svgbob_cli ran but produced no SVG at %s", res.SVGPath))
	}

	rsvgArgs := BuildRsvgArgs(res.SVGPath, res.PNGPath)
	if _, err := r.run(opts.RsvgBin, rsvgArgs); err != nil {
		return degrade(res, fmt.Errorf("rsvg-convert failed: %w", err))
	}
	if _, err := os.Stat(res.PNGPath); err != nil {
		return degrade(res, fmt.Errorf("rsvg-convert ran but produced no PNG at %s", res.PNGPath))
	}

	htmlBody := RenderHTML(opts.Title, source, filepath.Base(res.SVGPath), filepath.Base(res.PNGPath))
	if err := os.WriteFile(res.HTMLPath, []byte(htmlBody), 0o644); err != nil {
		return degrade(res, fmt.Errorf("writing Show Me page: %w", err))
	}

	return res
}

// Plan resolves defaults and slug WITHOUT running anything or touching the
// filesystem. It is the basis of --selftest.
func Plan(opts Options) Result {
	opts = withDefaults(opts)
	return newResult(opts, slugify(opts.Title))
}

func newResult(o Options, slug string) Result {
	res := Result{Tool: ToolName, Title: o.Title, Slug: slug}
	if slug != "" {
		res.SourcePath = filepath.Join(o.OutDir, slug+".txt")
		res.SVGPath = filepath.Join(o.OutDir, slug+".svg")
		res.PNGPath = filepath.Join(o.OutDir, slug+".png")
		res.HTMLPath = filepath.Join(o.OutDir, "index.html")
	}
	return res
}

// loadSource returns the ascii-art text: inline Text wins, else read In.
func loadSource(o Options) (string, error) {
	if strings.TrimSpace(o.Text) != "" {
		return o.Text, nil
	}
	if strings.TrimSpace(o.In) == "" {
		return "", fmt.Errorf("no --in file or inline text given")
	}
	b, err := os.ReadFile(o.In)
	if err != nil {
		return "", fmt.Errorf("reading --in %s: %w", o.In, err)
	}
	return string(b), nil
}

// withDefaults fills unset Options from package defaults + env fallbacks.
// Flags (already in opts) win; then env; then the hardcoded default.
func withDefaults(o Options) Options {
	o.SvgbobBin = firstNonEmpty(o.SvgbobBin, os.Getenv(EnvSvgbobBin), DefaultSvgbobBin)
	o.RsvgBin = firstNonEmpty(o.RsvgBin, os.Getenv(EnvRsvgBin), DefaultRsvgBin)
	o.OutDir = firstNonEmpty(o.OutDir, DefaultOutDir)
	return o
}

// BuildSvgbobArgs assembles the svgbob_cli argv for one ascii -> SVG render.
// Pure function, no side effects — the basis of --selftest's offline proof.
func BuildSvgbobArgs(o Options, srcPath, svgPath string) []string {
	args := []string{srcPath, "-o", svgPath}
	if o.FontSize > 0 {
		args = append(args, "--font-size", strconv.Itoa(o.FontSize))
	}
	if o.StrokeWidth > 0 {
		args = append(args, "--stroke-width", strconv.Itoa(o.StrokeWidth))
	}
	if o.Scale > 0 {
		args = append(args, "--scale", trimFloat(o.Scale))
	}
	return args
}

// BuildRsvgArgs assembles the rsvg-convert argv for one SVG -> PNG render.
func BuildRsvgArgs(svgPath, pngPath string) []string {
	return []string{svgPath, "-o", pngPath}
}

// checkBinary confirms a binary is resolvable before it's run, so a missing
// tool becomes a precise degrade note instead of an exec failure. A path with
// a separator is checked with os.Stat; a bare name is resolved via PATH.
func checkBinary(bin, what string) error {
	if strings.ContainsAny(bin, `/\`) {
		if _, err := os.Stat(bin); err != nil {
			return fmt.Errorf("%s not found at %s", what, bin)
		}
		return nil
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%s (%q) not found on PATH — install it or set the env override", what, bin)
	}
	return nil
}

// slugify turns a title into a filesystem-safe, lowercase, dash-separated
// basename. Empty/all-punctuation input falls back to "diagram".
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		return "diagram"
	}
	return out
}

// RenderHTML builds the Show Me page body for one diagram. Pure function
// (no I/O) so it is directly unit-testable and reused by --selftest.
//
// Every font-size in this template is >= 28px (>= 40px for the title) per
// AUTOPILOT.md Law 4 — data\showme\*\index.html readability is enforced by
// tools\audit_truth.py, which flags ANY font-size below 28px in the file.
func RenderHTML(title, source, svgFile, pngFile string) string {
	return fmt.Sprintf(`<div style="background:#05070a;color:#f0f0f0;font-family:Segoe UI,Arial,sans-serif;padding:30px">
<h1 style="color:#14FF39;font-size:48px;margin:0 0 20px 0">%s</h1>
<div style="font-size:29px;color:#9fb0c0;margin-bottom:24px">Rendered by becky-diagram: ascii-art &rarr; svgbob &rarr; PNG. One dumb call.</div>

<div style="border:3px solid #14FF39;padding:20px;margin-bottom:24px;background:#0a0c10">
<img src="%s" style="max-width:100%%;height:auto;display:block;margin:0 auto" alt="%s">
</div>

<details style="margin-bottom:20px">
<summary style="font-size:30px;color:#FFD700;cursor:pointer">ASCII SOURCE</summary>
<pre style="font-size:28px;line-height:1.3;color:#cfe;background:#0a0c10;border:2px solid #14FF39;padding:16px;overflow-x:auto;margin-top:12px">%s</pre>
</details>

<div style="font-size:28px;color:#9a9a9a">SVG: %s &nbsp;|&nbsp; PNG: %s</div>
</div>
`, html.EscapeString(title), pngFile, html.EscapeString(title), html.EscapeString(source), svgFile, pngFile)
}

// degrade folds an error into a degraded Result (never returns the error).
func degrade(res Result, err error) Result {
	res.Degraded = true
	res.Error = err.Error()
	return res
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// tail trims a CLI's stderr to its last 500 chars for error context.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[len(s)-500:]
	}
	return s
}
