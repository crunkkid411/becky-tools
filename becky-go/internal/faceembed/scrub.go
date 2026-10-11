package faceembed

import (
	"fmt"
	"math"
	"strconv"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/osintexport"
	"becky-go/internal/pyhelpers"
)

// ScrubResult is the clearest views of one face found by stepping through a
// window of video one frame at a time.
type ScrubResult struct {
	Views  []Face    // best first; Path is the frame file, Index its place in the window
	Times  []float64 // seconds into the video, one per view
	Looked int       // frames actually examined before a clear shot stopped the search
	Total  int       // frames in the window
	Dir    string    // where the window's frames were written
}

// Scrub writes EVERY frame of video from at-window to at+window into dir, then the
// face helper steps ONE frame at a time outward from at (0, +1, -1, +2, -2, ...),
// follows the face at box, and stops at the first clear shot (sharp, straight on,
// big enough). Both face models read every view. fps maps a frame back to its time.
func Scrub(cfg config.Config, video string, at, window, fps float64, rotation int, box []float64, dir, device string, verbose bool) (ScrubResult, error) {
	if fps <= 0 {
		fps = 30
	}
	start := math.Max(0, at-window)
	if err := osintexport.ExtractEveryFrameRotated(cfg.FFmpeg, video, start, at+window-start, dir, 2, rotation); err != nil {
		return ScrubResult{}, err
	}
	script, err := pyhelpers.Materialize("face_embed.py", pyhelpers.FaceEmbed)
	if err != nil {
		return ScrubResult{}, fmt.Errorf("materialize face helper: %w", err)
	}
	args := []string{script, "--scrub-dir", dir, "--scrub-from", strconv.Itoa(int(math.Round((at - start) * fps)))}
	if len(box) == 4 {
		args = append(args, "--target", fmt.Sprintf("%.1f,%.1f,%.1f,%.1f", box[0], box[1], box[2], box[3]))
	}
	res, err := runHelper(cfg, args, device, cfg.FaceSecondModel, verbose)
	if err != nil {
		return ScrubResult{}, err
	}
	out := ScrubResult{Looked: res.Looked, Total: res.Total, Dir: dir}
	for _, r := range res.Faces {
		f := r.face()
		out.Views = append(out.Views, f)
		out.Times = append(out.Times, start+float64(f.Index)/fps)
	}
	beckyio.Logf(verbose, "faceembed: scrubbed %d frame(s) around %.2fs, %d view(s) kept", out.Looked, at, len(out.Views))
	return out, nil
}
