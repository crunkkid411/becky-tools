package samplelib

import (
	"encoding/binary"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// roleTokens maps each role to the lowercase tokens that signal it in a filename or
// parent-folder name. Tokens are matched as whole "words" (split on non-alphanumeric
// boundaries) so "kik" matches "trap kik 01" but "bd" does NOT match "bird". The order
// of this slice is the precedence order when multiple roles tie (kept stable for
// deterministic results).
var roleTokens = []struct {
	role   string
	tokens []string
}{
	{RoleKick, []string{"kick", "kik", "bd"}},
	{RoleSnare, []string{"snare", "snr", "sd"}},
	{RoleHat, []string{"hat", "hh", "chh", "ohh", "hihat"}},
	{RoleClap, []string{"clap", "clp"}},
	{RoleTom, []string{"tom"}},
	{RoleCrash, []string{"crash", "crsh", "cym"}},
	{RoleRide, []string{"ride"}},
	{RolePerc, []string{"perc", "shaker", "conga", "rim"}},
	{RoleBass, []string{"808", "sub", "bass"}},
	{RoleVocal, []string{"vocal", "vox", "voc"}},
	{RoleFX, []string{"fx", "riser", "sweep", "impact"}},
}

// nonAlnum splits identifiers into word tokens.
var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// tokenize lowercases s, drops the file extension, and splits it into alphanumeric
// word tokens. A trailing plural "s" is also recorded as a stem (so the folder "Kicks"
// matches the token "kick") — but only for tokens long enough that stripping "s" is
// safe (avoids turning a short token into a false match).
func tokenize(s string) map[string]bool {
	// Drop an extension so "kick.wav" -> "kick", not {kick, wav}.
	if dot := strings.LastIndexByte(s, '.'); dot > 0 {
		s = s[:dot]
	}
	out := map[string]bool{}
	for _, t := range nonAlnum.Split(strings.ToLower(s), -1) {
		if t == "" {
			continue
		}
		out[t] = true
		if len(t) >= 4 && strings.HasSuffix(t, "s") {
			out[t[:len(t)-1]] = true // de-pluralize: "kicks" -> "kick"
		}
	}
	return out
}

// roleFromTokens returns the first role (in precedence order) whose token set
// intersects the given word set, or RoleUnknown.
func roleFromTokens(words map[string]bool) string {
	for _, rt := range roleTokens {
		for _, tok := range rt.tokens {
			if words[tok] {
				return rt.role
			}
		}
	}
	return RoleUnknown
}

// guessRole applies corroborate-then-conclude: a role is CONCLUDED (high) only when the
// filename and the parent folder agree; a lone token (one side only) is low confidence;
// nothing on either side is unknown. We never return a role we are not at least
// low-confident in.
func guessRole(name, folder string) (role, confidence string) {
	fileRole := roleFromTokens(tokenize(name))
	folderRole := roleFromTokens(tokenize(folder))

	switch {
	case fileRole != RoleUnknown && fileRole == folderRole:
		return fileRole, ConfHigh
	case fileRole != RoleUnknown && folderRole != RoleUnknown && fileRole != folderRole:
		// Disagreement: trust the filename (more specific) but stay low-confidence.
		return fileRole, ConfLow
	case fileRole != RoleUnknown:
		return fileRole, ConfLow
	case folderRole != RoleUnknown:
		return folderRole, ConfLow
	default:
		return RoleUnknown, ConfUnknown
	}
}

// bpmRe matches a BPM token like "90bpm", "128 BPM", "174bpm". A boundary precedes the
// digits so "5128bpm" won't read as 128; "bpm" is followed by a non-letter boundary.
var bpmRe = regexp.MustCompile(`(?i)(?:^|[^0-9])(\d{2,3})\s?bpm(?:[^a-z]|$)`)

// bpmFromName extracts a BPM from a filename token, or 0 if none / out of a sane range.
func bpmFromName(name string) float64 {
	m := bpmRe.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	v, err := strconv.Atoi(m[1])
	if err != nil || v < 40 || v > 300 {
		return 0
	}
	return float64(v)
}

// keyRe matches a conservative musical-key token: a note A-G, optional #/b accidental,
// optional maj/min/m quality. Bounded by a non-alphanumeric (treating "_" as a
// separator, which Go's \b does NOT — that was a real bug).
var keyRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9#])([a-g](#|b)?(maj|min|m)?)(?:[^a-z0-9#]|$)`)

// keyFromName extracts a conservative musical-key token from a filename, or "" if none.
// Bare single-letter matches (e.g. "a"/"e"/"b") are too ambiguous in filenames and are
// rejected — we only conclude a key when there is an accidental or a quality suffix.
func keyFromName(name string) string {
	for _, m := range keyRe.FindAllStringSubmatch(name, -1) {
		tok := m[1]
		// Bare single letter (e.g. "a", "e", "b") is too ambiguous in filenames.
		if len(tok) == 1 {
			continue
		}
		return canonKey(tok)
	}
	return ""
}

// canonKey normalizes a key token: capital note letter, lower accidental, lower quality.
func canonKey(tok string) string {
	if tok == "" {
		return ""
	}
	r := []rune(tok)
	out := strings.ToUpper(string(r[0]))
	out += strings.ToLower(string(r[1:]))
	return out
}

// guessKind fuses signals: a BPM token plus a multi-second duration => loop; a short
// (<~1s) clip with no BPM => one-shot; otherwise unknown. Per corroborate-then-conclude
// we only conclude when the signals agree.
func guessKind(bpm, dur float64) string {
	const shortMax = 1.0 // seconds
	const loopMin = 1.0  // a loop is at least ~1s
	switch {
	case bpm > 0 && dur >= loopMin:
		return KindLoop
	case bpm > 0 && dur == 0:
		// BPM token but unknown duration (non-wav) — a BPM-named sample is almost
		// always a loop; corroboration is partial so this is still a fair conclusion.
		return KindLoop
	case dur > 0 && dur < shortMax && bpm == 0:
		return KindOneShot
	default:
		return KindUnknown
	}
}

// wavDurationSec reads ONLY the RIFF header (fmt + data chunk sizes) to compute the
// duration in seconds — it never decodes samples. Returns an error for a non-RIFF or
// truncated/zero-rate file so the caller can index it with unknown duration.
func wavDurationSec(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil {
		return 0, err
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return 0, errNotWAV
	}

	var (
		byteRate uint32 // bytes/sec (from fmt) — the simplest duration basis
		dataSize uint32
		haveFmt  bool
		haveData bool
	)
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			break // EOF or truncation: stop scanning chunks
		}
		id := string(hdr[0:4])
		size := binary.LittleEndian.Uint32(hdr[4:8])
		switch id {
		case "fmt ":
			body := make([]byte, size)
			if _, err := io.ReadFull(f, body); err != nil {
				return 0, err
			}
			if len(body) >= 16 {
				byteRate = binary.LittleEndian.Uint32(body[8:12])
			}
			haveFmt = true
			if size%2 == 1 {
				f.Seek(1, io.SeekCurrent)
			}
		case "data":
			dataSize = size
			haveData = true
			// We have what we need; no need to read the (large) audio body.
			goto done
		default:
			// Skip this chunk's body (word-aligned).
			skip := int64(size)
			if size%2 == 1 {
				skip++
			}
			if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
				break
			}
		}
	}
done:
	if !haveFmt || !haveData || byteRate == 0 {
		return 0, errBadWAVHeader
	}
	return float64(dataSize) / float64(byteRate), nil
}

var (
	errNotWAV       = errString("not a RIFF/WAVE file")
	errBadWAVHeader = errString("missing/zero WAV header fields")
)

type errString string

func (e errString) Error() string { return string(e) }
