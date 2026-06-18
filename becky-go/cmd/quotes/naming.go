// naming.go — output-name derivation (SPEC §3) and the read-only integrity
// guard (SPEC §2 invariants).
//
// Output name = the VIDEO filename with its extension removed + "_QUOTES.srt",
// in the video's directory unless --out overrides. If no --video is given, the
// name is derived from the --srt stem with a trailing ".en" stripped, so a
// "<stem>.en.srt" transcript yields "<stem>_QUOTES.srt" — NOT
// "<stem>.en_QUOTES.srt" (SPEC §3 / §13.5).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// deriveOutPath returns the _QUOTES.srt path. Precedence: explicit out > video
// stem > srt stem (with a trailing ".en" stripped). dir follows the chosen
// source (video dir, else srt dir).
func deriveOutPath(out, video, srt string) string {
	if strings.TrimSpace(out) != "" {
		return out
	}
	if strings.TrimSpace(video) != "" {
		dir := filepath.Dir(video)
		stem := stripExt(filepath.Base(video))
		return filepath.Join(dir, stem+"_QUOTES.srt")
	}
	dir := filepath.Dir(srt)
	stem := stripSrtStem(filepath.Base(srt))
	return filepath.Join(dir, stem+"_QUOTES.srt")
}

// stripExt removes the final extension from a base name ("foo.mp4" -> "foo").
func stripExt(base string) string {
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// stripSrtStem turns a transcript base name into the video stem: drop a trailing
// ".srt"/".vtt", then a trailing ".en" (or "<stem>.en-US" etc.) language tag, so
// "stream.en.srt" -> "stream" (SPEC §3 note). Conservative: only strips a SHORT
// language-looking final segment, never a meaningful filename part.
func stripSrtStem(base string) string {
	stem := stripExt(base) // drop .srt/.vtt
	// strip a trailing language tag like ".en", ".en-US", ".en-orig".
	if dot := strings.LastIndexByte(stem, '.'); dot >= 0 {
		tag := stem[dot+1:]
		if looksLikeLangTag(tag) {
			stem = stem[:dot]
		}
	}
	return stem
}

// looksLikeLangTag reports whether s resembles a subtitle language suffix
// ("en", "en-US", "en-orig", "spa"): short, starts with letters, only
// letters/hyphen. Deliberately strict to avoid eating real name parts.
func looksLikeLangTag(s string) bool {
	if s == "" || len(s) > 8 {
		return false
	}
	if !isAlpha(rune(s[0])) {
		return false
	}
	for _, r := range s {
		if !isAlpha(r) && r != '-' {
			return false
		}
	}
	return true
}

func isAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// fileSHA256 returns the hex sha256 of a file, or "" if it cannot be read
// (a missing --video is allowed — the guard simply records "" for it).
func fileSHA256(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
