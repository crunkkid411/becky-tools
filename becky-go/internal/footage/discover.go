package footage

// discover.go is becky-clip's FORGIVING transcript resolver — the fallback that
// runs only after the strict sidecar.FindSubtitle (which matches just
// "<videostem>.srt" / "<videostem>.en.srt" / "<videostem>.<lang>.srt" in the same
// dir) returns "". Real-world case footage names its transcripts loosely:
// "BTIG3571_converted.srt", "IMG_9624x.srt", a generic "transcript.srt", or a
// file sitting in a "captions/"-style subfolder. None of those match the strict
// rule, so the videos were unsearchable (has_transcript=false). This resolver
// pairs a video with a NEARBY subtitle using SAFE, boundary-aware rules so a
// false pair never happens — a miss is strictly better than a wrong transcript.
//
// SAFETY is the whole point. Every rule requires either a stem-boundary
// relationship (the subtitle's stem equals the video stem, or begins with the
// video stem followed by a real separator) or an unambiguous 1:1 directory
// pairing. There is deliberately NO fuzzy/Levenshtein matching and NO pairing
// across unrelated names. The classic trap — "clip1.mp4" grabbing "clip10.srt"
// because "clip10" starts with "clip1" — is rejected because the character after
// the prefix ("0") is alphanumeric, not a separator.
//
// Determinism: directory entries are sorted before matching, so the chosen
// sidecar is stable across runs and platforms. Degrade-never-crash: an unreadable
// directory yields no match, never a panic. The video bytes are never opened.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ytIDRe matches a yt-dlp video-id token: an 11-char YouTube id wrapped in square
// brackets, e.g. "[AknVT7uuvXY]" or "[T0r3hrJPW-g]". yt-dlp embeds this token in
// BOTH the downloaded video filename and its subtitle filename, so it is a very
// high-precision pairing key even when the rest of the two names differ. The
// bracket requirement is deliberate: a bare 11-char run inside a longer word must
// NOT match (that would pair unrelated files), so we only ever key off the
// bracketed form. The id alphabet is YouTube's URL-safe base64 ([A-Za-z0-9_-]).
var ytIDRe = regexp.MustCompile(`\[([A-Za-z0-9_-]{11})\]`)

// videoIDToken extracts the bracketed YouTube-id token (including the brackets,
// lowercased for case-insensitive comparison) from a file name, or "" if the name
// carries no such token. Only the FIRST token is used; a name with two bracketed
// ids is pathological and we key off the first deterministically.
func videoIDToken(name string) string {
	m := ytIDRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return "[" + strings.ToLower(m[1]) + "]"
}

// subtitleExts is the set of subtitle/transcript extensions this resolver will
// pair to a video. It matches exactly what sidecar.ParseSubtitle can parse, so a
// resolved transcript is always loadable. Lowercased, dot-prefixed.
var subtitleExts = map[string]bool{
	".srt":   true,
	".vtt":   true,
	".json3": true,
}

// subtitleSubdirs are the conventional sibling folders a detective's tooling
// drops captions into. Compared case-insensitively against a directory's base
// name. Used by rule 2 (sibling subtitle subfolders).
var subtitleSubdirs = map[string]bool{
	"subs":        true,
	"subtitles":   true,
	"captions":    true,
	"transcripts": true,
	"srt":         true,
}

// separators are the characters that legitimately delimit a video stem from a
// trailing qualifier in a subtitle filename (e.g. "BTIG3571_converted",
// "video - en", "video.mp4"). A boundary-prefix match REQUIRES the character
// immediately after the video stem to be one of these — that is what makes
// "clip1" reject "clip10" (next char '0' is not here).
const separators = "_-. "

// resolveTranscript is the forgiving fallback resolver. It returns the best
// nearby subtitle sidecar for videoPath, or "" if none is found safely. claimed
// is the set of already-paired subtitle paths (cleaned) within the current index
// walk; a subtitle already claimed by an earlier video is skipped so one .srt is
// not paired to two videos when another candidate exists. claimed may be nil
// (no claim tracking — every candidate is eligible).
//
// Order = most-confident first; the first rule that yields a match wins:
//  0. YouTube-id match (HIGHEST precision): the video filename carries a bracketed
//     11-char id token and a subtitle anywhere we look carries the SAME token;
//  1. same-dir boundary-prefix (stem == video stem, or stem begins with the
//     video stem + a separator), honoring a ".en." preference;
//  2. a sibling subtitle subfolder (subs/subtitles/captions/transcripts/srt)
//     containing such a match;
//  3. lone-pair: the directory holds exactly one video and exactly one subtitle.
func resolveTranscript(videoPath string, claimed map[string]bool) string {
	dir := filepath.Dir(videoPath)
	stem := strings.ToLower(stemOf(videoPath))

	// Rule 0: YouTube-id token match (tried first — it is the most confident pair
	// real-world yt-dlp footage offers, and it links files whose names otherwise
	// differ). Searches the same dir AND the conventional caption subfolders.
	if hit := idMatch(dir, filepath.Base(videoPath), claimed); hit != "" {
		return hit
	}

	// Rule 1: same directory, boundary-prefix match.
	if hit := boundaryMatch(dir, stem, claimed); hit != "" {
		return hit
	}

	// Rule 2: sibling subtitle subfolders next to the video.
	if hit := siblingSubdirMatch(dir, stem, claimed); hit != "" {
		return hit
	}

	// Rule 3: lone-pair fallback (exactly one video + one subtitle in the dir).
	if hit := lonePairMatch(dir, videoPath, claimed); hit != "" {
		return hit
	}

	return ""
}

// idMatch implements rule 0: if videoName carries a bracketed YouTube-id token,
// return the best subtitle — in dir or in any conventional caption subfolder —
// whose name carries the SAME token. yt-dlp writes the id into both the video and
// its caption file, so a shared bracketed token is a confident pair even when the
// rest of the names differ ("..._[ID].mp4" ↔ "..._stream_390_..._[ID].en.srt").
// A video with no id token yields "" (rule 0 simply doesn't apply, the boundary
// rules take over). Already-claimed subtitles are skipped. The same-dir match is
// preferred over a subfolder match; within a directory the ".en." marker wins,
// then lexical order — all deterministic.
func idMatch(dir, videoName string, claimed map[string]bool) string {
	token := videoIDToken(videoName)
	if token == "" {
		return ""
	}
	// Same directory first.
	if hit := idMatchInDir(dir, token, claimed); hit != "" {
		return hit
	}
	// Then conventional caption subfolders (subs/subtitles/captions/transcripts/srt).
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	subs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && subtitleSubdirs[strings.ToLower(e.Name())] {
			subs = append(subs, e.Name())
		}
	}
	sortNamesStable(subs)
	for _, s := range subs {
		if hit := idMatchInDir(filepath.Join(dir, s), token, claimed); hit != "" {
			return hit
		}
	}
	return ""
}

// idMatchInDir returns the best subtitle in a single directory whose name carries
// token (a lowercased "[id]" string), or "". The ".en." marker is preferred over
// a bare match; within a tier the lexicographically-first (already sorted) name
// wins for determinism. Already-claimed subtitles are skipped.
func idMatchInDir(dir, token string, claimed map[string]bool) string {
	names := subtitleNames(dir)
	var best, bestEN string
	for _, n := range names {
		if videoIDToken(n) != token {
			continue
		}
		full := filepath.Join(dir, n)
		if claimed[filepath.Clean(full)] {
			continue
		}
		if hasENMarker(n) {
			if bestEN == "" {
				bestEN = full
			}
		} else if best == "" {
			best = full
		}
	}
	if bestEN != "" {
		return bestEN
	}
	return best
}

// stemOf returns the file name without its extension. filepath.Base/Ext suffice
// here because Index always feeds absolute, host-native paths (filepath.WalkDir
// produces them); the resolver never receives a foreign-OS path literal.
func stemOf(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// subtitleNames returns the subtitle file names in dir, sorted lexicographically
// (case-insensitive) for deterministic selection. Unreadable dirs yield nil
// (degrade, never crash). Directory entries are excluded.
func subtitleNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if subtitleExts[strings.ToLower(filepath.Ext(e.Name()))] {
			out = append(out, e.Name())
		}
	}
	sortNamesStable(out)
	return out
}

// sortNamesStable sorts file names case-insensitively, with the exact-byte order
// as a tie-break, so selection is fully deterministic across platforms.
func sortNamesStable(names []string) {
	sort.Slice(names, func(i, j int) bool {
		li, lj := strings.ToLower(names[i]), strings.ToLower(names[j])
		if li != lj {
			return li < lj
		}
		return names[i] < names[j]
	})
}

// boundaryMatch implements rule 1 over a single directory: among the subtitle
// files in dir, return the best one whose stem == stem OR starts with stem
// followed by a separator. A subtitle carrying an English marker (".en." in its
// name, e.g. "video.en.srt") is preferred over a bare match; within a preference
// tier the lexicographically-first (already sorted) name wins for determinism.
// Already-claimed subtitles are skipped. Returns the joined path or "".
func boundaryMatch(dir, stem string, claimed map[string]bool) string {
	names := subtitleNames(dir)
	var best, bestEN string
	for _, n := range names {
		full := filepath.Join(dir, n)
		if claimed[filepath.Clean(full)] {
			continue
		}
		subStem := strings.ToLower(stemOf(n))
		if !stemMatchesBoundary(stem, subStem) {
			continue
		}
		if hasENMarker(n) {
			if bestEN == "" {
				bestEN = full
			}
		} else if best == "" {
			best = full
		}
	}
	if bestEN != "" {
		return bestEN
	}
	return best
}

// stemMatchesBoundary reports whether subStem (a subtitle's lowercased stem) is a
// safe match for videoStem (also lowercased): either equal, or subStem begins
// with videoStem AND the very next character is a separator. The separator
// requirement is what prevents "clip1" from matching "clip10" — the next char
// '0' is alphanumeric, so it is rejected. Note subStem may itself still carry a
// language suffix (e.g. "video.en"); TrimSuffix(Ext) only removes the final
// ".srt"-style extension, so a "<stem>.en.srt" file presents subStem "<stem>.en",
// whose next char after the stem is '.', a separator — correctly accepted.
func stemMatchesBoundary(videoStem, subStem string) bool {
	if subStem == videoStem {
		return true
	}
	if len(subStem) <= len(videoStem) {
		return false
	}
	if !strings.HasPrefix(subStem, videoStem) {
		return false
	}
	next := subStem[len(videoStem)]
	return strings.IndexByte(separators, next) >= 0
}

// hasENMarker reports whether a subtitle file name carries an English-language
// marker as a dot-delimited token (".en." or trailing ".en"), e.g.
// "video.en.srt" / "interview.en-US.vtt". This honors the ".en." preference from
// rule 1 without matching an unrelated "...len.srt" (the token is bounded by
// dots). Case-insensitive.
func hasENMarker(name string) bool {
	lower := strings.ToLower(name)
	// Strip the final extension so a trailing ".en" before ".srt" is detectable.
	lower = strings.TrimSuffix(lower, filepath.Ext(lower))
	for _, tok := range strings.Split(lower, ".") {
		if tok == "en" || strings.HasPrefix(tok, "en-") {
			return true
		}
	}
	return false
}

// siblingSubdirMatch implements rule 2: scan conventional caption subfolders
// (subs/subtitles/captions/transcripts/srt, case-insensitive) located directly
// inside dir for a boundary-prefix match (rule 1) on the video stem. Subfolders
// are visited in sorted order for determinism. Returns the joined path or "".
func siblingSubdirMatch(dir, stem string, claimed map[string]bool) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	subs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && subtitleSubdirs[strings.ToLower(e.Name())] {
			subs = append(subs, e.Name())
		}
	}
	sortNamesStable(subs)
	for _, s := range subs {
		if hit := boundaryMatch(filepath.Join(dir, s), stem, claimed); hit != "" {
			return hit
		}
	}
	return ""
}

// lonePairMatch implements rule 3: if dir contains exactly one video file and
// exactly one subtitle file, pair them (covers a generic "transcript.srt" next
// to a single clip). The single video must be videoPath itself, and the single
// subtitle must not already be claimed. Any ambiguity (≠1 video or ≠1 subtitle)
// yields "" — we never guess when more than one of either is present.
//
// CRITICAL exception: a lone subtitle whose stem is a prefix-collision with the
// video stem (it begins with the video stem but FAILED the rule-1 boundary check,
// e.g. "clip10.srt" beside "clip1.mp4") is deliberately NOT paired here. That
// naming is intentional and points at a different source; pairing it would be the
// exact false pair the boundary rule exists to prevent. A genuinely generic name
// ("transcript.srt") does not begin with the video stem, so it still pairs.
func lonePairMatch(dir, videoPath string, claimed map[string]bool) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var videos, subtitles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		switch {
		case videoExts[ext]:
			videos = append(videos, e.Name())
		case subtitleExts[ext]:
			subtitles = append(subtitles, e.Name())
		}
	}
	if len(videos) != 1 || len(subtitles) != 1 {
		return ""
	}
	// The one video in this dir must be the one we're resolving.
	if !strings.EqualFold(videos[0], filepath.Base(videoPath)) {
		return ""
	}
	// Reject the prefix-collision near-miss (clip1 ↔ clip10): if the subtitle
	// stem starts with the video stem yet did not boundary-match, the names were
	// chosen to differ — don't paper over that with a lone-pair.
	videoStem := strings.ToLower(stemOf(videoPath))
	subStem := strings.ToLower(stemOf(subtitles[0]))
	if subStem != videoStem &&
		strings.HasPrefix(subStem, videoStem) &&
		!stemMatchesBoundary(videoStem, subStem) {
		return ""
	}
	full := filepath.Join(dir, subtitles[0])
	if claimed[filepath.Clean(full)] {
		return ""
	}
	return full
}

// collectOrphans walks the whole case-folder tree (root + every subfolder, incl.
// "transcripts/") for subtitle files and returns those NOT in claimed — i.e. the
// transcripts that paired to no indexed video. Each gets a human Title derived
// from its filename (deriveTranscriptTitle). This is what makes Jordan's 418
// orphaned `.en.srt` searchable even though their videos are absent or still
// `.mp4.part`. Read-only, degrade-never-crash: an unreadable subtree is skipped.
// Determinism: the result is sorted by Path.
func collectOrphans(root string, claimed map[string]bool) []OrphanTranscript {
	out := []OrphanTranscript{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !subtitleExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if claimed[filepath.Clean(path)] {
			return nil // this subtitle is a video's transcript — not an orphan
		}
		out = append(out, OrphanTranscript{
			Path:  path,
			Title: deriveTranscriptTitle(filepath.Base(path)),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// langSuffixRe matches a trailing language tag immediately before the subtitle
// extension, e.g. ".en" / ".en-US" / ".en-orig" in "...episode.en.srt". Stripped
// so the derived title doesn't end in ".en". The subtitle extension itself is
// removed first.
var langSuffixRe = regexp.MustCompile(`\.[A-Za-z]{2,3}(-[A-Za-z0-9]+)?$`)

// datePrefixRe matches a leading recording-date prefix yt-dlp prepends, either
// "2025-09-28_" (ISO) or "20250928_" (compact), so the human title starts at the
// real episode name, not a date.
var datePrefixRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}|\d{8})[_ -]+`)

// streamNoRe matches a leading "stream_NNN_" episode-number prefix (yt-dlp
// playlist autonumber), e.g. "stream_390_". Removed after the date prefix.
var streamNoRe = regexp.MustCompile(`(?i)^stream[_ -]*\d+[_ -]+`)

// deriveTranscriptTitle turns a subtitle FILE NAME into a human episode label by
// stripping the machine scaffolding yt-dlp adds: the extension, a trailing
// language tag (".en"), the bracketed "[id]" token, a leading date prefix, and a
// leading "stream_NNN_" number. Example:
//
//	"2025-09-28_stream_390_TakingBack2007 is live! DUCKY IRL STREAM_[H27b7Hmem5E].en.srt"
//	→ "TakingBack2007 is live! DUCKY IRL STREAM"
//
// If stripping leaves nothing (a name that was ALL scaffolding), it falls back to
// the original stem so a label is never empty.
func deriveTranscriptTitle(name string) string {
	s := name
	// 1) Drop the subtitle extension (".srt"/".vtt"/".json3").
	s = strings.TrimSuffix(s, filepath.Ext(s))
	// 2) Drop a trailing language tag (".en", ".en-US", …).
	s = langSuffixRe.ReplaceAllString(s, "")
	// 3) Drop the bracketed YouTube-id token anywhere it sits.
	s = ytIDRe.ReplaceAllString(s, "")
	// 4) Drop a leading date prefix, then a leading "stream_NNN_" number.
	s = datePrefixRe.ReplaceAllString(s, "")
	s = streamNoRe.ReplaceAllString(s, "")
	// 5) Tidy separators left behind (trailing "_"/"-", collapsed whitespace).
	s = strings.Trim(s, "_- ")
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		// Pure-scaffolding name: fall back to the bare stem so the label isn't empty.
		base := strings.TrimSuffix(name, filepath.Ext(name))
		base = strings.Trim(base, "_- ")
		if base == "" {
			return name
		}
		return base
	}
	return s
}
