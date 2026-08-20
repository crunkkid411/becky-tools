// Package pathx provides separator-agnostic path helpers.
//
// becky-tools runs in production on Windows, where paths use '\', but the unit
// tests and CI run on Linux, where the standard library's path/filepath treats
// only '/' as a separator. That mismatch silently breaks any helper that calls
// filepath.Base/Dir on a Windows path while running on Linux (filepath.Base of
// `C:\dir\file.jpg` returns the whole string, not `file.jpg`).
//
// These helpers treat BOTH '/' and '\' as separators regardless of host OS, so
// a display name or basename is derived correctly no matter where the tool runs
// or which platform produced the path. Use them whenever the input path may have
// originated on a different OS than the one currently executing.
package pathx

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Base returns the final element of p, treating both '/' and '\' as separators.
// It returns p unchanged when p contains no separator. Unlike filepath.Base it
// does not collapse "" to "." — an empty input yields "".
func Base(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Dir returns everything before the final separator in p, treating both '/' and
// '\' as separators. It returns "" when p has no separator.
func Dir(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[:i]
	}
	return ""
}

// IsAbs reports whether p is absolute under EITHER OS convention: a rooted
// POSIX path ("/x"), a Windows drive path (`C:\x` or `C:/x`), or a UNC path
// (`\\server\share`). filepath.IsAbs answers only for the HOST OS, so on Linux
// it calls a `X:\...` path relative — which reads as "resolves against the
// cwd" in tests that guard against exactly that bug.
func IsAbs(p string) bool {
	if len(p) >= 1 && (p[0] == '/' || p[0] == '\\') {
		return true
	}
	if len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\') {
		c := p[0]
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	return false
}

// FilesIn lists the files directly inside dir whose names start with prefix and
// end with suffix, sorted by name, as full paths. Either affix may be "".
//
// It exists because filepath.Glob is the wrong tool for a LITERAL directory.
// Glob parses its whole argument as a pattern, so any glob metacharacter in the
// directory part is silently reinterpreted — and `[` is a character class.
//
// This is not theoretical. Jordan's library is almost entirely yt-dlp output,
// which puts the video id in brackets:
//
//	2024-08-30_..._BLINDFOLD_Tasting_[unswA5Jv7fI].mp4
//	Prank Clips_Sony AVC-MVC_BEST 30 FPS 1080[4].mp4
//
// becky-validate caches sampled frames in a directory named after the clip, so
// the glob became `...1080[4]\frame_*.jpg`, where `[4]` matches the single
// character `4`. It looked in `...10804`, found nothing, and reported
// "extracted 0 frame(s)" — after ffmpeg had written all forty of them. Worse,
// when a sibling clip named `...10804` DID exist, the glob matched THAT clip's
// frames and the model was asked about the wrong video.
//
// Reading the directory has no pattern to misparse.
func FilesIn(dir, prefix, suffix string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, suffix) {
			continue
		}
		out = append(out, filepath.Join(dir, n))
	}
	sort.Strings(out)
	return out, nil
}
