package audioengine

import (
	"fmt"

	"becky-go/internal/dawmodel"
)

// render_song.go is the OFFLINE, pure-Go render of a whole arrangement (drums + bass
// + chords + melody) to one mono buffer — no audio device, no cgo, no build tag. It
// is the audible end of becky's pipe (becky-song): intent → arrangement → WAV. It
// mirrors the live PlayPatternAudio path (synth voices for pitched notes, the default
// drum kit for channel-9 hits; sine fallback when no kit samples are on disk) so the
// offline render and the live playback sound the same.

// RenderArrangementBuf renders the arrangement to a mono float32 buffer at sampleRate.
// loops tiles the rendered content for a longer preview (loops<=1 = once). Errors on
// an empty arrangement; degrade-never-crash otherwise.
func RenderArrangementBuf(arr *dawmodel.Arrangement, sampleRate, loops int) ([]float32, error) {
	if arr == nil {
		return nil, fmt.Errorf("render: nil arrangement")
	}
	bpm := arr.BPM
	if bpm <= 0 {
		bpm = 120
	}
	ppq := arr.PPQ
	if ppq <= 0 {
		ppq = 480
	}
	tr, err := NewTransport(float64(bpm), ppq, sampleRate)
	if err != nil {
		return nil, err
	}
	var allNotes []dawmodel.Note
	for _, track := range arr.Tracks {
		for _, c := range track.Clips {
			allNotes = append(allNotes, c.Notes...)
		}
	}
	if len(allNotes) == 0 {
		return nil, fmt.Errorf("render: no MIDI notes in the arrangement")
	}
	evs, err := SequenceNotes(allNotes, tr)
	if err != nil {
		return nil, err
	}
	kit := LoadDefaultDrumKit(sampleRate) // real drum samples when present; nil → sine
	single := RenderScheduleWithKit(evs, sampleRate, DurationSamples(evs, sampleRate), kit)
	normalizePeak(single, 0.89) // gain-stage so summed layers don't clip (~-1 dBFS)
	if loops <= 1 {
		return single, nil
	}
	out := make([]float32, 0, len(single)*loops)
	for i := 0; i < loops; i++ {
		out = append(out, single...)
	}
	return out, nil
}

// RenderArrangementWAV renders an arrangement and writes it to a mono float32 WAV.
// One call = the whole pipe's audible output.
func RenderArrangementWAV(arr *dawmodel.Arrangement, path string, sampleRate, loops int) error {
	buf, err := RenderArrangementBuf(arr, sampleRate, loops)
	if err != nil {
		return err
	}
	if len(buf) == 0 {
		return fmt.Errorf("render: produced no audio")
	}
	return WriteMonoFloat32WAV(path, buf, sampleRate)
}

// normalizePeak scales buf in place so its peak magnitude equals target (e.g. 0.89 ≈
// −1 dBFS), preventing the summed layers from clipping. A silent buffer is left as-is.
func normalizePeak(buf []float32, target float32) {
	var peak float32
	for _, s := range buf {
		if s < 0 {
			s = -s
		}
		if s > peak {
			peak = s
		}
	}
	if peak <= target || peak == 0 {
		return
	}
	g := target / peak
	for i := range buf {
		buf[i] *= g
	}
}
