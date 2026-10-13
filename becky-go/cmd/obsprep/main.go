// becky-obsprep - prep a folder of OBS recordings for editing in VEGAS Pro,
// exactly as Jordan's obs-workflow.md describes (2026-10-12):
//
//  1. the folder's .mp4 files go on the timeline in order (OBS names them by
//     date and time); the first one sets the project's size and frame rate;
//  2. the project is saved in that folder, named after it minus the leading
//     day number ("12_goodbye-youtube-update" -> goodbye-youtube-update.veg);
//  3. OBS writes 1 video + 3 audio streams: mic+desktop mix, desktop, mic. The
//     mix track is deleted; the desktop track too with --no-desktop;
//  4. each file's audio is moved so the clap spike lands on the first frame
//     where his hands come together (clap.go), then video + audio are grouped.
//
// No LLM anywhere. The only model is the sound labeler that confirms the spike
// sounds like a clap. VEGAS is driven through becky-vegas (BeckyObsPrep.cs).
//
// Usage: becky-obsprep <footage folder or one of its .mp4 files> [--no-desktop] [--analyze-only]
// Exit code 0 = project saved; 1 = stopped (the reason is printed in plain words).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"becky-go/internal/config"
)

// fileSync is what was decided for one recording.
type fileSync struct {
	File    string  `json:"file"`
	Spike   float64 `json:"spike_s,omitempty"`       // clap onset in the mic audio
	Contact int     `json:"contact_frame,omitempty"` // first frame the hands are together
	Delay   float64 `json:"audio_delay_s"`           // + = audio moves later
	Found   bool    `json:"clap_found"`
	Note    string  `json:"note,omitempty"`
	// VegasFixMs is how much the audio moved again after hearing the clap in
	// VEGAS's own render (VEGAS decodes the AAC a little differently).
	VegasFixMs float64 `json:"vegas_fix_ms,omitempty"`
}

func main() {
	noDesktop := flag.Bool("no-desktop", false, "also delete the desktop audio track")
	analyzeOnly := flag.Bool("analyze-only", false, "find the claps and print them; do not touch VEGAS")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Println("Usage: becky-obsprep <footage folder> [--no-desktop] [--analyze-only]")
		os.Exit(1)
	}
	if err := run(flag.Arg(0), *noDesktop, *analyzeOnly); err != nil {
		fmt.Println()
		fmt.Println("STOPPED: " + err.Error())
		os.Exit(1)
	}
}

func run(arg string, noDesktop, analyzeOnly bool) error {
	cfg := config.Load()
	arg, _ = filepath.Abs(arg)
	st, err := os.Stat(arg)
	if err != nil {
		return fmt.Errorf("%s does not exist", arg)
	}
	folder, files := arg, []string{arg}
	if st.IsDir() {
		if files, err = recordings(folder); err != nil {
			return err
		}
	} else {
		folder = filepath.Dir(arg) // one file: just that recording
	}
	veg := filepath.Join(folder, projectName(filepath.Base(folder))+".veg")
	if _, err := os.Stat(veg); err == nil && !analyzeOnly {
		return fmt.Errorf("%s already exists - nothing was changed (your project is never overwritten)", filepath.Base(veg))
	}
	work := filepath.Join(os.Getenv("LOCALAPPDATA"), "becky", "obsprep", filepath.Base(folder))
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}

	fmt.Printf("Folder: %s\n", folder)
	fmt.Printf("%d recording(s), in this order:\n", len(files))
	for _, f := range files {
		fmt.Println("   " + filepath.Base(f))
	}
	var syncs []fileSync
	fps := 0.0
	for _, f := range files {
		ff, nAudio, err := probe(cfg.FFprobe, f)
		if err != nil {
			return err
		}
		if nAudio != 3 {
			return fmt.Errorf("%s has %d audio tracks; OBS footage has 3 (mix, desktop, mic)", filepath.Base(f), nAudio)
		}
		if fps == 0 {
			fps = ff
		}
		fmt.Printf("\nLooking for the clap in %s ...\n", filepath.Base(f))
		s := findClap(cfg, f, ff, work)
		if s.Found {
			fmt.Printf("   clap heard at %.3f s; hands meet on frame %d -> audio moves %+.1f ms (%+.2f frames)\n",
				s.Spike, s.Contact, s.Delay*1000, s.Delay*ff)
		} else {
			fmt.Printf("   NO CLAP FOUND (%s) - this file's audio stays as recorded\n", s.Note)
		}
		syncs = append(syncs, s)
	}
	b, _ := json.MarshalIndent(syncs, "", "  ")
	_ = os.WriteFile(filepath.Join(work, "sync.json"), b, 0o644)
	if analyzeOnly {
		fmt.Println(string(b))
		return nil
	}
	return buildInVegas(syncs, veg, work, noDesktop, fps)
}

// recordings lists the folder's .mp4 files in recording order (OBS names
// them by date and time, so name order is time order).
func recordings(folder string) ([]string, error) {
	ents, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".mp4") {
			out = append(out, filepath.Join(folder, e.Name()))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("there is no .mp4 file in %s", folder)
	}
	sort.Strings(out)
	return out, nil
}

var dayPrefix = regexp.MustCompile(`^\d+_`)

// projectName is the folder name minus the date and underscore.
func projectName(folder string) string { return dayPrefix.ReplaceAllString(folder, "") }

// probe returns the video frame rate and how many audio streams the file has.
func probe(ffprobe, file string) (float64, int, error) {
	out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "stream=codec_type,r_frame_rate",
		"-of", "csv=p=0", file).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("could not read %s: %w", filepath.Base(file), err)
	}
	fps, audio := 0.0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(strings.TrimSpace(line), ",")
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "video":
			if fps == 0 {
				fps = ratio(f[1])
			}
		case "audio":
			audio++
		}
	}
	if fps <= 0 {
		return 0, 0, fmt.Errorf("%s has no video", filepath.Base(file))
	}
	return fps, audio, nil
}

func ratio(s string) float64 {
	n, d, ok := strings.Cut(s, "/")
	a, _ := strconv.ParseFloat(n, 64)
	if !ok {
		return a
	}
	b, _ := strconv.ParseFloat(d, 64)
	if b == 0 {
		return 0
	}
	return a / b
}

// findClap tries the sharpest spikes in the mic track until one sounds like a
// clap AND has his hands meeting within 20 frames of it.
func findClap(cfg config.Config, file string, fps float64, work string) fileSync {
	s := fileSync{File: file}
	const mic = 2 // OBS audio stream order: mix, desktop, mic
	x, err := micSamples(cfg.FFmpeg, file, mic)
	if err != nil {
		s.Note = err.Error()
		return s
	}
	sp := findSpikes(x)
	if len(sp) == 0 {
		s.Note = "no sharp spike in the first two minutes of the mic track"
		return s
	}
	labels, err := labelSpikes(cfg, cfg.FFmpeg, file, mic, sp, work)
	if err != nil {
		s.Note = err.Error()
		return s
	}
	var tried []string
	for i, c := range sp {
		if !soundsLikeClap(labels[i]) {
			tried = append(tried, fmt.Sprintf("%.2fs sounds like %q", c.Onset, labels[i].Label))
			continue
		}
		sf := int(math.Round(c.Onset * fps))
		m, err := frameMotion(cfg.FFmpeg, file, max(1, sf-frameWindow-1), sf+frameWindow+6, fps)
		if err != nil {
			tried = append(tried, err.Error())
			continue
		}
		f, ok, why := contactFrame(m, sf)
		if !ok {
			tried = append(tried, fmt.Sprintf("%.2fs: %s", c.Onset, why))
			continue
		}
		s.Found, s.Spike, s.Contact = true, c.Onset, f
		s.Delay = float64(f)/fps - c.Onset
		return s
	}
	s.Note = strings.Join(tried, "; ")
	return s
}
