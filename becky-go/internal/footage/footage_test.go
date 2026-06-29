package footage

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a tiny fixture helper.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// synthFolder builds a tiny case folder: two videos in the root + one in a
// subdir, two with SRT sidecars, one with a beckymeta sidecar, plus a stray
// non-video file. Returns the root.
func synthFolder(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Video 1: has transcript + beckymeta.
	writeFile(t, filepath.Join(root, "2026-06-14_ring.mp4"), "not real video bytes")
	writeFile(t, filepath.Join(root, "2026-06-14_ring.srt"),
		"1\n00:00:10,000 --> 00:00:13,000\nI will pay you for the cat\n\n"+
			"2\n00:00:20,000 --> 00:00:22,000\nbring me the cat Penguin\n")
	writeFile(t, filepath.Join(root, "2026-06-14_ring.mp4.beckymeta.json"),
		`{"date":"2026-06-14","source_fps":30,"person":"defendant"}`)

	// Video 2: has an .en.srt transcript, no meta.
	writeFile(t, filepath.Join(root, "doorbell.mov"), "x")
	writeFile(t, filepath.Join(root, "doorbell.en.srt"),
		"1\n00:01:00,000 --> 00:01:04,000\nthe dog is barking\n")

	// Video 3 in a subdir: no transcript.
	writeFile(t, filepath.Join(root, "sub", "clip3.mkv"), "y")

	// Stray non-video file (must be ignored).
	writeFile(t, filepath.Join(root, "notes.txt"), "ignore me")

	return root
}

func TestIndex(t *testing.T) {
	root := synthFolder(t)
	idx, err := Index(root)
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if len(idx.Videos) != 3 {
		t.Fatalf("Index() found %d videos, want 3: %+v", len(idx.Videos), idx.Videos)
	}

	by := map[string]Video{}
	for _, v := range idx.Videos {
		by[v.Name] = v
	}

	ring, ok := by["2026-06-14_ring.mp4"]
	if !ok {
		t.Fatal("ring video missing from index")
	}
	if !ring.HasTranscript || filepath.Base(ring.TranscriptPath) != "2026-06-14_ring.srt" {
		t.Fatalf("ring transcript = %q (has=%v), want 2026-06-14_ring.srt", ring.TranscriptPath, ring.HasTranscript)
	}
	if ring.Meta.Date != "2026-06-14" || ring.Meta.SourceFPS != 30 || ring.Meta.Person != "defendant" {
		t.Fatalf("ring meta = %+v, want date/fps/person filled", ring.Meta)
	}

	door, ok := by["doorbell.mov"]
	if !ok || !door.HasTranscript {
		t.Fatalf("doorbell should be indexed with its .en.srt; got %+v", door)
	}
	if door.Meta.Date != "" {
		t.Fatalf("doorbell has no meta sidecar; want zero Meta, got %+v", door.Meta)
	}

	clip3, ok := by["clip3.mkv"]
	if !ok || clip3.HasTranscript {
		t.Fatalf("clip3 should be indexed with NO transcript; got %+v", clip3)
	}

	if got := len(idx.WithTranscripts()); got != 2 {
		t.Fatalf("WithTranscripts() = %d, want 2", got)
	}
	if _, ok := idx.VideoByName("doorbell.mov"); !ok {
		t.Fatal("VideoByName(doorbell.mov) should resolve")
	}
	if _, ok := idx.VideoByName("nope.mp4"); ok {
		t.Fatal("VideoByName(nope.mp4) should not resolve")
	}
}

func TestIndexMissingFolderDegrades(t *testing.T) {
	idx, err := Index(filepath.Join(t.TempDir(), "does-not-exist"))
	// A non-nil error here is acceptable (root truly absent); the contract is
	// "never panic, never invent videos".
	if len(idx.Videos) != 0 {
		t.Fatalf("missing folder yielded %d videos, want 0 (err=%v)", len(idx.Videos), err)
	}
}

func TestGrepTranscripts(t *testing.T) {
	root := synthFolder(t)
	idx, err := Index(root)
	if err != nil {
		t.Fatal(err)
	}

	cands := GrepTranscripts(idx, []string{"cat", "Penguin"})
	if len(cands) != 2 {
		t.Fatalf("grep cat/Penguin = %d candidates, want 2: %+v", len(cands), cands)
	}
	// The cue that hits BOTH terms ("bring me the cat Penguin") must rank first.
	if cands[0].Text != "bring me the cat Penguin" {
		t.Fatalf("top candidate = %q, want the two-term hit first", cands[0].Text)
	}
	if cands[0].Timestamp != 20.0 || cands[0].Source == "" {
		t.Fatalf("candidate timestamps/source must be verbatim from the cue; got %+v", cands[0])
	}
	if len(cands[0].Terms) != 2 {
		t.Fatalf("top candidate should record 2 matched terms, got %v", cands[0].Terms)
	}

	// Case-insensitive and OR semantics.
	if got := GrepTranscripts(idx, []string{"DOG"}); len(got) != 1 || got[0].Name != "doorbell.mov" {
		t.Fatalf("grep DOG = %+v, want 1 hit in doorbell.mov", got)
	}

	// Blank-only term list → no candidates (not the whole transcript).
	if got := GrepTranscripts(idx, []string{"   ", ""}); len(got) != 0 {
		t.Fatalf("blank terms should yield 0 candidates, got %d", len(got))
	}
	// A miss yields an empty, non-nil slice.
	if got := GrepTranscripts(idx, []string{"zzzz"}); got == nil || len(got) != 0 {
		t.Fatalf("a miss should be empty non-nil, got %v", got)
	}
}

func TestMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	writeFile(t, video, "video bytes that must NOT be touched")
	before, _ := os.ReadFile(video)

	// Missing sidecar → zero Meta, nil error.
	m, err := LoadMeta(video)
	if err != nil {
		t.Fatalf("LoadMeta(missing) error = %v, want nil", err)
	}
	if (m != Meta{}) {
		t.Fatalf("LoadMeta(missing) = %+v, want zero Meta", m)
	}

	// Save then load round-trips exactly, and writes only the sidecar.
	want := Meta{Date: "2026-06-14", Link: "https://example/evidence", Person: "defendant", Location: "porch", SourceFPS: 29.97}
	if err := SaveMeta(video, want); err != nil {
		t.Fatalf("SaveMeta error = %v", err)
	}
	if _, err := os.Stat(MetaPath(video)); err != nil {
		t.Fatalf("sidecar %s should exist after SaveMeta: %v", MetaPath(video), err)
	}
	got, err := LoadMeta(video)
	if err != nil {
		t.Fatalf("LoadMeta after save error = %v", err)
	}
	if got != want {
		t.Fatalf("round-trip Meta = %+v, want %+v", got, want)
	}

	// The video bytes must be byte-identical (originals never modified).
	after, _ := os.ReadFile(video)
	if string(before) != string(after) {
		t.Fatal("SaveMeta modified the video file — originals must never be touched")
	}
}

// indexByName indexes root and returns a name→Video map, failing the test on
// any indexing error.
func indexByName(t *testing.T, root string) map[string]Video {
	t.Helper()
	idx, err := Index(root)
	if err != nil {
		t.Fatalf("Index(%s) error = %v", root, err)
	}
	by := map[string]Video{}
	for _, v := range idx.Videos {
		by[v.Name] = v
	}
	return by
}

// srtBody is a tiny one-cue SRT containing word, used so the resolved transcript
// is also greppable end-to-end.
func srtBody(word string) string {
	return "1\n00:00:01,000 --> 00:00:03,000\n" + word + " happened here\n"
}

// TestResolveExactStemRegression proves the strict-match path is NOT regressed:
// a plain "<stem>.srt" beside the video still resolves (this is also how a fresh
// becky-transcribe sidecar is found).
func TestResolveExactStemRegression(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "interview.mp4"), "v")
	writeFile(t, filepath.Join(root, "interview.srt"), srtBody("statement"))

	by := indexByName(t, root)
	v, ok := by["interview.mp4"]
	if !ok || !v.HasTranscript {
		t.Fatalf("interview.mp4 should have its exact-stem transcript; got %+v", v)
	}
	if filepath.Base(v.TranscriptPath) != "interview.srt" {
		t.Fatalf("transcript = %q, want interview.srt", v.TranscriptPath)
	}
}

// TestResolveConvertedSuffix covers the real-world "<stem>_converted.srt" name
// (a suffix after a separator) that the strict matcher misses.
func TestResolveConvertedSuffix(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "BTIG3571.mp4"), "v")
	writeFile(t, filepath.Join(root, "BTIG3571_converted.srt"), srtBody("bounty"))

	by := indexByName(t, root)
	v, ok := by["BTIG3571.mp4"]
	if !ok || !v.HasTranscript {
		t.Fatalf("BTIG3571.mp4 should pair its _converted.srt; got %+v", v)
	}
	if filepath.Base(v.TranscriptPath) != "BTIG3571_converted.srt" {
		t.Fatalf("transcript = %q, want BTIG3571_converted.srt", v.TranscriptPath)
	}
	// And it must be greppable through the same funnel becky-clip search uses.
	idx, _ := Index(root)
	if got := GrepTranscripts(idx, []string{"bounty"}); len(got) != 1 || got[0].Name != "BTIG3571.mp4" {
		t.Fatalf("grep bounty over resolved transcript = %+v, want 1 hit in BTIG3571.mp4", got)
	}
}

// TestResolveENPreference covers a "<stem>.en.srt" with a separator boundary and
// the ".en." preference winning over a bare same-stem match.
func TestResolveENPreference(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "porch.mov"), "v")
	// Two candidates that both boundary-match; the .en. one must be preferred.
	writeFile(t, filepath.Join(root, "porch.de.srt"), srtBody("hund"))
	writeFile(t, filepath.Join(root, "porch.en.srt"), srtBody("dog"))

	by := indexByName(t, root)
	v, ok := by["porch.mov"]
	if !ok || !v.HasTranscript {
		t.Fatalf("porch.mov should have a transcript; got %+v", v)
	}
	if filepath.Base(v.TranscriptPath) != "porch.en.srt" {
		t.Fatalf("transcript = %q, want porch.en.srt (.en. preference)", v.TranscriptPath)
	}
}

// TestResolveCaptionsSubfolder covers rule 2: a transcript living in a sibling
// captions/ folder next to the video.
func TestResolveCaptionsSubfolder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "doorcam.mp4"), "v")
	writeFile(t, filepath.Join(root, "captions", "doorcam.srt"), srtBody("threat"))

	by := indexByName(t, root)
	v, ok := by["doorcam.mp4"]
	if !ok || !v.HasTranscript {
		t.Fatalf("doorcam.mp4 should pair the captions/ transcript; got %+v", v)
	}
	if filepath.Base(filepath.Dir(v.TranscriptPath)) != "captions" {
		t.Fatalf("transcript should be in captions/, got %q", v.TranscriptPath)
	}
}

// TestResolveLonePair covers rule 3: exactly one video + one generically-named
// subtitle in a directory are paired.
func TestResolveLonePair(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "evidence_clip.mp4"), "v")
	writeFile(t, filepath.Join(root, "transcript.srt"), srtBody("admission"))

	by := indexByName(t, root)
	v, ok := by["evidence_clip.mp4"]
	if !ok || !v.HasTranscript {
		t.Fatalf("lone video+subtitle should be paired; got %+v", v)
	}
	if filepath.Base(v.TranscriptPath) != "transcript.srt" {
		t.Fatalf("transcript = %q, want transcript.srt", v.TranscriptPath)
	}
}

// TestResolveNegativeClip1NotClip10 is the load-bearing safety test: "clip1.mp4"
// must NOT grab "clip10.srt". The stem "clip10" starts with "clip1" but the next
// char '0' is alphanumeric (not a separator), so the boundary rule rejects it.
// There is more than one subtitle, so lone-pair does not apply either.
func TestResolveNegativeClip1NotClip10(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "clip1.mp4"), "v")
	writeFile(t, filepath.Join(root, "clip10.srt"), srtBody("unrelated"))

	by := indexByName(t, root)
	v, ok := by["clip1.mp4"]
	if !ok {
		t.Fatal("clip1.mp4 should be indexed")
	}
	if v.HasTranscript {
		t.Fatalf("clip1.mp4 must NOT pair clip10.srt (false pair); got transcript %q", v.TranscriptPath)
	}
}

// TestResolveNoDoublePair proves a single subtitle is not paired to two videos
// when only one can boundary-match it. Here "a.srt" boundary-matches a.mp4 only;
// b.mp4 has no candidate. (Guards the claim-tracking + boundary rules together.)
func TestResolveNoDoublePair(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.mp4"), "v")
	writeFile(t, filepath.Join(root, "b.mp4"), "v")
	writeFile(t, filepath.Join(root, "a_fixed.srt"), srtBody("only_a"))

	by := indexByName(t, root)
	if a := by["a.mp4"]; !a.HasTranscript || filepath.Base(a.TranscriptPath) != "a_fixed.srt" {
		t.Fatalf("a.mp4 should pair a_fixed.srt; got %+v", a)
	}
	if b := by["b.mp4"]; b.HasTranscript {
		t.Fatalf("b.mp4 must NOT pair a_fixed.srt; got %q", b.TranscriptPath)
	}
}

// TestResolveDeterministic checks that two indexes of the same loose-named folder
// pick the identical transcript (sorted-entry selection).
func TestResolveDeterministic(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cam.mp4"), "v")
	// Multiple bare boundary-matches; selection must be stable (lexical first).
	writeFile(t, filepath.Join(root, "cam_b.srt"), srtBody("b"))
	writeFile(t, filepath.Join(root, "cam_a.srt"), srtBody("a"))

	first := indexByName(t, root)["cam.mp4"].TranscriptPath
	second := indexByName(t, root)["cam.mp4"].TranscriptPath
	if first == "" || first != second {
		t.Fatalf("resolver must be deterministic: first=%q second=%q", first, second)
	}
	if filepath.Base(first) != "cam_a.srt" {
		t.Fatalf("stable selection should pick cam_a.srt (lexical first), got %q", first)
	}
}

func TestLoadMetaCorruptSidecar(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	writeFile(t, video, "x")
	writeFile(t, MetaPath(video), "{ this is not valid json")

	// LoadMeta surfaces the parse error...
	if _, err := LoadMeta(video); err == nil {
		t.Fatal("LoadMeta(corrupt) should return an error")
	}
	// ...but indexing degrades (a corrupt sidecar must not abort the walk).
	idx, err := Index(dir)
	if err != nil {
		t.Fatalf("Index() with a corrupt sidecar should not fail: %v", err)
	}
	if len(idx.Videos) != 1 || (idx.Videos[0].Meta != Meta{}) {
		t.Fatalf("corrupt sidecar should index the video with zero Meta; got %+v", idx.Videos)
	}
}

// TestEverySrtNamingSchemeIsSearchable proves the core guarantee: EVERY .srt in
// the tree is searchable, whatever it's named. Inconsistent agents have produced
// "<stem>.en.srt", "<stem>_LOCAL.srt", "<stem>_parakeet_transcription.srt", plus
// loose "captions.srt"/"becky.srt" with no matching video. Each must surface a hit
// for its unique term — paired to its video (GrepTranscripts) or standalone
// (GrepOrphans). If this fails, search is silently missing transcripts.
func TestEverySrtNamingSchemeIsSearchable(t *testing.T) {
	root := t.TempDir()
	cue := func(term string) string {
		return "1\n00:00:01,000 --> 00:00:03,000\nthis cue contains " + term + " verbatim\n"
	}
	// Paired schemes (a video + a transcript whose name varies).
	writeFile(t, filepath.Join(root, "2026-06-29_Normal_[abcdefghijk].mp4"), "x")
	writeFile(t, filepath.Join(root, "2026-06-29_Normal_[abcdefghijk].en.srt"), cue("alphaterm"))
	writeFile(t, filepath.Join(root, "2026-06-28_LocalCase.mp4"), "x")
	writeFile(t, filepath.Join(root, "2026-06-28_LocalCase_LOCAL.srt"), cue("betaterm"))
	writeFile(t, filepath.Join(root, "2026-06-27_Pk.mp4"), "x")
	writeFile(t, filepath.Join(root, "2026-06-27_Pk_parakeet_transcription.srt"), cue("gammaterm"))
	// Orphan schemes (loose transcripts with no matching video — must still search).
	writeFile(t, filepath.Join(root, "captions.srt"), cue("deltaterm"))
	writeFile(t, filepath.Join(root, "becky.srt"), cue("epsilonterm"))

	idx, err := Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	for _, term := range []string{"alphaterm", "betaterm", "gammaterm", "deltaterm", "epsilonterm"} {
		hits := len(GrepTranscripts(idx, []string{term})) + len(GrepOrphans(idx, []string{term}))
		if hits == 0 {
			t.Errorf("term %q is NOT searchable — a transcript was missed (videos=%d, orphans=%d)",
				term, len(idx.Videos), len(idx.Orphans))
		}
	}
}

// ---- YouTube-id pairing (rule 0) ------------------------------------------

// TestResolveYouTubeIDSameDir is the id-pairing positive case in the same dir:
// the video and its caption share a bracketed "[id]" token but the rest of the
// names differ (yt-dlp prepends a date + "stream_NNN_" to the caption). The
// boundary rules would miss this; the id token pairs them.
func TestResolveYouTubeIDSameDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "2025-12-22_Stream Ft. Puff_[AknVT7uuvXY].mp4"), "v")
	writeFile(t, filepath.Join(root, "2025-12-22_stream_52_Stream Ft. Puff_[AknVT7uuvXY].en.srt"), srtBody("penguin"))

	by := indexByName(t, root)
	v, ok := by["2025-12-22_Stream Ft. Puff_[AknVT7uuvXY].mp4"]
	if !ok || !v.HasTranscript {
		t.Fatalf("video should pair its same-id caption; got %+v", v)
	}
	if filepath.Base(v.TranscriptPath) != "2025-12-22_stream_52_Stream Ft. Puff_[AknVT7uuvXY].en.srt" {
		t.Fatalf("transcript = %q, want the [AknVT7uuvXY] caption", v.TranscriptPath)
	}
	// And it must be greppable as a video-backed (NOT orphan) hit.
	idx, _ := Index(root)
	if len(idx.Orphans) != 0 {
		t.Fatalf("the id-paired caption must not also be an orphan; got %+v", idx.Orphans)
	}
	if got := GrepTranscripts(idx, []string{"penguin"}); len(got) != 1 || got[0].Source == "" {
		t.Fatalf("grep penguin should be 1 video-backed hit; got %+v", got)
	}
}

// TestResolveYouTubeIDSubfolder covers the real-world layout: the caption lives
// in a transcripts/ subfolder (its video absent there) and pairs by id.
func TestResolveYouTubeIDSubfolder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "2025-12-29_Live_[Ssi9W8gOrCI].mp4"), "v")
	writeFile(t, filepath.Join(root, "transcripts", "2025-12-29_stream_27_Live_[Ssi9W8gOrCI].en.srt"), srtBody("bounty"))

	by := indexByName(t, root)
	v, ok := by["2025-12-29_Live_[Ssi9W8gOrCI].mp4"]
	if !ok || !v.HasTranscript {
		t.Fatalf("video should pair its caption in transcripts/; got %+v", v)
	}
	if filepath.Base(filepath.Dir(v.TranscriptPath)) != "transcripts" {
		t.Fatalf("transcript should be in transcripts/, got %q", v.TranscriptPath)
	}
}

// TestResolveYouTubeIDNegativeDifferentID proves two DIFFERENT ids never pair:
// the video carries [AAAAAAAAAAA] and the only caption carries [BBBBBBBBBBB], and
// their names share no boundary-prefix — so no transcript is found.
func TestResolveYouTubeIDNegativeDifferentID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "alpha_[AAAAAAAAAAA].mp4"), "v")
	writeFile(t, filepath.Join(root, "transcripts", "beta_[BBBBBBBBBBB].en.srt"), srtBody("nope"))

	by := indexByName(t, root)
	v, ok := by["alpha_[AAAAAAAAAAA].mp4"]
	if !ok {
		t.Fatal("video should be indexed")
	}
	if v.HasTranscript {
		t.Fatalf("different ids must NOT pair; got transcript %q", v.TranscriptPath)
	}
	// The beta caption is therefore an orphan.
	idx, _ := Index(root)
	if len(idx.Orphans) != 1 || filepath.Base(idx.Orphans[0].Path) != "beta_[BBBBBBBBBBB].en.srt" {
		t.Fatalf("beta caption should be the sole orphan; got %+v", idx.Orphans)
	}
}

// TestResolveYouTubeIDNegativeBareSubstring proves the bracket requirement: a
// caption whose name contains the 11-char id as a BARE substring (no brackets)
// must NOT pair, because only the "[id]" form is a confident key. The names also
// don't boundary-match, so nothing pairs.
func TestResolveYouTubeIDNegativeBareSubstring(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "clipX_[AknVT7uuvXY].mp4"), "v")
	// id present but unbracketed in the caption name → not an id pair.
	writeFile(t, filepath.Join(root, "transcripts", "randomAknVT7uuvXYtail.en.srt"), srtBody("nope"))

	by := indexByName(t, root)
	if v := by["clipX_[AknVT7uuvXY].mp4"]; v.HasTranscript {
		t.Fatalf("a bare (unbracketed) id substring must NOT pair; got %q", v.TranscriptPath)
	}
}

// TestResolveYouTubeIDPrefersEN proves that when two same-id captions exist, the
// ".en." one wins (mirrors the boundary rule's English preference).
func TestResolveYouTubeIDPrefersEN(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "show_[ZZZZZZZZZZZ].mp4"), "v")
	writeFile(t, filepath.Join(root, "transcripts", "show_[ZZZZZZZZZZZ].de.srt"), srtBody("hund"))
	writeFile(t, filepath.Join(root, "transcripts", "show_[ZZZZZZZZZZZ].en.srt"), srtBody("dog"))

	v := indexByName(t, root)["show_[ZZZZZZZZZZZ].mp4"]
	if !v.HasTranscript || filepath.Base(v.TranscriptPath) != "show_[ZZZZZZZZZZZ].en.srt" {
		t.Fatalf("id pairing should prefer the .en. caption; got %+v", v)
	}
}

// ---- orphan collection -----------------------------------------------------

// TestCollectOrphans proves that subtitles paired to NO video are collected as
// searchable orphans, that a video-backed transcript is NOT double-counted as an
// orphan, and that orphan hits flow through GrepOrphans with Source="" + Title.
func TestCollectOrphans(t *testing.T) {
	root := t.TempDir()
	// One indexed video WITH its transcript (must be excluded from orphans).
	writeFile(t, filepath.Join(root, "have_[VID0000000A].mp4"), "v")
	writeFile(t, filepath.Join(root, "transcripts", "2025-01-01_stream_1_have_[VID0000000A].en.srt"), srtBody("kept"))
	// Two orphan transcripts (their videos are absent).
	writeFile(t, filepath.Join(root, "transcripts", "2025-09-28_stream_390_DUCKY IRL STREAM_[ORPHAN0001A].en.srt"), srtBody("penguin"))
	writeFile(t, filepath.Join(root, "transcripts", "2025-09-29_stream_391_DUCKY PART TWO_[ORPHAN0002B].en.srt"), srtBody("bounty"))

	idx, err := Index(root)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly one video-backed transcript; exactly two orphans (no double-count).
	if got := len(idx.WithTranscripts()); got != 1 {
		t.Fatalf("WithTranscripts() = %d, want 1", got)
	}
	if len(idx.Orphans) != 2 {
		t.Fatalf("orphans = %d, want 2: %+v", len(idx.Orphans), idx.Orphans)
	}
	for _, o := range idx.Orphans {
		if filepath.Base(o.Path) == "2025-01-01_stream_1_have_[VID0000000A].en.srt" {
			t.Fatalf("the video-backed transcript must NOT be an orphan: %+v", o)
		}
	}
	// Determinism: sorted by Path.
	if idx.Orphans[0].Path > idx.Orphans[1].Path {
		t.Fatalf("orphans must be sorted by Path; got %+v", idx.Orphans)
	}
	// Titles are the derived episode labels.
	if idx.Orphans[0].Title != "DUCKY IRL STREAM" {
		t.Fatalf("orphan[0] title = %q, want \"DUCKY IRL STREAM\"", idx.Orphans[0].Title)
	}
	// GrepOrphans surfaces them with Source="" and Name=Title.
	got := GrepOrphans(idx, []string{"penguin"})
	if len(got) != 1 {
		t.Fatalf("GrepOrphans(penguin) = %d, want 1: %+v", len(got), got)
	}
	if got[0].Source != "" {
		t.Fatalf("orphan hit Source must be empty (no video), got %q", got[0].Source)
	}
	if got[0].Name != "DUCKY IRL STREAM" {
		t.Fatalf("orphan hit Name = %q, want the derived title", got[0].Name)
	}
	// GrepTranscripts (video-backed only) must NOT return the orphan hit.
	if v := GrepTranscripts(idx, []string{"penguin"}); len(v) != 0 {
		t.Fatalf("GrepTranscripts(penguin) should be 0 (only orphans have it); got %+v", v)
	}
	// And it must NOT return the orphan's "bounty" either, but DOES return "kept".
	if v := GrepTranscripts(idx, []string{"kept"}); len(v) != 1 || v[0].Source == "" {
		t.Fatalf("GrepTranscripts(kept) should be 1 video-backed hit; got %+v", v)
	}
}

// TestDeriveTranscriptTitle is the title-derivation table: machine scaffolding
// (date / stream_NNN / [id] / .en / extension) is stripped to a human label.
func TestDeriveTranscriptTitle(t *testing.T) {
	cases := []struct{ name, want string }{
		{
			"2025-09-28_stream_390_TakingBack2007 is live! DUCKY IRL STREAM_[H27b7Hmem5E].en.srt",
			"TakingBack2007 is live! DUCKY IRL STREAM",
		},
		{"2025-12-22_Stream Ft. Puff_[AknVT7uuvXY].en.srt", "Stream Ft. Puff"},
		{"plain transcript.srt", "plain transcript"},
		{"20251230_Compact Date Stream_[T0r3hrJPW-g].en.srt", "Compact Date Stream"},
		{"stream_07_No Date Here.vtt", "No Date Here"},
		{"Episode without anything.srt", "Episode without anything"},
		{"2025-09-07_stream_01_Ducky Warz Episode 1.en.srt", "Ducky Warz Episode 1"},
		// All-scaffolding name → falls back to the bare stem (never empty).
		{"2025-09-28_stream_390_[H27b7Hmem5E].en.srt", "2025-09-28_stream_390_[H27b7Hmem5E].en"},
	}
	for _, c := range cases {
		if got := deriveTranscriptTitle(c.name); got != c.want {
			t.Errorf("deriveTranscriptTitle(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestOrphanGrepDeterministic proves two GrepOrphans over the same index produce
// identical ordering (the parse cache must not affect determinism).
func TestOrphanGrepDeterministic(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "transcripts", "2025-01-02_stream_2_Beta Ep_[BBBBBBBBBBB].en.srt"), srtBody("term"))
	writeFile(t, filepath.Join(root, "transcripts", "2025-01-01_stream_1_Alpha Ep_[AAAAAAAAAAA].en.srt"), srtBody("term"))

	idx, _ := Index(root)
	first := GrepOrphans(idx, []string{"term"})
	second := GrepOrphans(idx, []string{"term"})
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected 2 orphan hits each run; got %d / %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Name != second[i].Name {
			t.Fatalf("GrepOrphans not deterministic at %d: %q vs %q", i, first[i].Name, second[i].Name)
		}
	}
	// Equal-score orphans order by Name (Alpha before Beta).
	if first[0].Name != "Alpha Ep" || first[1].Name != "Beta Ep" {
		t.Fatalf("equal-score orphans should order by title; got %q, %q", first[0].Name, first[1].Name)
	}
}

// TestResolveLocalSuffix: becky-clip's local re-transcription
// "<stem>_parakeet_transcription.srt" is recognised as the video's transcript when
// it is the only one present.
func TestResolveLocalSuffix(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kitchen.mov"), "v")
	writeFile(t, filepath.Join(root, "kitchen_parakeet_transcription.srt"), srtBody("unlock"))

	by := indexByName(t, root)
	v, ok := by["kitchen.mov"]
	if !ok || !v.HasTranscript {
		t.Fatalf("kitchen.mov should pair its _parakeet_transcription.srt; got %+v", v)
	}
	if filepath.Base(v.TranscriptPath) != "kitchen_parakeet_transcription.srt" {
		t.Fatalf("transcript = %q, want kitchen_parakeet_transcription.srt", v.TranscriptPath)
	}
	// And it is greppable through the same funnel becky-clip search uses.
	idx, _ := Index(root)
	if got := GrepTranscripts(idx, []string{"unlock"}); len(got) != 1 || got[0].Name != "kitchen.mov" {
		t.Fatalf("grep over resolved parakeet transcript = %+v, want 1 hit", got)
	}
}

// TestResolveLocalWithIDToken: a "<stem>_parakeet_transcription.srt" whose stem
// carries a yt-dlp "[id]" token (the real becky-clip case) is still recognised.
func TestResolveLocalWithIDToken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "2026-06-16_Scene_[46T0KmQA7Eg].mp4"), "v")
	writeFile(t, filepath.Join(root, "2026-06-16_Scene_[46T0KmQA7Eg]_parakeet_transcription.srt"), srtBody("threat"))

	by := indexByName(t, root)
	v, ok := by["2026-06-16_Scene_[46T0KmQA7Eg].mp4"]
	if !ok || !v.HasTranscript {
		t.Fatalf("id-tokened video should pair its _parakeet_transcription.srt; got %+v", v)
	}
	if filepath.Base(v.TranscriptPath) != "2026-06-16_Scene_[46T0KmQA7Eg]_parakeet_transcription.srt" {
		t.Fatalf("transcript = %q, want the parakeet one", v.TranscriptPath)
	}
}

// TestResolveOfficialPreferredOverLocal: when BOTH an official "<stem>.en.srt" and
// a becky "<stem>_parakeet_transcription.srt" sit beside a video, the OFFICIAL one
// is chosen (it is matched by the strict resolver that runs first). This is the
// forensic preference Jordan asked for: keep both versions, surface the original.
func TestResolveOfficialPreferredOverLocal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "stream.mp4"), "v")
	writeFile(t, filepath.Join(root, "stream.en.srt"), srtBody("official"))
	writeFile(t, filepath.Join(root, "stream_parakeet_transcription.srt"), srtBody("localtake"))

	by := indexByName(t, root)
	v, ok := by["stream.mp4"]
	if !ok || !v.HasTranscript {
		t.Fatalf("stream.mp4 should have a transcript; got %+v", v)
	}
	if filepath.Base(v.TranscriptPath) != "stream.en.srt" {
		t.Fatalf("official .en.srt must be preferred over the parakeet secondary; got %q", v.TranscriptPath)
	}
}

// TestResolveBareOfficialPreferredOverLocal: same preference with a bare official
// "<stem>.srt" (no language tag) vs "<stem>_parakeet_transcription.srt".
func TestResolveBareOfficialPreferredOverLocal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "stream.mp4"), "v")
	writeFile(t, filepath.Join(root, "stream.srt"), srtBody("official"))
	writeFile(t, filepath.Join(root, "stream_parakeet_transcription.srt"), srtBody("localtake"))

	by := indexByName(t, root)
	v := by["stream.mp4"]
	if filepath.Base(v.TranscriptPath) != "stream.srt" {
		t.Fatalf("bare official .srt must be preferred over the parakeet secondary; got %q", v.TranscriptPath)
	}
}

// TestResolveLocalMatchExactSuffix: localMatch is exact about the
// "_parakeet_transcription" suffix — a near-miss like "clip_parakeet.srt" is NOT
// claimed by it (it may still pair via the generic boundary rule, which is fine;
// this guards localMatch itself).
func TestResolveLocalMatchExactSuffix(t *testing.T) {
	root := t.TempDir()
	if got := localMatch(root, "clip", nil); got != "" {
		t.Fatalf("localMatch with no parakeet file should be empty, got %q", got)
	}
	writeFile(t, filepath.Join(root, "clip_parakeet.srt"), srtBody("x"))
	if got := localMatch(root, "clip", nil); got != "" {
		t.Fatalf("localMatch must require the exact _parakeet_transcription suffix, matched %q", got)
	}
	writeFile(t, filepath.Join(root, "clip_parakeet_transcription.srt"), srtBody("x"))
	if got := localMatch(root, "clip", nil); filepath.Base(got) != "clip_parakeet_transcription.srt" {
		t.Fatalf("localMatch should find clip_parakeet_transcription.srt, got %q", got)
	}
}

// TestIndex_PairsByYouTubeIDAcrossUnrecognizedSubfolder locks the fix for the
// "0 playable, everything transcript-only" bug: a yt-dlp video in the root must
// pair to its caption even when the caption sits in a subfolder the known-caption-
// subdir rules don't recognise ("english/"), via the shared bracketed [id] token.
func TestIndex_PairsByYouTubeIDAcrossUnrecognizedSubfolder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "2026-02-26_Studio Serotonin 0%_[pNMS91b6Zqo].mp4"), "v")
	// caption in an UNRECOGNISED subfolder, different name, SAME [id]:
	capName := "2026-02-26_stream_390_[pNMS91b6Zqo].en.srt"
	writeFile(t, filepath.Join(root, "english", capName), srtBody("penguin"))

	idx, err := Index(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Videos) != 1 {
		t.Fatalf("want 1 video, got %d", len(idx.Videos))
	}
	v := idx.Videos[0]
	if !v.HasTranscript {
		t.Fatalf("video should pair to the same-[id] caption in english/, but HasTranscript=false")
	}
	if filepath.Base(v.TranscriptPath) != capName {
		t.Fatalf("paired the wrong transcript: %q", v.TranscriptPath)
	}
	if len(idx.Orphans) != 0 {
		t.Fatalf("the paired caption must NOT be an orphan, got %d", len(idx.Orphans))
	}
	// it must surface as a PLAYABLE hit (Source = the video, not "").
	cands := GrepTranscripts(idx, []string{"penguin"})
	if len(cands) == 0 {
		t.Fatalf("expected a playable 'penguin' hit")
	}
	if cands[0].Source == "" {
		t.Fatalf("hit must carry the video source (playable), got empty source")
	}
}
