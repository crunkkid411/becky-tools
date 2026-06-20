// render.go — headless rendering of a .kdenlive project via the kdenlive-bundled
// melt.exe, plus melt discovery. This is the only file in the package that execs
// a process. The arg builder (meltArgs) is PURE and unit-tested; Render does the
// exec, the h264_nvenc->libx264 fallback, and a best-effort sanity note.
//
// melt invocation (proven this session):
//
//	melt <proj.kdenlive> -consumer avformat:<out.mp4> vcodec=h264_nvenc acodec=aac
//
// On an nvenc failure (GPU-less box / init error) we retry once with libx264 —
// same result, just slower — and record it in RenderResult.Note. melt opens the
// source videos READ-ONLY; only the output path is written.
package kdenlive

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/proc"
)

// RenderResult is the structured outcome of a headless render.
type RenderResult struct {
	Output string `json:"output"`         // absolute path of the written MP4
	Melt   string `json:"melt"`           // the melt binary used
	Vcodec string `json:"vcodec"`         // the video codec ACTUALLY used (after any fallback)
	Acodec string `json:"acodec"`         // the audio codec requested
	Note   string `json:"note,omitempty"` // degrade/fallback note (e.g. nvenc->libx264)
}

// RenderOptions configures a melt render. Zero values use sane defaults.
type RenderOptions struct {
	Melt    string // path to melt.exe; "" -> FindMelt()
	Vcodec  string // "" -> "h264_nvenc" (then libx264 fallback)
	Acodec  string // "" -> "aac"
	Verbose bool   // stream melt's progress to stderr
}

const (
	defaultVcodec  = "h264_nvenc"
	fallbackVcodec = "libx264"
	defaultAcodec  = "aac"
)

// meltArgs builds the melt argument vector for a project->mp4 render. Pure +
// unit-tested. The consumer spec "avformat:<out>" is one token (melt parses the
// path after the colon); the codec props follow as separate tokens.
func meltArgs(projPath, outPath, vcodec, acodec string) []string {
	if vcodec == "" {
		vcodec = defaultVcodec
	}
	if acodec == "" {
		acodec = defaultAcodec
	}
	return []string{
		projPath,
		"-consumer", "avformat:" + outPath,
		"vcodec=" + vcodec,
		"acodec=" + acodec,
	}
}

// Render renders projPath to outPath via melt. It tries vcodec (default
// h264_nvenc) and, on failure, retries once with libx264, recording the fallback
// in the Note. Returns a typed error if melt is missing or both codecs fail —
// never a panic.
func Render(ctx context.Context, projPath, outPath string, opts RenderOptions) (RenderResult, error) {
	meltBin := opts.Melt
	if meltBin == "" {
		var err error
		meltBin, err = FindMelt()
		if err != nil {
			return RenderResult{}, err
		}
	}
	if _, err := os.Stat(projPath); err != nil {
		return RenderResult{}, fmt.Errorf("kdenlive: project not found: %s", projPath)
	}

	absOut, err := filepath.Abs(outPath)
	if err != nil {
		absOut = outPath
	}

	vcodec := opts.Vcodec
	if vcodec == "" {
		vcodec = defaultVcodec
	}
	acodec := opts.Acodec
	if acodec == "" {
		acodec = defaultAcodec
	}

	runErr := runMelt(ctx, meltBin, opts.Verbose, meltArgs(projPath, absOut, vcodec, acodec))

	var note string
	if runErr != nil && vcodec != fallbackVcodec {
		// Degrade-never-crash: the GPU encoder failed; retry with libx264.
		fbErr := runMelt(ctx, meltBin, opts.Verbose, meltArgs(projPath, absOut, fallbackVcodec, acodec))
		if fbErr == nil {
			note = fmt.Sprintf("%s unavailable (%s); fell back to %s", vcodec, firstLine(runErr), fallbackVcodec)
			vcodec = fallbackVcodec
			runErr = nil
		} else {
			return RenderResult{}, fmt.Errorf("kdenlive: melt render failed with %s (%s) and %s fallback also failed (%s)",
				vcodec, firstLine(runErr), fallbackVcodec, firstLine(fbErr))
		}
	}
	if runErr != nil {
		return RenderResult{}, fmt.Errorf("kdenlive: melt render failed: %w", runErr)
	}
	if fi, statErr := os.Stat(absOut); statErr != nil || fi.Size() == 0 {
		return RenderResult{}, fmt.Errorf("kdenlive: melt reported success but produced no output at %s", absOut)
	}

	return RenderResult{
		Output: absOut,
		Melt:   meltBin,
		Vcodec: vcodec,
		Acodec: acodec,
		Note:   note,
	}, nil
}

// runMelt executes one melt invocation. Output is discarded unless verbose (then
// it streams to the process stderr so a user watching the CLI sees progress).
func runMelt(ctx context.Context, meltBin string, verbose bool, args []string) error {
	cmd := exec.CommandContext(ctx, meltBin, args...)
	proc.NoWindow(cmd)
	if verbose {
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	// Capture combined output so a failure message is useful, but don't spam.
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, firstLine(string(out)))
	}
	return nil
}

// meltCandidates lists where the kdenlive-bundled melt typically lives on this
// machine + the generic name. FindMelt returns the first that exists.
func meltCandidates() []string {
	c := []string{}
	if env := strings.TrimSpace(os.Getenv("BECKY_MELT")); env != "" {
		c = append(c, env)
	}
	c = append(c,
		`C:\Program Files\kdenlive\bin\melt.exe`,
		`C:\Program Files (x86)\kdenlive\bin\melt.exe`,
	)
	return c
}

// FindMelt locates the melt binary: $BECKY_MELT, then the known kdenlive install
// paths, then melt/melt.exe on PATH. Returns a typed error (with the friendly
// "install kdenlive" hint) when none is found — degrade-never-crash.
func FindMelt() (string, error) {
	for _, cand := range meltCandidates() {
		if fileExists(cand) {
			return cand, nil
		}
	}
	for _, name := range []string{"melt", "melt.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("kdenlive: melt not found — install kdenlive (it bundles melt.exe) or set BECKY_MELT to its path")
}

// FindKdenlive locates the kdenlive GUI binary for --open (sibling of melt).
func FindKdenlive() (string, error) {
	if env := strings.TrimSpace(os.Getenv("BECKY_KDENLIVE")); env != "" && fileExists(env) {
		return env, nil
	}
	candidates := []string{
		`C:\Program Files\kdenlive\bin\kdenlive.exe`,
		`C:\Program Files (x86)\kdenlive\bin\kdenlive.exe`,
	}
	for _, cand := range candidates {
		if fileExists(cand) {
			return cand, nil
		}
	}
	for _, name := range []string{"kdenlive", "kdenlive.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("kdenlive: kdenlive.exe not found — install kdenlive or set BECKY_KDENLIVE to its path")
}

// Open launches the kdenlive GUI on the given project for a human to edit. It
// does NOT wait for the editor to close (it detaches). Returns a typed error if
// kdenlive can't be found or fails to start.
func Open(projPath string) error {
	bin, err := FindKdenlive()
	if err != nil {
		return err
	}
	abs, aerr := filepath.Abs(projPath)
	if aerr != nil {
		abs = projPath
	}
	if _, serr := os.Stat(abs); serr != nil {
		return fmt.Errorf("kdenlive: project not found: %s", abs)
	}
	cmd := exec.Command(bin, abs)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("kdenlive: failed to launch kdenlive: %w", err)
	}
	// Detach: let the editor run independently of this CLI process.
	_ = cmd.Process.Release()
	return nil
}

// fileExists reports whether p is an existing file/dir.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// firstLine returns the first non-empty line of v (an error or string), trimmed,
// so render failures surface a useful one-liner instead of a wall of melt output.
func firstLine(v any) string {
	var s string
	switch t := v.(type) {
	case error:
		if t == nil {
			return ""
		}
		s = t.Error()
	case string:
		s = t
	default:
		s = fmt.Sprint(v)
	}
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(strings.TrimRight(ln, "\r"))
		if ln != "" {
			return ln
		}
	}
	return strings.TrimSpace(s)
}
