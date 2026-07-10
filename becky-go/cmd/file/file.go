// file.go — the pure, testable core of becky-file: shortcut/path resolution,
// the allowed-roots containment check (the security boundary), and every file
// operation. main.go is only flag parsing + wiring, so all the logic with a
// decision in it lives here and can be unit-tested with no real args.
//
// Safety posture (AUTOPILOT.md Law 8b "DELETE NOTHING OF JORDAN'S. EVER." +
// the becky "degrade, never crash" invariant): this tool has NO permanent
// delete and NO bulk auto-move (the Mark source's delete/organize_desktop were
// deliberately dropped when porting). Every write/move/copy REFUSES to clobber
// an existing file rather than overwrite it, unless the caller passes an
// explicit --overwrite (write only). Every path — source AND destination — is
// verified to resolve inside an allowed root before any I/O happens; a path
// that escapes (via .. or a symlink) is denied, not acted on.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/pathx"
)

// Entry is one item in a list/find result.
type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// FileInfo is the stat result for the `info` action.
type FileInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// Result is becky-file's stdout JSON envelope. Exactly one of the payload
// fields is populated per action; OK is false with Error set on any failure.
type Result struct {
	OK        bool      `json:"ok"`
	Action    string    `json:"action,omitempty"`
	Path      string    `json:"path,omitempty"`
	Message   string    `json:"message,omitempty"`
	Entries   []Entry   `json:"entries,omitempty"`
	Content   string    `json:"content,omitempty"`
	Info      *FileInfo `json:"info,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// maxFindDirs caps how many directories a find walk will descend into, so a
// find rooted at a huge tree can't hang the tool (ported from the Mark source's
// same guard). maxFindResults caps returned rows.
const (
	maxFindDirs    = 2000
	maxFindResults = 50
	defReadChars   = 4000
)

// shortcuts maps the plain-language folder names an agent/Whoretana will say
// ("save it to my desktop") to real home-relative directories. Kept
// intentionally home-relative so they always pass the allowed-roots check under
// the default root without special-casing.
func shortcuts() map[string]string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return map[string]string{
		"home":      home,
		"desktop":   filepath.Join(home, "Desktop"),
		"downloads": filepath.Join(home, "Downloads"),
		"documents": filepath.Join(home, "Documents"),
		"pictures":  filepath.Join(home, "Pictures"),
		"music":     filepath.Join(home, "Music"),
		"videos":    filepath.Join(home, "Videos"),
	}
}

// expandShortcut turns a raw path into a concrete filesystem path: a known
// shortcut word, a ~ home prefix, or an as-is path. It does NOT check
// containment — resolveWithinRoots does that.
func expandShortcut(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "desktop"
	}
	if p, ok := shortcuts()[strings.ToLower(trimmed)]; ok {
		return p
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, `~\`) {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(trimmed, "~"), string(os.PathSeparator)))
		}
	}
	return trimmed
}

// allowedRoots returns the directories every operation is confined to. Default
// is the user's home directory (the Mark source's exact _SAFE_ROOTS). It can be
// widened via BECKY_FILE_ROOTS, an OS-path-list-separated list of directories,
// so Whoretana can be granted, e.g., X:\AI-2 explicitly — never implicitly.
func allowedRoots() []string {
	if env := strings.TrimSpace(os.Getenv("BECKY_FILE_ROOTS")); env != "" {
		var roots []string
		for _, r := range strings.Split(env, string(os.PathListSeparator)) {
			if r = strings.TrimSpace(r); r != "" {
				roots = append(roots, r)
			}
		}
		if len(roots) > 0 {
			return roots
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{home}
}

// evalSymlinksBestEffort resolves symlinks in p as far as the path exists on
// disk, then re-appends the not-yet-existing tail. This lets the containment
// check work for a write/move DESTINATION that does not exist yet while still
// resolving any symlink in its existing ancestors — closing the "symlink in a
// parent dir escapes the root" hole that a purely lexical filepath.Clean leaves
// open.
func evalSymlinksBestEffort(p string) string {
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p // reached a volume/filesystem root
	}
	return filepath.Join(evalSymlinksBestEffort(parent), filepath.Base(p))
}

// resolveWithinRoots is the security boundary. It expands raw to an absolute,
// symlink-resolved, lexically-cleaned path and confirms it sits at or under an
// allowed root. Any path that escapes — through .., an absolute path outside
// the roots, or a symlink pointing out — is denied here, before any I/O.
func resolveWithinRoots(raw string) (string, error) {
	abs, err := filepath.Abs(expandShortcut(raw))
	if err != nil {
		return "", fmt.Errorf("cannot resolve path %q: %w", raw, err)
	}
	real := evalSymlinksBestEffort(abs)

	roots := allowedRoots()
	if len(roots) == 0 {
		return "", fmt.Errorf("no allowed roots configured (could not determine home dir; set BECKY_FILE_ROOTS)")
	}
	for _, root := range roots {
		rootAbs, err := filepath.Abs(expandShortcut(root))
		if err != nil {
			continue
		}
		rootReal := evalSymlinksBestEffort(rootAbs)
		if real == rootReal || strings.HasPrefix(real, rootReal+string(os.PathSeparator)) {
			return real, nil
		}
	}
	// Report the denial with the basename only — never leak the full resolved
	// absolute path of somewhere outside the sandbox.
	return "", fmt.Errorf("access denied: %q is outside the allowed roots (set BECKY_FILE_ROOTS to widen)", pathx.Base(real))
}

// joinNameWithin resolves a base path within roots and, if name is non-empty,
// joins name onto it and re-checks containment (so name can't be "../escape").
func joinNameWithin(base, name string) (string, error) {
	basePath, err := resolveWithinRoots(base)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return basePath, nil
	}
	// Re-run the full containment check on the joined path so a name containing
	// separators or .. cannot escape.
	return resolveWithinRoots(filepath.Join(basePath, name))
}

// --- operations -----------------------------------------------------------

func doList(path string, showHidden bool) Result {
	target, err := resolveWithinRoots(path)
	if err != nil {
		return failResult("list", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return failResult("list", fmt.Errorf("path not found: %s", pathx.Base(target)))
	}
	if !info.IsDir() {
		return failResult("list", fmt.Errorf("not a directory: %s", pathx.Base(target)))
	}
	dirents, err := os.ReadDir(target)
	if err != nil {
		return failResult("list", fmt.Errorf("cannot read directory: %v", err))
	}
	entries := make([]Entry, 0, len(dirents))
	for _, de := range dirents {
		if !showHidden && strings.HasPrefix(de.Name(), ".") {
			continue
		}
		var size int64
		if fi, err := de.Info(); err == nil {
			size = fi.Size()
		}
		entries = append(entries, Entry{
			Name:  de.Name(),
			Path:  filepath.Join(target, de.Name()),
			IsDir: de.IsDir(),
			Size:  size,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return Result{OK: true, Action: "list", Path: target, Entries: entries,
		Message: fmt.Sprintf("%d item(s) in %s", len(entries), pathx.Base(target))}
}

func doRead(path, name string, maxChars int) Result {
	target, err := joinNameWithin(path, name)
	if err != nil {
		return failResult("read", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return failResult("read", fmt.Errorf("file not found: %s", pathx.Base(target)))
	}
	if info.IsDir() {
		return failResult("read", fmt.Errorf("not a file: %s", pathx.Base(target)))
	}
	if maxChars <= 0 {
		maxChars = defReadChars
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return failResult("read", fmt.Errorf("cannot read file: %v", err))
	}
	content := string(raw)
	truncated := false
	if len(content) > maxChars {
		content = content[:maxChars]
		truncated = true
	}
	return Result{OK: true, Action: "read", Path: target, Content: content, Truncated: truncated,
		Message: fmt.Sprintf("read %s", pathx.Base(target))}
}

func doWrite(path, name, content string, appendMode, overwrite bool) Result {
	target, err := joinNameWithin(path, name)
	if err != nil {
		return failResult("write", err)
	}
	if info, statErr := os.Stat(target); statErr == nil {
		if info.IsDir() {
			return failResult("write", fmt.Errorf("cannot write: %s is a directory", pathx.Base(target)))
		}
		// Refuse to clobber existing data unless explicitly told to (Law 8b).
		if !appendMode && !overwrite {
			return failResult("write", fmt.Errorf("refusing to overwrite existing file %s (pass --overwrite or --append)", pathx.Base(target)))
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return failResult("write", fmt.Errorf("cannot create parent directory: %v", err))
	}
	flag := os.O_CREATE | os.O_WRONLY
	verb := "wrote"
	if appendMode {
		flag |= os.O_APPEND
		verb = "appended to"
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(target, flag, 0o644)
	if err != nil {
		return failResult("write", fmt.Errorf("cannot open file for writing: %v", err))
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		return failResult("write", fmt.Errorf("write failed: %v", err))
	}
	return Result{OK: true, Action: "write", Path: target,
		Message: fmt.Sprintf("%s %s (%d bytes)", verb, pathx.Base(target), len(content))}
}

func doMkdir(path, name string) Result {
	target, err := joinNameWithin(path, name)
	if err != nil {
		return failResult("mkdir", err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return failResult("mkdir", fmt.Errorf("cannot create folder: %v", err))
	}
	return Result{OK: true, Action: "mkdir", Path: target,
		Message: fmt.Sprintf("folder ready: %s", pathx.Base(target))}
}

// resolveDest computes the final destination path for move/copy. When dest is
// an existing directory, the source basename is appended (copy INTO the folder,
// like `mv`). It then refuses if the final path already exists — move/copy
// NEVER clobber (Law 8b). Both src and the final dest are already root-checked
// by the callers.
func resolveDest(src, dest string) (string, error) {
	destPath, err := resolveWithinRoots(dest)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(destPath); err == nil && info.IsDir() {
		destPath = filepath.Join(destPath, filepath.Base(src))
		// Re-check containment of the joined-into-dir path.
		if destPath, err = resolveWithinRoots(destPath); err != nil {
			return "", err
		}
	}
	if _, err := os.Stat(destPath); err == nil {
		return "", fmt.Errorf("destination already exists: %s (refusing to overwrite)", pathx.Base(destPath))
	}
	return destPath, nil
}

func doMove(path, name, dest string) Result {
	src, err := joinNameWithin(path, name)
	if err != nil {
		return failResult("move", err)
	}
	if _, err := os.Stat(src); err != nil {
		return failResult("move", fmt.Errorf("source not found: %s", pathx.Base(src)))
	}
	if strings.TrimSpace(dest) == "" {
		return failResult("move", fmt.Errorf("no destination given (pass --dest)"))
	}
	destPath, err := resolveDest(src, dest)
	if err != nil {
		return failResult("move", err)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return failResult("move", fmt.Errorf("cannot create destination directory: %v", err))
	}
	if err := os.Rename(src, destPath); err != nil {
		// ponytail: os.Rename can't cross volumes; surface that as a typed error
		// rather than a copy+delete fallback — deleting the source is exactly the
		// data-loss path Law 8b forbids doing implicitly. Same-drive moves work;
		// a cross-drive move is reported so the caller can copy explicitly.
		return failResult("move", fmt.Errorf("could not move (same-drive moves only): %v", err))
	}
	return Result{OK: true, Action: "move", Path: destPath,
		Message: fmt.Sprintf("moved %s -> %s", pathx.Base(src), pathx.Base(destPath))}
}

func doCopy(path, name, dest string) Result {
	src, err := joinNameWithin(path, name)
	if err != nil {
		return failResult("copy", err)
	}
	info, err := os.Stat(src)
	if err != nil {
		return failResult("copy", fmt.Errorf("source not found: %s", pathx.Base(src)))
	}
	if strings.TrimSpace(dest) == "" {
		return failResult("copy", fmt.Errorf("no destination given (pass --dest)"))
	}
	destPath, err := resolveDest(src, dest)
	if err != nil {
		return failResult("copy", err)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return failResult("copy", fmt.Errorf("cannot create destination directory: %v", err))
	}
	if info.IsDir() {
		if err := copyTree(src, destPath); err != nil {
			return failResult("copy", fmt.Errorf("copy failed: %v", err))
		}
	} else {
		if err := copyFile(src, destPath); err != nil {
			return failResult("copy", fmt.Errorf("copy failed: %v", err))
		}
	}
	return Result{OK: true, Action: "copy", Path: destPath,
		Message: fmt.Sprintf("copied %s -> %s", pathx.Base(src), pathx.Base(destPath))}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

func doFind(path, name, ext string, maxResults int) Result {
	root, err := resolveWithinRoots(path)
	if err != nil {
		return failResult("find", err)
	}
	if maxResults <= 0 || maxResults > maxFindResults {
		maxResults = maxFindResults
	}
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	name = strings.ToLower(strings.TrimSpace(name))

	var entries []Entry
	dirCount := 0
	stop := fmt.Errorf("stop")
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
		}
		if info.IsDir() {
			dirCount++
			if dirCount > maxFindDirs {
				return filepath.SkipDir
			}
			return nil
		}
		if ext != "" && strings.ToLower(filepath.Ext(p)) != ext {
			return nil
		}
		if name != "" && !strings.Contains(strings.ToLower(info.Name()), name) {
			return nil
		}
		entries = append(entries, Entry{Name: info.Name(), Path: p, IsDir: false, Size: info.Size()})
		if len(entries) >= maxResults {
			return stop
		}
		return nil
	})
	return Result{OK: true, Action: "find", Path: root, Entries: entries,
		Message: fmt.Sprintf("found %d file(s) under %s", len(entries), pathx.Base(root))}
}

func doInfo(path, name string) Result {
	target, err := joinNameWithin(path, name)
	if err != nil {
		return failResult("info", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return failResult("info", fmt.Errorf("not found: %s", pathx.Base(target)))
	}
	return Result{OK: true, Action: "info", Path: target, Info: &FileInfo{
		Name:     info.Name(),
		Path:     target,
		IsDir:    info.IsDir(),
		Size:     info.Size(),
		Modified: info.ModTime().Format(time.RFC3339),
	}, Message: fmt.Sprintf("info for %s", pathx.Base(target))}
}

func failResult(action string, err error) Result {
	return Result{OK: false, Action: action, Error: err.Error()}
}

// run dispatches one action to its operation. It is the single entry point
// main() calls, and the one unit tests exercise directly.
func run(action string, opt options) Result {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "list", "ls":
		return doList(opt.path, opt.hidden)
	case "read", "cat":
		return doRead(opt.path, opt.name, opt.max)
	case "write":
		return doWrite(opt.path, opt.name, opt.content, opt.appendMode, opt.overwrite)
	case "mkdir", "create_folder":
		return doMkdir(opt.path, opt.name)
	case "move", "mv", "rename":
		return doMove(opt.path, opt.name, opt.dest)
	case "copy", "cp":
		return doCopy(opt.path, opt.name, opt.dest)
	case "find":
		return doFind(opt.path, opt.name, opt.ext, opt.max)
	case "info", "stat":
		return doInfo(opt.path, opt.name)
	default:
		return Result{OK: false, Error: fmt.Sprintf("unknown action: %q (want list|read|write|mkdir|move|copy|find|info)", action)}
	}
}

// options carries the parsed flags into run().
type options struct {
	path       string
	name       string
	dest       string
	content    string
	ext        string
	max        int
	hidden     bool
	appendMode bool
	overwrite  bool
}
