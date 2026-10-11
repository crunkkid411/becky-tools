// becky-revsearch - reverse image search for a person: is this face anywhere on the web,
// and do the pages that show it say who it is?
//
//	becky-revsearch <image> [--names "Name|alias,alias;Other Name"] [--out dir]
//	                [--face face.json] [--engines yandex,bing] [--device cpu] [--show] [--verbose]
//
// It uploads the picture to Yandex and Bing in the agent browser's stealth Firefox
// (Camoufox, hidden, no login; Google blocks automated uploads), saves every result
// thumbnail with the page and words around it, then BOTH face models (ArcFace and
// LVFace) compare each thumbnail's face with the face in <image>. Only results where
// both models say "same face" count; a search engine's "similar" pictures are mostly
// look-alikes and are never treated as a match on their own.
//
// --names lists the people to look for in the matching pages' titles and links
// (each "Name|alias,alias"; people separated by ";"). becky-identify passes its
// candidate here before it ever lists a face for Jordan.
//
// This tool goes online by design: it sends the picture to Yandex and Bing.
// JSON on stdout; exit 0 even when a site blocks it (the note says which).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"unicode"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/faceembed"
	"becky-go/internal/proc"
	"becky-go/internal/pyhelpers"
)

// The naming bars becky-identify uses for both face models (research/kb-final-face-voice-check).
const (
	arcSame = 0.55
	lvSame  = 0.30
	arcNear = 0.40
	lvNear  = 0.15
)

type Engine struct {
	Engine string `json:"engine"`
	OK     bool   `json:"ok"`
	Note   string `json:"note,omitempty"`
	Count  int    `json:"count"`
}

type Result struct {
	Engine  string   `json:"engine"`
	Tab     string   `json:"tab,omitempty"`
	Page    string   `json:"page,omitempty"`
	Text    string   `json:"text,omitempty"`
	Image   string   `json:"image"`
	ArcFace float64  `json:"arcface"`
	LVFace  float64  `json:"lvface,omitempty"`
	Names   []string `json:"names,omitempty"` // the --names people this page mentions
}

type Output struct {
	Image         string         `json:"image"`
	Engines       []Engine       `json:"engines"`
	Thumbnails    int            `json:"thumbnails"`
	FacesCompared int            `json:"faces_compared"`
	SameFace      []Result       `json:"same_face"`  // both face models: same person
	NearFace      []Result       `json:"near_face"`  // close, not enough to say same
	NamesSeen     map[string]int `json:"names_seen"` // --names person -> same-face pages naming them
	Summary       string         `json:"summary"`
}

type helperOut struct {
	Skipped bool     `json:"skipped"`
	Reason  string   `json:"reason"`
	Engines []Engine `json:"engines"`
	Results []struct {
		Engine, Tab, Image, Page, Text string
	} `json:"results"`
}

func main() {
	names := flag.String("names", "", `people to look for in matching pages: "Name|alias,alias;Other"`)
	out := flag.String("out", "", "folder for the result thumbnails (default: a temp folder)")
	engines := flag.String("engines", "yandex,bing", "search engines to ask")
	device := flag.String("device", "", "face model device: cpu, cuda, dml (default from config)")
	show := flag.Bool("show", false, "show the browser window")
	faceFile := flag.String("face", "", `JSON {"vector": [...], "vector2": [...]}: the face to compare with, when the caller already read it (default: the face in <image>)`)
	verbose := flag.Bool("verbose", false, "progress on stderr")
	flag.Parse()
	rest := flag.Args()
	if len(rest) > 1 { // flags after the image
		_ = flag.CommandLine.Parse(rest[1:])
	}
	if len(rest) == 0 || flag.NArg() > 0 && len(rest) > 1 {
		beckyio.Fatalf("usage: becky-revsearch <image> [--names ...] [--out dir]")
	}
	img := rest[0]
	if _, err := os.Stat(img); err != nil {
		beckyio.Fatalf("image not found: %s", img)
	}
	cfg := config.Load()
	dev := *device
	if dev == "" {
		dev = cfg.Device
	}
	dir := *out
	if dir == "" {
		d, err := os.MkdirTemp("", "becky_revsearch_")
		if err != nil {
			beckyio.Fatalf("%v", err)
		}
		dir = d
	}
	var known *faceembed.Face
	if *faceFile != "" {
		var v struct{ Vector, Vector2 []float64 }
		b, err := os.ReadFile(*faceFile)
		if err != nil || json.Unmarshal(b, &v) != nil || len(v.Vector) == 0 {
			beckyio.Fatalf("cannot read --face %s", *faceFile)
		}
		known = &faceembed.Face{Found: true, Vector: v.Vector, Vector2: v.Vector2}
	}
	o := run(cfg, img, dir, *engines, dev, parseNames(*names), known, *show, *verbose)
	b, _ := json.MarshalIndent(o, "", "  ")
	fmt.Println(string(b))
}

func run(cfg config.Config, img, dir, engines, device string, people map[string][]string, known *faceembed.Face, show, verbose bool) Output {
	o := Output{Image: img, NamesSeen: map[string]int{}, SameFace: []Result{}, NearFace: []Result{}}
	h, err := search(cfg, img, dir, engines, show, verbose)
	if err != nil {
		o.Summary = "reverse image search could not run: " + err.Error()
		return o
	}
	o.Engines, o.Thumbnails = h.Engines, len(h.Results)
	if len(h.Results) == 0 {
		o.Summary = "the search engines returned no pictures"
		return o
	}
	paths := []string{img}
	for _, r := range h.Results {
		paths = append(paths, r.Image)
	}
	beckyio.Logf(verbose, "revsearch: comparing %d result picture(s) with both face models...", len(h.Results))
	recs, err := faceembed.EmbedBoth(cfg, paths, device, verbose)
	if err != nil || len(recs) != len(paths) {
		o.Summary = fmt.Sprintf("face models could not read the results: %v", err)
		return o
	}
	q := recs[0]
	if known != nil {
		q = *known
	}
	if !q.Found {
		o.Summary = "no face found in the picture searched for"
		return o
	}
	for i, r := range h.Results {
		f := recs[i+1]
		if !f.Found {
			continue
		}
		o.FacesCompared++
		res := Result{Engine: r.Engine, Tab: r.Tab, Page: r.Page, Text: r.Text, Image: r.Image,
			ArcFace: round(dot(q.Vector, f.Vector))}
		hasLV := len(q.Vector2) > 0 && len(f.Vector2) > 0
		if hasLV {
			res.LVFace = round(dot(q.Vector2, f.Vector2))
		}
		switch verdict(res.ArcFace, res.LVFace, hasLV) {
		case "same":
			res.Names = mentions(r.Text+" "+r.Page, people)
			for _, n := range res.Names {
				o.NamesSeen[n]++
			}
			o.SameFace = append(o.SameFace, res)
		case "near":
			res.Names = mentions(r.Text+" "+r.Page, people)
			o.NearFace = append(o.NearFace, res)
		}
	}
	sort.Slice(o.SameFace, func(i, j int) bool { return o.SameFace[i].ArcFace > o.SameFace[j].ArcFace })
	sort.Slice(o.NearFace, func(i, j int) bool { return o.NearFace[i].ArcFace > o.NearFace[j].ArcFace })
	o.Summary = summary(o)
	return o
}

// verdict is what both face models say about one result face (pure; unit-tested).
func verdict(arc, lv float64, hasLV bool) string {
	switch {
	case arc >= arcSame && (!hasLV || lv >= lvSame):
		return "same"
	case arc >= arcNear || (hasLV && lv >= lvNear):
		return "near"
	}
	return "different"
}

func summary(o Output) string {
	var ok []string
	for _, e := range o.Engines {
		if e.OK {
			ok = append(ok, e.Engine)
		}
	}
	s := fmt.Sprintf("searched %s; %d result picture(s), %d with a face; %d show the same face", strings.Join(ok, " + "),
		o.Thumbnails, o.FacesCompared, len(o.SameFace))
	for n, c := range o.NamesSeen {
		s += fmt.Sprintf("; %d of those pages name %s", c, n)
	}
	return s
}

// mentions returns the people whose name or an alias appears in text (letters and
// digits only, any case, so "John-Clancy" in a link matches "John Clancy").
func mentions(text string, people map[string][]string) []string {
	hay := squash(text)
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		words[w] = true
	}
	var out []string
	for name, aliases := range people {
		for _, a := range append([]string{name}, aliases...) {
			k := squash(a)
			// a short alias ("Shel") only counts as a whole word, never inside "shell"
			if len(k) >= 6 && strings.Contains(hay, k) || len(k) >= 3 && words[k] {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// parseNames reads "Name|alias,alias;Other".
func parseNames(s string) map[string][]string {
	out := map[string][]string{}
	for _, person := range strings.Split(s, ";") {
		name, aliases, _ := strings.Cut(person, "|")
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		for _, a := range strings.Split(aliases, ",") {
			if a = strings.TrimSpace(a); a != "" {
				out[name] = append(out[name], a)
			}
		}
		if _, ok := out[name]; !ok {
			out[name] = nil
		}
	}
	return out
}

func search(cfg config.Config, img, dir, engines string, show, verbose bool) (helperOut, error) {
	if cfg.RevSearchPython == "" {
		return helperOut{}, fmt.Errorf("the agent browser (Camoufox) is not configured")
	}
	if _, err := os.Stat(cfg.RevSearchPython); err != nil {
		return helperOut{}, fmt.Errorf("the agent browser (Camoufox) is not installed at %s", cfg.RevSearchPython)
	}
	script, err := pyhelpers.Materialize("reverse_search.py", pyhelpers.ReverseSearch)
	if err != nil {
		return helperOut{}, err
	}
	args := []string{"-I", script, img, "--out", dir, "--engines", engines}
	if show {
		args = append(args, "--show")
	}
	beckyio.Logf(verbose, "revsearch: asking %s (about a minute)...", engines)
	cmd := exec.Command(cfg.RevSearchPython, args...)
	proc.NoWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil && stdout.Len() == 0 {
		return helperOut{}, fmt.Errorf("browser helper failed: %v %s", err, lastLine(stderr.String()))
	}
	var h helperOut
	if err := json.Unmarshal([]byte(lastLine(stdout.String())), &h); err != nil {
		return helperOut{}, fmt.Errorf("could not read the browser helper's answer")
	}
	if h.Skipped {
		return helperOut{}, fmt.Errorf("%s", h.Reason)
	}
	return h, nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func dot(a, b []float64) float64 {
	var s, na, nb float64
	for i := range a {
		if i < len(b) {
			s += a[i] * b[i]
			na += a[i] * a[i]
			nb += b[i] * b[i]
		}
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return s / math.Sqrt(na*nb)
}

func round(f float64) float64 { return math.Round(f*1000) / 1000 }
