package diagram

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// fakeRunner is a test double for svgbob_cli/rsvg-convert — it never spawns a
// real binary. It optionally writes an output file per call so the post-run
// existence checks pass, keyed by call order (svgbob call, then rsvg call).
type fakeRunner struct {
	writeOn   []string // file to create on call N (0-indexed); "" = write nothing
	err       error
	errOnCall int // -1 = never error
	calls     [][]string
}

func (f *fakeRunner) run(bin string, args []string) (string, error) {
	f.calls = append(f.calls, append([]string{bin}, args...))
	n := len(f.calls) - 1
	if n < len(f.writeOn) && f.writeOn[n] != "" {
		_ = os.WriteFile(f.writeOn[n], []byte("x"), 0o644)
	}
	if f.errOnCall == n {
		return "", f.err
	}
	return "", nil
}

func newFakeRunner() *fakeRunner { return &fakeRunner{errOnCall: -1} }

func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// (a) argument construction ---------------------------------------------------

func TestBuildSvgbobArgs_basic(t *testing.T) {
	args := BuildSvgbobArgs(Options{}, "in.txt", "out.svg")
	if args[0] != "in.txt" || argValue(args, "-o") != "out.svg" {
		t.Fatalf("basic args wrong: %v", args)
	}
}

func TestBuildSvgbobArgs_optionalFlagsGatedOff(t *testing.T) {
	args := BuildSvgbobArgs(Options{}, "in.txt", "out.svg")
	for _, f := range []string{"--font-size", "--stroke-width", "--scale"} {
		if argValue(args, f) != "" {
			t.Errorf("did not expect %s by default, got %v", f, args)
		}
	}
}

func TestBuildSvgbobArgs_optionalFlagsOn(t *testing.T) {
	args := BuildSvgbobArgs(Options{FontSize: 16, StrokeWidth: 3, Scale: 1.5}, "in.txt", "out.svg")
	if argValue(args, "--font-size") != "16" {
		t.Errorf("--font-size: got %q", argValue(args, "--font-size"))
	}
	if argValue(args, "--stroke-width") != "3" {
		t.Errorf("--stroke-width: got %q", argValue(args, "--stroke-width"))
	}
	if argValue(args, "--scale") != "1.5" {
		t.Errorf("--scale: got %q", argValue(args, "--scale"))
	}
}

func TestBuildRsvgArgs(t *testing.T) {
	args := BuildRsvgArgs("d.svg", "d.png")
	if args[0] != "d.svg" || argValue(args, "-o") != "d.png" {
		t.Fatalf("rsvg args wrong: %v", args)
	}
}

// (b) slugify -------------------------------------------------------------

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Downtime Engine":      "downtime-engine",
		"  Weird!! Title??  ":  "weird-title",
		"already-slug":         "already-slug",
		"":                     "diagram",
		"### ***":              "diagram",
		"Mixed_Case 123 Title": "mixed-case-123-title",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q want %q", in, got, want)
		}
	}
}

// (c) defaults --------------------------------------------------------------

func TestWithDefaults(t *testing.T) {
	o := withDefaults(Options{})
	if o.SvgbobBin != DefaultSvgbobBin || o.RsvgBin != DefaultRsvgBin || o.OutDir != DefaultOutDir {
		t.Errorf("defaults not applied: %+v", o)
	}
}

func TestWithDefaults_envFallback(t *testing.T) {
	t.Setenv(EnvSvgbobBin, "/env/svgbob")
	t.Setenv(EnvRsvgBin, "/env/rsvg")
	o := withDefaults(Options{})
	if o.SvgbobBin != "/env/svgbob" || o.RsvgBin != "/env/rsvg" {
		t.Errorf("env fallback not applied: %+v", o)
	}
	o2 := withDefaults(Options{SvgbobBin: "/flag/svgbob"})
	if o2.SvgbobBin != "/flag/svgbob" {
		t.Errorf("flag should win over env: got %q", o2.SvgbobBin)
	}
}

// (d) degrade paths -----------------------------------------------------------

func TestGenerate_degradesWhenNoSourceGiven(t *testing.T) {
	res := generateWith(newFakeRunner(), Options{Title: "x"})
	if !res.Degraded || !strings.Contains(res.Error, "no --in file or inline text") {
		t.Fatalf("expected missing-source degrade, got %+v", res)
	}
}

func TestGenerate_degradesWhenTitleMissing(t *testing.T) {
	res := generateWith(newFakeRunner(), Options{Text: "+---+"})
	if !res.Degraded || !strings.Contains(res.Error, "--title") {
		t.Fatalf("expected missing-title degrade, got %+v", res)
	}
}

func TestGenerate_degradesWhenInFileUnreadable(t *testing.T) {
	dir := t.TempDir()
	res := generateWith(newFakeRunner(), Options{
		In: filepath.Join(dir, "nope.txt"), Title: "x", OutDir: dir,
	})
	if !res.Degraded || !strings.Contains(res.Error, "reading --in") {
		t.Fatalf("expected read-failure degrade, got %+v", res)
	}
}

func TestGenerate_degradesWhenSvgbobBinaryMissing(t *testing.T) {
	dir := t.TempDir()
	res := generateWith(newFakeRunner(), Options{
		Text: "+--+", Title: "x", OutDir: dir,
		SvgbobBin: filepath.Join(dir, "no-such-svgbob-binary"),
	})
	if !res.Degraded || !strings.Contains(res.Error, "svgbob_cli not found") {
		t.Fatalf("expected svgbob-missing degrade, got %+v", res)
	}
}

func TestGenerate_degradesWhenSvgbobRunFails(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "svgbob")
	if err := os.WriteFile(bin, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newFakeRunner()
	r.errOnCall = 0
	r.err = errors.New("boom")
	res := generateWith(r, Options{
		Text: "+--+", Title: "x", OutDir: dir,
		SvgbobBin: bin, RsvgBin: bin,
	})
	if !res.Degraded || !strings.Contains(res.Error, "svgbob_cli failed") {
		t.Fatalf("expected svgbob-run-fail degrade, got %+v", res)
	}
}

func TestGenerate_degradesWhenNoSVGProduced(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "svgbob")
	if err := os.WriteFile(bin, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// runner "succeeds" but writes no SVG file.
	res := generateWith(newFakeRunner(), Options{
		Text: "+--+", Title: "x", OutDir: dir, SvgbobBin: bin, RsvgBin: bin,
	})
	if !res.Degraded || !strings.Contains(res.Error, "no SVG") {
		t.Fatalf("expected no-svg degrade, got %+v", res)
	}
}

func TestGenerate_degradesWhenRsvgRunFails(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.WriteFile(bin, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	svgPath := filepath.Join(dir, "x.svg")
	r := newFakeRunner()
	r.writeOn = []string{svgPath} // svgbob call writes the svg
	r.errOnCall = 1               // rsvg call fails
	r.err = errors.New("rsvg boom")
	res := generateWith(r, Options{
		Text: "+--+", Title: "x", OutDir: dir, SvgbobBin: bin, RsvgBin: bin,
	})
	if !res.Degraded || !strings.Contains(res.Error, "rsvg-convert failed") {
		t.Fatalf("expected rsvg-run-fail degrade, got %+v", res)
	}
}

// (e) happy path --------------------------------------------------------------

func TestGenerate_succeedsAndWritesAllFour(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.WriteFile(bin, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")

	r := newFakeRunner()
	// call 0 = svgbob (writes svg), call 1 = rsvg (writes png). Paths are
	// resolved lazily below since they depend on the slug.
	res := generateWithWriter(r, Options{
		Text: "+---+\n| x |\n+---+", Title: "My Diagram!", OutDir: outDir,
		SvgbobBin: bin, RsvgBin: bin,
	})

	if res.Degraded {
		t.Fatalf("unexpected degrade: %+v", res)
	}
	if res.Slug != "my-diagram" {
		t.Errorf("slug: got %q want %q", res.Slug, "my-diagram")
	}
	for _, p := range []string{res.SourcePath, res.SVGPath, res.PNGPath, res.HTMLPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file to exist: %s (%v)", p, err)
		}
	}
	htmlBytes, err := os.ReadFile(res.HTMLPath)
	if err != nil {
		t.Fatal(err)
	}
	htmlStr := string(htmlBytes)
	if !strings.Contains(htmlStr, "My Diagram!") {
		t.Errorf("HTML missing title: %s", htmlStr)
	}
	if bad := regexp.MustCompile(`font-size:\s*(\d+)px`).FindAllStringSubmatch(htmlStr, -1); len(bad) == 0 {
		t.Errorf("expected font-size declarations in HTML")
	} else {
		for _, m := range bad {
			n, _ := strconv.Atoi(m[1])
			if n < 28 {
				t.Errorf("font-size %dpx is below the 28px Law 4 floor", n)
			}
		}
	}
}

// generateWithWriter runs generateWith but pre-registers the svgbob/rsvg
// output paths on the fake runner (they depend on the resolved slug, which
// generateWith computes internally) by running Plan first.
func generateWithWriter(r *fakeRunner, opts Options) Result {
	plan := Plan(opts)
	r.writeOn = []string{plan.SVGPath, plan.PNGPath}
	return generateWith(r, opts)
}

// (f) Plan is side-effect free ------------------------------------------------

func TestPlan_noSideEffects(t *testing.T) {
	dir := t.TempDir()
	res := Plan(Options{Text: "+--+", Title: "Plan Test", OutDir: dir})
	if res.Degraded {
		t.Fatalf("Plan should never degrade: %+v", res)
	}
	if res.Slug != "plan-test" {
		t.Errorf("slug: got %q", res.Slug)
	}
	if _, err := os.Stat(res.SVGPath); !os.IsNotExist(err) {
		t.Errorf("Plan must not create files")
	}
}

// (g) RenderHTML --------------------------------------------------------------

func TestRenderHTML_escapesAndEmbedsPaths(t *testing.T) {
	out := RenderHTML(`<Title & "quotes">`, "src < text", "d.svg", "d.png")
	if strings.Contains(out, "<Title") {
		t.Errorf("title not escaped: %s", out)
	}
	if !strings.Contains(out, "d.svg") || !strings.Contains(out, "d.png") {
		t.Errorf("missing image paths: %s", out)
	}
}
