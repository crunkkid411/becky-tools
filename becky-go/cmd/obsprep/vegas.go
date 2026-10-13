package main

// vegas.go - build the project in VEGAS through becky-vegas (the same verbs
// becky-livestream uses): launch, new_project (refuses over unsaved work),
// BeckyObsPrep.cs, save, then read the timeline back to prove the audio sits
// where it was meant to.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var vegasPID int

func beckyBin(name string) (string, error) {
	if self, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(self), name+".exe")
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%s was not found - run build-all-tools.bat", name)
}

func vegasScripts() string {
	if v := strings.TrimSpace(os.Getenv("BECKY_VEGAS_SCRIPTS")); v != "" {
		return v
	}
	return `X:\AI-2\becky-tools\vegas`
}

func vegas(timeout time.Duration, args ...string) (json.RawMessage, error) {
	bin, err := beckyBin("becky-vegas")
	if err != nil {
		return nil, err
	}
	full := []string{"--timeout", timeout.String()}
	if vegasPID > 0 {
		full = append(full, "--pid", strconv.Itoa(vegasPID))
	}
	out, runErr := exec.Command(bin, append(full, args...)...).Output()
	var r struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(string(out))), &r) != nil {
		return nil, fmt.Errorf("VEGAS did not answer %s (%v)", args[0], runErr)
	}
	if !r.OK {
		return nil, fmt.Errorf("VEGAS refused %s: %s", args[0], r.Error)
	}
	return r.Result, nil
}

func buildInVegas(syncs []fileSync, veg, work string, noDesktop bool, fps float64) error {
	fmt.Println("\nOpening VEGAS Pro (or using the one already open)...")
	res, err := vegas(6*time.Minute, "launch")
	if err != nil {
		return err
	}
	var st struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(res, &st) == nil && st.PID > 0 {
		vegasPID = st.PID
	}
	if _, err := vegas(2*time.Minute, "new_project"); err != nil {
		return fmt.Errorf("%v (save or close the project that is open in VEGAS, then run this again)", err)
	}
	fmt.Println("Laying out the footage and lining up the audio...")
	out, err := obsJob(work, "build", syncs, noDesktop, "")
	if err != nil {
		return err
	}
	if err := syncInVegas(syncs, work, fps); err != nil {
		return err
	}
	if _, err := vegas(2*time.Minute, "save", "path="+veg); err != nil {
		return err
	}
	if err := verifyTimeline(syncs, fps); err != nil {
		return err
	}
	fmt.Printf("\nDone. Saved %s\n", veg)
	fmt.Println(strings.TrimSpace(string(out)))
	return nil
}

// obsJob runs BeckyObsPrep.cs in one mode and returns its "ok ..." line.
func obsJob(work, mode string, syncs []fileSync, noDesktop bool, extra string) (string, error) {
	var sb strings.Builder
	drop := "0"
	if noDesktop {
		drop = "1"
	}
	fmt.Fprintf(&sb, "mode\t%s\ndrop_desktop\t%s\n", mode, drop)
	for _, s := range syncs {
		fmt.Fprintf(&sb, "media\t%s\t%s\n", s.File, strconv.FormatFloat(s.Delay, 'f', 7, 64))
	}
	sb.WriteString(extra)
	job := filepath.Join(work, "vegas-"+mode+".txt")
	if err := os.WriteFile(job, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	_ = os.Remove(job + ".result.txt")
	if _, err := vegas(10*time.Minute, "run_script", "path="+filepath.Join(vegasScripts(), "BeckyObsPrep.cs"), "job="+job); err != nil {
		return "", err
	}
	out, _ := os.ReadFile(job + ".result.txt")
	if !strings.HasPrefix(string(out), "ok") {
		return "", fmt.Errorf("the VEGAS script (%s) did not finish: %s", mode, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// syncInVegas hears where VEGAS itself plays each clap (a mic-only render) and
// moves the audio until the spike sits on the contact frame. VEGAS decodes
// OBS's AAC ~43 ms earlier than ffmpeg (measured 2026-10-12), so the delay
// worked out from ffmpeg alone lands the clap ~1.3 frames early.
func syncInVegas(syncs []fileSync, work string, fps float64) error {
	for round := 0; round < 3; round++ {
		var probes strings.Builder
		for i, s := range syncs {
			if s.Found {
				fmt.Fprintf(&probes, "probe\t%d\t%d\t%d\t%s\n", i, s.Contact-probeBefore, probeBefore*3, probeWav(work, i))
			}
		}
		if probes.Len() == 0 {
			return nil
		}
		if _, err := obsJob(work, "probe", syncs, false, probes.String()); err != nil {
			return err
		}
		worst := 0.0
		for i := range syncs {
			s := &syncs[i]
			if !s.Found {
				continue
			}
			at, err := spikeInWav(probeWav(work, i))
			if err != nil {
				return fmt.Errorf("could not hear the clap in VEGAS's render: %w", err)
			}
			miss := float64(s.Contact-probeBefore)/fps + at - float64(s.Contact)/fps
			s.VegasFixMs -= miss * 1000
			s.Delay -= miss
			worst = math.Max(worst, math.Abs(miss))
		}
		fmt.Printf("   VEGAS check %d: clap was %.2f ms off the frame at worst\n", round+1, worst*1000)
		if worst < 0.25e-3 {
			return nil
		}
		if _, err := obsJob(work, "adjust", syncs, false, ""); err != nil {
			return err
		}
	}
	return fmt.Errorf("the clap would not settle on its frame in VEGAS")
}

const probeBefore = 30 // frames rendered before the contact frame (the spike is < 20 away)

func probeWav(work string, i int) string { return filepath.Join(work, fmt.Sprintf("probe-%d.wav", i)) }

// spikeInWav finds the clap onset (seconds) in a 16-bit PCM WAV: the loudest
// sample, then the first sample in the 10 ms before it at half its height -
// the same rule as findSpikes.
func spikeInWav(path string) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, fmt.Errorf("%s is not a WAV", path)
	}
	rate, chans, bits := 0, 0, 0
	var data []byte
	for p := 12; p+8 <= len(b); {
		id, n := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:]))
		body := b[p+8 : min(len(b), p+8+n)]
		switch id {
		case "fmt ":
			chans = int(binary.LittleEndian.Uint16(body[2:]))
			rate = int(binary.LittleEndian.Uint32(body[4:]))
			bits = int(binary.LittleEndian.Uint16(body[14:]))
		case "data":
			data = body
		}
		p += 8 + n + n%2
	}
	if bits != 16 || chans < 1 || rate == 0 || len(data) == 0 {
		return 0, fmt.Errorf("%s: unexpected WAV format", path)
	}
	frames := len(data) / (2 * chans)
	amp := func(i int) float64 { return math.Abs(float64(int16(binary.LittleEndian.Uint16(data[i*2*chans:])))) }
	peak, at := 0.0, 0
	for i := 0; i < frames; i++ {
		if a := amp(i); a > peak {
			peak, at = a, i
		}
	}
	if peak < 32768*minPeak {
		return 0, fmt.Errorf("the render is silent")
	}
	for i := max(0, at-rate/100); i <= at; i++ {
		if amp(i) >= 0.5*peak {
			return float64(i) / float64(rate), nil
		}
	}
	return float64(at) / float64(rate), nil
}

// verifyTimeline reads the saved timeline back: for every file, each audio
// event must play its media exactly Delay later than the picture.
func verifyTimeline(syncs []fileSync, fps float64) error {
	raw, err := vegas(2*time.Minute, "timeline")
	if err != nil {
		return err
	}
	var t struct {
		Events []struct {
			Kind     string  `json:"kind"`
			Source   string  `json:"source"`
			In       float64 `json:"in"`
			Timeline float64 `json:"timeline"`
			Grouped  bool    `json:"grouped"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("could not read the timeline back: %w", err)
	}
	for _, s := range syncs {
		var v *float64
		var a []float64
		for _, e := range t.Events {
			if !strings.EqualFold(filepath.Clean(e.Source), filepath.Clean(s.File)) {
				continue
			}
			if !e.Grouped {
				return fmt.Errorf("%s: an event is not grouped", filepath.Base(s.File))
			}
			zero := e.Timeline - e.In // where media time 0 sits on the ruler
			if e.Kind == "video" {
				v = &zero
			} else {
				a = append(a, zero)
			}
		}
		if v == nil || len(a) == 0 {
			return fmt.Errorf("%s is not on the timeline as expected", filepath.Base(s.File))
		}
		for _, z := range a {
			if got := z - *v; math.Abs(got-s.Delay) > 0.0005 {
				return fmt.Errorf("%s: audio sits %.4f s from the picture, meant %.4f s", filepath.Base(s.File), got, s.Delay)
			}
		}
		fmt.Printf("   checked %s: picture + %d audio track(s), audio %+.1f ms, grouped\n", filepath.Base(s.File), len(a), s.Delay*1000)
	}
	return nil
}
