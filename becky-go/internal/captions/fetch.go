package captions

// fetch.go is the PRODUCTION implementation of the FetchAutoSubs seam: becky's
// single, explicit, logged network step (the package is otherwise offline +
// deterministic, mirroring becky-scout). It shells out to yt-dlp to pull a
// video's English auto/official subtitles, then GUARANTEES the result is a single
// file at exactly outPath (<video-stem>.en.srt next to the source) — "same naming
// scheme, same folder, no extra folders, no weird locations".
//
// Why download to a temp template and then rename, rather than telling yt-dlp to
// write outPath directly: yt-dlp builds the output name from its OWN output
// template and the language tag it actually downloads (en, en-orig, en-US, a
// machine-translated en, …), so the file it emits is "<template>.<lang>.srt" with
// a language code we can't predict. We therefore point yt-dlp at a private temp
// directory, let it write whatever it wants there, pick the best resulting .srt,
// and move THAT to outPath. The video's real folder only ever receives the final,
// correctly-named <stem>.en.srt — never yt-dlp's scratch.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/proc"
)

// ytdlpBin resolves the yt-dlp executable: BECKY_YTDLP if set, else "yt-dlp" on
// PATH. Matches becky-scout's override knob.
func ytdlpBin() string {
	if b := strings.TrimSpace(os.Getenv("BECKY_YTDLP")); b != "" {
		return b
	}
	return "yt-dlp"
}

// realFetchAutoSubs is the default FetchAutoSubs. It downloads English subtitles
// for video id `id` into a temp dir, then moves the best .srt to outPath. On
// success it returns outPath. On any failure (yt-dlp missing, video private /
// removed, no captions, network down) it returns an error — which Analyze turns
// into a clean "local_needed" Decision, never a crash.
func realFetchAutoSubs(id, outPath string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("no video id")
	}
	if strings.TrimSpace(outPath) == "" {
		return "", fmt.Errorf("no output path")
	}

	// Private temp dir for yt-dlp's scratch, cleaned up unconditionally so no
	// stray folder/file is ever left near the case footage.
	tmp, err := os.MkdirTemp("", "becky-captions-")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	// yt-dlp writes "<id>.<lang>.srt" into tmp. We ask for English (incl. regional
	// and auto variants), srt format, no media download, and convert to srt so VTT
	// sources still land as .srt.
	template := filepath.Join(tmp, "%(id)s.%(ext)s")
	url := "https://www.youtube.com/watch?v=" + id
	args := []string{
		"--skip-download",
		"--write-auto-subs",
		"--write-subs",
		"--sub-langs", "en.*,en",
		"--sub-format", "srt",
		"--convert-subs", "srt",
		"-o", template,
	}
	if extra := strings.TrimSpace(os.Getenv("BECKY_YTDLP_ARGS")); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}
	args = append(args, url)

	cmd := exec.Command(ytdlpBin(), args...)
	proc.NoWindow(cmd) // no console flash when the GUI spawns it
	out, runErr := cmd.CombinedOutput()

	// IMPORTANT: judge success by whether a subtitle actually landed, NOT by
	// yt-dlp's exit code. yt-dlp routinely exits non-zero on benign conditions
	// AFTER writing the subtitle — e.g. the post-processing warning "[Subtitles
	// Convertor] Subtitle file for srt is already in the requested format" (when
	// --convert-subs srt sees an already-srt track) makes it return 1 even though
	// "BNj57_O3cZM.en.srt" was downloaded fine (verified live 2026-06-18). It also
	// exits non-zero when comment scraping gives up. So: if we got an .srt, use it;
	// only if NONE was produced do we treat the run as a failure and surface
	// yt-dlp's message for the degrade note.
	srt := bestSRT(tmp)
	if srt == "" {
		if runErr != nil {
			return "", fmt.Errorf("yt-dlp: %s", lastLine(string(out), runErr))
		}
		// Clean exit but no subtitle — the common "no captions available" case
		// (incl. a video that has only non-English captions).
		return "", fmt.Errorf("no English subtitles available for %s", id)
	}

	if err := moveFile(srt, outPath); err != nil {
		return "", fmt.Errorf("place subtitle at %s: %w", outPath, err)
	}
	return outPath, nil
}

// bestSRT picks the most preferred .srt yt-dlp wrote into dir. yt-dlp may emit
// several (en, en-orig, en-US, en machine-translated). Preference: a plain "en"
// track over a regional/auto/translated one, then shortest name, then lexical —
// all deterministic. Returns "" if the dir holds no .srt.
func bestSRT(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var srts []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".srt") {
			srts = append(srts, e.Name())
		}
	}
	if len(srts) == 0 {
		return ""
	}
	sort.Slice(srts, func(i, j int) bool {
		pi, pj := subLangRank(srts[i]), subLangRank(srts[j])
		if pi != pj {
			return pi < pj
		}
		if len(srts[i]) != len(srts[j]) {
			return len(srts[i]) < len(srts[j])
		}
		return srts[i] < srts[j]
	})
	return filepath.Join(dir, srts[0])
}

// subLangRank ranks a subtitle filename's language flavour: a plain ".en." track
// (manually-authored or straight auto-English) is best (0); anything else
// (regional en-US, en-orig, machine-translated) is 1. The filename here is
// "<id>.<lang>.srt", so the language token is the part between the final two dots.
func subLangRank(name string) int {
	base := strings.TrimSuffix(name, filepath.Ext(name)) // drop ".srt"
	if i := strings.LastIndex(base, "."); i >= 0 {
		lang := strings.ToLower(base[i+1:])
		if lang == "en" {
			return 0
		}
	}
	return 1
}

// moveFile moves src to dst, preferring an atomic rename and falling back to a
// copy+remove when src and dst are on different volumes (os.Rename fails across
// drives on Windows — and yt-dlp's temp dir is typically on C: while footage is
// on E:). dst's directory is assumed to exist (it's the video's own folder).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	_ = os.Remove(src)
	return nil
}

// lastLine returns the last non-empty line of yt-dlp's combined output (its error
// message) for a compact degrade note, falling back to the exec error.
func lastLine(combined string, fallback error) string {
	lines := strings.Split(strings.TrimSpace(combined), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	if fallback != nil {
		return fallback.Error()
	}
	return "unknown error"
}
