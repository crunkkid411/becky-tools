// files.go — language detection by extension and the source-file walker.
//
// The walker collects scannable files under the target path, skipping the usual
// non-source noise (VCS dirs, vendored deps, build output). Each returned file
// carries its detected language so the per-language analyzers know which rules
// apply. When --since is given the caller restricts this set to changed files.
package main

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// Supported languages.
const (
	langGo     = "go"
	langPython = "python"
	langRust   = "rust"
	langTS     = "typescript"
	langJS     = "javascript"
)

// extToLang maps a lowercase file extension to its language. Anything not here
// is ignored by the scanner.
var extToLang = map[string]string{
	".go":  langGo,
	".py":  langPython,
	".rs":  langRust,
	".ts":  langTS,
	".tsx": langTS,
	".js":  langJS,
	".jsx": langJS,
	".mjs": langJS,
	".cjs": langJS,
}

// skipDirs are directory names never worth scanning. Matched on the base name.
var skipDirs = map[string]bool{
	".git":         true,
	".hg":          true,
	".svn":         true,
	"node_modules": true,
	"vendor":       true,
	"target":       true, // rust build output
	"dist":         true,
	"build":        true,
	"bin":          true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	".idea":        true,
	".vscode":      true,
	".cache":       true,
	".next":        true,
}

// sourceFile is a discovered file plus its detected language.
type sourceFile struct {
	path string // absolute path on disk
	rel  string // path relative to the scan root (forward slashes), used in findings
	lang string
}

// langOf returns the language for a path, or "" if the extension is unscanned.
func langOf(path string) string {
	return extToLang[strings.ToLower(filepath.Ext(path))]
}

// walkSources collects every scannable source file under root. languages, when
// non-empty, restricts results to those languages. A file is skipped if any of
// its path segments is in skipDirs. The returned slice is sorted for
// deterministic output.
func walkSources(root string, languages map[string]bool) ([]sourceFile, error) {
	var files []sourceFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip, never abort the whole walk
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		lang := langOf(path)
		if lang == "" {
			return nil
		}
		if len(languages) > 0 && !languages[lang] {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		files = append(files, sourceFile{
			path: path,
			rel:  filepath.ToSlash(rel),
			lang: lang,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortSources(files)
	return files, nil
}

// filterByPaths keeps only the source files whose path is in the allow set.
// Used by --since to restrict a full walk to changed files. The allow set holds
// absolute paths (cleaned).
func filterByPaths(files []sourceFile, allow map[string]bool) []sourceFile {
	var kept []sourceFile
	for _, f := range files {
		if allow[filepath.Clean(f.path)] {
			kept = append(kept, f)
		}
	}
	return kept
}

// detectedLanguages returns the sorted set of languages present in files.
func detectedLanguages(files []sourceFile) []string {
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.lang] = true
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sortStrings(out)
	return out
}
