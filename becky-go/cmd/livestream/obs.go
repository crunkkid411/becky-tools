package main

// obs.go - OBS recordings (Jordan's obs-workflow.md, 2026-10-12). An OBS file
// has one picture and three audio streams: mic+desktop mix, desktop, mic. Its
// audio runs a little off the picture until "Prep OBS Footage.bat"
// (becky-obsprep) lines the clap up, and that sync lives in its sync.json.
//
// So the edit never reads the raw file's sound: every audio step (transcript,
// silences, breaths, the edit check) reads <work>\<stem>.synced.mkv - the same
// picture (copied, not re-encoded) with the mic moved by obsprep's delay - and
// VEGAS gets the ORIGINAL file with the mic (and the desktop audio, when it is
// not silent) moved by the delay VEGAS needs, which obsprep measured in VEGAS's
// own render (VEGAS plays OBS's AAC ~43 ms earlier than ffmpeg reads it).

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// vegasAudio is one audio stream VEGAS puts under the picture.
type vegasAudio struct {
	Stream int
	Delay  float64 // seconds the audio plays after the picture, in VEGAS
}

const (
	obsDesktop = 1 // OBS audio stream order: 0 mix, 1 desktop, 2 mic
	obsMic     = 2
	silentDB   = -70.0 // desktop audio quieter than this everywhere is left out
)

// prepOBS switches r to the synced analysis copy when r.media is an OBS
// recording. A file that is not OBS's is left alone.
func (r *run) prepOBS() error {
	n, err := audioStreams(r.cfg.FFprobe, r.footage)
	if err != nil || n < 3 {
		return err
	}
	sync, err := obsSync(r.footage)
	if err != nil {
		return err
	}
	synced := filepath.Join(r.work, r.stem+".synced.mkv")
	if _, err := cached(synced, func() error {
		r.logf("OBS recording: making a copy whose mic is lined up with the picture (%+.1f ms)...", sync.Delay*1000)
		af := fmt.Sprintf("adelay=%.3f:all=1", sync.Delay*1000)
		if sync.Delay < 0 {
			af = fmt.Sprintf("atrim=start=%.6f,asetpts=PTS-STARTPTS", -sync.Delay)
		}
		af += fmt.Sprintf(",atrim=end=%.3f", r.duration)
		out, err := exec.Command(r.cfg.FFmpeg, "-v", "error", "-y", "-i", r.footage, "-map", "0:v:0",
			"-map", fmt.Sprintf("0:a:%d", obsMic), "-c:v", "copy", "-af", af, "-c:a", "flac", synced).CombinedOutput()
		if err != nil {
			_ = os.Remove(synced)
			return fmt.Errorf("could not make the synced copy: %v (%s)", err, lastLines(string(out), 2))
		}
		return nil
	}); err != nil {
		return err
	}
	r.media = synced
	r.audio = []vegasAudio{{obsMic, sync.VegasDelay}}
	if db, err := maxVolume(r.cfg.FFmpeg, r.footage, obsDesktop); err == nil && db > silentDB {
		r.audio = append([]vegasAudio{{obsDesktop, sync.VegasDelay}}, r.audio...)
		r.logf("OBS recording: mic + desktop audio (the desktop audio is not silent, %.0f dB at its loudest)", db)
	} else {
		r.logf("OBS recording: mic only (the desktop audio is silent)")
	}
	return nil
}

type obsFileSync struct {
	File       string  `json:"file"`
	Delay      float64 `json:"audio_delay_s"`
	VegasDelay float64 `json:"vegas_audio_delay_s"`
	Found      bool    `json:"clap_found"`
}

// obsSync reads becky-obsprep's sync for this file. Without it the edit would
// be cut against sound that is off the picture, so it stops and says what to do.
func obsSync(file string) (obsFileSync, error) {
	folder := filepath.Base(filepath.Dir(file))
	path := filepath.Join(os.Getenv("LOCALAPPDATA"), "becky", "obsprep", folder, "sync.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return obsFileSync{}, fmt.Errorf("this is an OBS recording that has not been synced yet - double-click \"Prep OBS Footage.bat\" in its folder first")
	}
	var all []obsFileSync
	if err := json.Unmarshal(b, &all); err != nil {
		return obsFileSync{}, fmt.Errorf("%s: %w", path, err)
	}
	for _, s := range all {
		if strings.EqualFold(filepath.Clean(s.File), filepath.Clean(file)) {
			if s.Found && s.VegasDelay == 0 {
				return s, fmt.Errorf("the sync for this recording is from an older Prep OBS Footage - run it again (move the old .veg aside first)")
			}
			return s, nil
		}
	}
	return obsFileSync{}, fmt.Errorf("Prep OBS Footage has not synced %s yet - run it on its folder first", filepath.Base(file))
}

func audioStreams(ffprobe, file string) (int, error) {
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "a", "-show_entries", "stream=index", "-of", "csv=p=0", file).Output()
	if err != nil {
		return 0, fmt.Errorf("could not read the audio of %s: %v", filepath.Base(file), err)
	}
	return len(strings.Fields(string(out))), nil
}

// maxVolume is the loudest sample of one audio stream, in dB.
func maxVolume(ffmpeg, file string, stream int) (float64, error) {
	out, _ := exec.Command(ffmpeg, "-hide_banner", "-i", file, "-map", fmt.Sprintf("0:a:%d", stream),
		"-af", "volumedetect", "-f", "null", "-").CombinedOutput()
	for _, l := range strings.Split(string(out), "\n") {
		if _, v, ok := strings.Cut(l, "max_volume:"); ok {
			return strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "dB")), 64)
		}
	}
	return 0, fmt.Errorf("no level measured")
}

// audioDelay is how far VEGAS has the audio off its picture (0 unless OBS).
func (r *run) audioDelay() float64 {
	if len(r.audio) == 0 {
		return 0
	}
	return r.audio[0].Delay
}
