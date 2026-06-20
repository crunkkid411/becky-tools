package audiohost

import (
	"os"
	"path/filepath"
	"runtime"
)

// exeName is the host binary's basename per platform.
func exeName() string {
	if runtime.GOOS == "windows" {
		return "becky-audio-host.exe"
	}
	return "becky-audio-host"
}

// ResolveHost finds the becky-audio-host executable, in priority order:
//
//  1. $BECKY_AUDIO_HOST (explicit override; used as-is if it exists).
//  2. Next to the running binary (the deployed layout: all becky exes co-located).
//  3. The repo's native build output: <repo>/native/audio-host/build/becky-audio-host(.exe),
//     discovered by walking up from the running binary and from the cwd.
//
// It returns the resolved path and true, or "" and false if none exists. It
// never panics and never executes anything — pure filesystem lookup.
func ResolveHost() (string, bool) {
	path, _ := resolveHostVerbose()
	return path, path != ""
}

// resolveHostVerbose returns the resolved path (or "") and the list of
// candidate locations that were searched (for the NotFoundError message).
func resolveHostVerbose() (string, []string) {
	var searched []string
	check := func(p string) (string, bool) {
		if p == "" {
			return "", false
		}
		searched = append(searched, p)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
		return "", false
	}

	// 1. Explicit override.
	if env := os.Getenv("BECKY_AUDIO_HOST"); env != "" {
		if p, ok := check(env); ok {
			return p, searched
		}
	}

	name := exeName()

	// 2. Next to the running binary.
	if self, err := os.Executable(); err == nil {
		if p, ok := check(filepath.Join(filepath.Dir(self), name)); ok {
			return p, searched
		}
	}

	// 3. The native build output. Search from the running binary's dir and from
	// the cwd, walking up to find a "native/audio-host/build/<exe>".
	var roots []string
	if self, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(self))
	}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, cwd)
	}
	rel := filepath.Join("native", "audio-host", "build", name)
	for _, root := range roots {
		for _, base := range ancestors(root) {
			if p, ok := check(filepath.Join(base, rel)); ok {
				return p, searched
			}
		}
	}

	return "", searched
}

// ancestors returns dir and each of its parent directories, up to the
// filesystem root. The walk is bounded so a degenerate path cannot loop.
func ancestors(dir string) []string {
	var out []string
	cur := filepath.Clean(dir)
	for i := 0; i < 64; i++ {
		out = append(out, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return out
}
