// wiki.go — crawl an Obsidian-style case wiki and extract PERSON entities plus the
// raw video/image files each person appears in. This is the "answer key" reader:
// the wiki already records who the known people are and which media they reference,
// so enrollment needs zero human clip-making.
//
// Two kinds of .md files matter:
//
//  1. PERSON entity files — frontmatter `tags:` contains `person` (or `entity` +
//     `primary`). One file per person (e.g. john-anthony-clancy.md). The H1 title is
//     the display name; "Known Aliases" tables and an `aliases:` field supply
//     aliases. Direct `raw/*.mp4` references in the file attach to that person.
//
//  2. EVIDENCE files — frontmatter `tags:` naming a person slug (e.g. `hair-jordan`)
//     or a "Primary speaker" field. A video referenced by such a file is attached to
//     the named person (one wiki hop). This is how most speaking videos are found,
//     since person files mostly link to evidence rather than embedding videos.
//
// Video paths are resolved relative to the wiki root's sibling `raw/` dir (the spec:
// "raw videos in ..\raw"); only files that actually exist on disk are kept.
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// videoExtSet is the set of extensions treated as enrollable video.
var videoExtSet = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".avi": true,
	".webm": true, ".m4v": true, ".mpg": true, ".mpeg": true,
}

// imageExtSet is the set of extensions treated as an enrollable still image.
var imageExtSet = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
}

// extraMediaRoots are additional directories searched (by base filename) when a
// media reference cannot be resolved relative to the wiki. The becky-tools root
// holds the shared test.mp4 that the VIDEOS wiki references by name.
var extraMediaRoots = []string{
	`X:\AI-2\becky-tools`,
}

// Person is one detected wiki person plus the media that references them.
type Person struct {
	Name        string   // display name (H1 or filename)
	Slug        string   // file stem, lowercased (used to match evidence tags)
	root        string   // the wiki root this person was detected in (evidence scoping)
	Aliases     []string // de-duplicated aliases
	MDSource    string   // wiki-relative path to the person's .md
	VideoRefs   []string // absolute, existing video paths (direct + via evidence)
	ImageRefs   []string // absolute, existing image paths
	SpeakerHint string   // free-text "who speaks" note harvested from evidence (best-effort)
	NonSubject  bool     // role tags mark a legal professional (attorney/prosecution/defense)
}

// mdFile is a parsed wiki markdown file (frontmatter + raw text references).
type mdFile struct {
	path        string   // absolute path
	root        string   // the wiki root this file came from
	relPath     string   // path relative to the wiki root
	stem        string   // filename without extension
	tags        []string // frontmatter tags (lowercased)
	title       string   // first H1
	aliases     []string // frontmatter aliases + "Known Aliases" table values
	speaker     string   // "Primary speaker" field value, if any
	body        string   // full text (used to find the person an evidence file centers on)
	videos      []string // resolved, existing absolute video paths referenced
	images      []string // resolved, existing absolute image paths referenced
	isPerson    bool     // tags mark this as a person/entity
	personSlugs []string // person slugs named in this file's tags (for evidence attach)
}

// Frontmatter / content regexes (compiled once).
var (
	reFrontmatter = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---`)
	reH1          = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)
	reTagsLine    = regexp.MustCompile(`(?m)^tags:\s*(.+)$`)
	reAliasField  = regexp.MustCompile(`(?m)^aliases:\s*(.+)$`)
	reSpeaker     = regexp.MustCompile(`(?i)Primary speaker\s*\|\s*(.+?)\s*\|`)
	// Media references: raw/foo.mp4, ../raw/foo.mp4, bare foo.mp4, or inside
	// [[wikilinks]] / [text](path). Captured then resolved + existence-checked.
	reMediaRef = regexp.MustCompile(`(?i)([A-Za-z0-9_./\\()\[\]'!,&+-]+\.(?:mp4|mov|mkv|avi|webm|m4v|mpg|mpeg|jpg|jpeg|png))`)
)

// crawlWiki reads every .md under each wiki root, classifies person vs evidence
// files, and returns the resolved Person list (alphabetical by name).
func crawlWiki(roots []string, verbose bool) ([]Person, []string, error) {
	var warnings []string
	var files []mdFile
	for _, root := range roots {
		rawDir := siblingRawDir(root)
		md, w := parseWikiRoot(root, rawDir, verbose)
		files = append(files, md...)
		warnings = append(warnings, w...)
	}
	people := assemblePeople(files, verbose)
	return people, warnings, nil
}

// parseWikiRoot walks one wiki root and parses each .md file.
func parseWikiRoot(root, rawDir string, verbose bool) ([]mdFile, []string) {
	var out []mdFile
	var warnings []string
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, []string{"cannot read wiki root " + root + ": " + err.Error()}
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		abs := filepath.Join(root, e.Name())
		mf, perr := parseMD(abs, root, rawDir)
		if perr != nil {
			warnings = append(warnings, "parse "+abs+": "+perr.Error())
			continue
		}
		out = append(out, mf)
	}
	return out, warnings
}

// parseMD reads one markdown file and extracts the fields enrollment needs.
func parseMD(abs, root, rawDir string) (mdFile, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return mdFile{}, err
	}
	text := string(data)
	rel, _ := filepath.Rel(filepath.Dir(root), abs) // e.g. wiki/john.md
	if rel == "" {
		rel = filepath.Base(abs)
	}
	mf := mdFile{
		path:    abs,
		root:    root,
		relPath: filepath.ToSlash(rel),
		stem:    strings.ToLower(strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))),
	}

	front := frontmatter(text)
	mf.tags = parseTags(front)
	mf.aliases = parseAliasField(front)
	mf.title = firstH1(text)
	mf.speaker = parseSpeakerField(text)
	mf.aliases = append(mf.aliases, parseAliasTable(text)...)

	mf.isPerson = tagsMarkPerson(mf.tags)
	mf.personSlugs = personSlugsFromTags(mf.tags)
	mf.body = text

	mf.videos, mf.images = resolveMediaRefs(text, abs, rawDir)
	return mf, nil
}

// assemblePeople builds the Person list: one per person file, augmented with
// videos/images harvested from evidence files that name that person.
func assemblePeople(files []mdFile, verbose bool) []Person {
	// Index person files by slug for evidence matching.
	persons := map[string]*Person{}
	var order []string
	for _, mf := range files {
		if !mf.isPerson {
			continue
		}
		name := mf.title
		if name == "" {
			name = titleizeSlug(mf.stem)
		}
		p := &Person{
			Name:       name,
			Slug:       mf.stem,
			root:       mf.root,
			Aliases:    dedupeStrings(mf.aliases),
			MDSource:   mf.relPath,
			VideoRefs:  append([]string(nil), mf.videos...),
			ImageRefs:  append([]string(nil), mf.images...),
			NonSubject: hasNonSubjectRole(mf.tags),
		}
		persons[mf.stem] = p
		order = append(order, mf.stem)
	}

	// Attach evidence-file media to the person(s) it names — but only to persons
	// detected in the SAME wiki root, so a TRIAL-wiki evidence file does not bleed
	// onto the VIDEOS-wiki "Shelby" (the two wikis describe the same people but keep
	// separate raw/ media; cross-root attachment would resolve to the wrong videos).
	for _, mf := range files {
		if mf.isPerson {
			continue
		}
		if len(mf.videos) == 0 && len(mf.images) == 0 {
			continue
		}
		scoped := personsInRoot(persons, mf.root)
		for _, slug := range matchEvidenceToPersons(mf, scoped) {
			p := persons[slug]
			p.VideoRefs = append(p.VideoRefs, mf.videos...)
			p.ImageRefs = append(p.ImageRefs, mf.images...)
			if p.SpeakerHint == "" && mf.speaker != "" {
				p.SpeakerHint = mf.speaker
			}
		}
	}

	// Count how many distinct people each video is attributed to. A video shared by
	// many people is almost certainly a shared exhibit, not a clean single-speaker
	// source for any one of them — so we rank less-shared videos first per person
	// (a dedicated solo video wins over a multiply-cited exhibit). Recall-first: we
	// keep all refs; we only reorder which the enroller tries first.
	shareCount := map[string]int{}
	for _, slug := range order {
		for _, v := range dedupeStrings(persons[slug].VideoRefs) {
			shareCount[v]++
		}
	}

	out := make([]Person, 0, len(order))
	for _, slug := range order {
		p := persons[slug]
		p.VideoRefs = rankByExclusivity(dedupeStrings(p.VideoRefs), shareCount)
		p.ImageRefs = rankByExclusivity(dedupeStrings(p.ImageRefs), nil)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// rankByExclusivity orders media so the least-shared (most likely person-specific)
// items come first. With a nil shareCount the input order is preserved.
func rankByExclusivity(media []string, shareCount map[string]int) []string {
	if shareCount == nil {
		return media
	}
	out := append([]string(nil), media...)
	sort.SliceStable(out, func(i, j int) bool {
		return shareCount[out[i]] < shareCount[out[j]]
	})
	return out
}

// personsInRoot returns the subset of persons detected in the given wiki root.
func personsInRoot(persons map[string]*Person, root string) map[string]*Person {
	out := map[string]*Person{}
	for slug, p := range persons {
		if p.root == root {
			out[slug] = p
		}
	}
	return out
}

// matchEvidenceToPersons returns the person slugs an evidence file belongs to.
// Primary signal: a person slug appears verbatim in the file's tags. Secondary:
// the "Primary speaker" field names a person (matched against name/aliases).
func matchEvidenceToPersons(mf mdFile, persons map[string]*Person) []string {
	hit := map[string]bool{}
	for _, slug := range mf.personSlugs {
		if _, ok := persons[slug]; ok {
			hit[slug] = true
		}
	}
	// Also match tag tokens like "john-clancy" to person slug "john-anthony-clancy"
	// by name-token overlap, and the Primary speaker field to a person.
	for slug, p := range persons {
		if hit[slug] {
			continue
		}
		if mf.speaker != "" && nameMatches(mf.speaker, p) {
			hit[slug] = true
			continue
		}
		for _, tag := range mf.tags {
			if tagNamesPerson(tag, p) {
				hit[slug] = true
				break
			}
		}
	}
	// Body-centered fallback: if no tag/speaker signal fired but this evidence file
	// references a video AND its body is dominated by one person's name (e.g. John's
	// "YouTube Livestream Threat" file, tagged only `evidence`), attach to that
	// person. Recall-first: the enrollment + self-match step is the precision gate.
	if len(hit) == 0 && len(mf.videos) > 0 {
		if slug := centeredPerson(mf.body, persons); slug != "" {
			hit[slug] = true
		}
	}

	var out []string
	for slug := range hit {
		out = append(out, slug)
	}
	sort.Strings(out)
	return out
}

// centeredPerson returns the person slug whose name dominates an evidence file's
// body, but only when that person is mentioned clearly more than any other (a 2x
// margin and >= 3 mentions). Returns "" when the file is not centered on one
// person, so multi-person evidence is not mis-attributed.
func centeredPerson(body string, persons map[string]*Person) string {
	lower := strings.ToLower(body)
	type score struct {
		slug string
		n    int
	}
	var scores []score
	for slug, p := range persons {
		scores = append(scores, score{slug: slug, n: mentionCount(lower, p)})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].n != scores[j].n {
			return scores[i].n > scores[j].n
		}
		return scores[i].slug < scores[j].slug
	})
	if len(scores) == 0 || scores[0].n < 3 {
		return ""
	}
	if len(scores) > 1 && scores[1].n*2 > scores[0].n {
		return "" // not clearly centered on one person
	}
	return scores[0].slug
}

// mentionCount counts how often a person's name or aliases appear in lowered text.
func mentionCount(lower string, p *Person) int {
	n := 0
	cands := append([]string{p.Name}, p.Aliases...)
	for _, c := range cands {
		c = strings.ToLower(strings.TrimSpace(c))
		if len(c) < 4 {
			continue // skip short/ambiguous aliases (e.g. "JC")
		}
		n += strings.Count(lower, c)
	}
	return n
}

// nameMatches reports whether a free-text name string refers to person p.
func nameMatches(s string, p *Person) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	cands := append([]string{p.Name}, p.Aliases...)
	for _, c := range cands {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" && (s == c || strings.Contains(s, c) || strings.Contains(c, s)) {
			return true
		}
	}
	return false
}

// tagNamesPerson reports whether a tag token (e.g. "hair-jordan") names person p,
// allowing the tag to be a subset of the person's slug tokens (john-clancy ⊂
// john-anthony-clancy).
func tagNamesPerson(tag string, p *Person) bool {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" || !strings.Contains(tag, "-") {
		return false // ignore generic single-word tags (evidence, youtube, ...)
	}
	if tag == p.Slug {
		return true
	}
	tagTokens := strings.Split(tag, "-")
	slugTokens := map[string]bool{}
	for _, t := range strings.Split(p.Slug, "-") {
		slugTokens[t] = true
	}
	// All meaningful tag tokens (len>2) must appear in the slug, and there must be
	// at least two (first+last name) to avoid spurious single-token hits.
	matched := 0
	for _, t := range tagTokens {
		if len(t) <= 2 {
			continue
		}
		if !slugTokens[t] {
			return false
		}
		matched++
	}
	return matched >= 2
}

// resolveMediaRefs extracts every media path mentioned in the text, resolves each
// against likely base dirs, and keeps only those that exist on disk.
func resolveMediaRefs(text, mdAbs, rawDir string) (videos, images []string) {
	mdDir := filepath.Dir(mdAbs)
	seenV, seenI := map[string]bool{}, map[string]bool{}
	for _, m := range reMediaRef.FindAllStringSubmatch(text, -1) {
		raw := strings.TrimSpace(m[1])
		raw = strings.Trim(raw, "[]()'\"`")
		if raw == "" {
			continue
		}
		abs := resolveMediaPath(raw, mdDir, rawDir)
		if abs == "" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(abs))
		switch {
		case videoExtSet[ext] && !seenV[abs]:
			seenV[abs] = true
			videos = append(videos, abs)
		case imageExtSet[ext] && !seenI[abs]:
			seenI[abs] = true
			images = append(images, abs)
		}
	}
	sort.Strings(videos)
	sort.Strings(images)
	return videos, images
}

// resolveMediaPath tries the candidate path against several base directories and
// returns the first that exists, or "" if none do. Absolute paths (incl. other
// drives like E:\) are checked directly and skipped if missing.
func resolveMediaPath(ref, mdDir, rawDir string) string {
	ref = filepath.FromSlash(strings.ReplaceAll(ref, "\\", "/"))
	if filepath.IsAbs(ref) {
		if fileExists(ref) {
			return ref
		}
		return ""
	}
	base := filepath.Base(ref)
	candidates := []string{
		filepath.Join(rawDir, strings.TrimPrefix(ref, "raw"+string(filepath.Separator))), // raw/x.mp4 -> <root>/../raw/x.mp4
		filepath.Join(rawDir, base),              // bare name in raw/
		filepath.Join(mdDir, ref),                // relative to the .md
		filepath.Join(filepath.Dir(rawDir), ref), // relative to wiki package root
	}
	// Fallback media roots: shared test assets referenced by name (e.g. the VIDEOS
	// wiki's test.mp4 lives in the becky-tools root, not the wiki's raw/ dir).
	for _, root := range extraMediaRoots {
		candidates = append(candidates, filepath.Join(root, base))
	}
	for _, c := range candidates {
		if fileExists(c) {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs
			}
			return c
		}
	}
	return ""
}

// --- frontmatter + content helpers ---

func frontmatter(text string) string {
	m := reFrontmatter.FindStringSubmatch(text)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func firstH1(text string) string {
	// Skip frontmatter so an H1 inside it is not mistaken for the title.
	body := reFrontmatter.ReplaceAllString(text, "")
	m := reH1.FindStringSubmatch(body)
	if len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func parseTags(front string) []string {
	m := reTagsLine.FindStringSubmatch(front)
	if len(m) != 2 {
		return nil
	}
	return splitListValue(m[1])
}

func parseAliasField(front string) []string {
	m := reAliasField.FindStringSubmatch(front)
	if len(m) != 2 {
		return nil
	}
	return splitListValue(m[1])
}

func parseSpeakerField(text string) string {
	m := reSpeaker.FindStringSubmatch(text)
	if len(m) == 2 {
		v := strings.TrimSpace(m[1])
		if !strings.EqualFold(v, "Value") { // skip table header rows
			return v
		}
	}
	return ""
}

// parseAliasTable harvests alias values from a "Known Aliases" markdown table.
// Each data row's first cell is taken as an alias (header + separator skipped).
func parseAliasTable(text string) []string {
	lower := strings.ToLower(text)
	idx := strings.Index(lower, "known aliases")
	if idx < 0 {
		return nil
	}
	var aliases []string
	lines := strings.Split(text[idx:], "\n")
	started := false
	for _, ln := range lines[1:] {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "|") {
			if started {
				break // table ended
			}
			continue
		}
		cells := splitTableRow(ln)
		if len(cells) == 0 {
			continue
		}
		first := strings.TrimSpace(cells[0])
		lf := strings.ToLower(first)
		if lf == "alias" || lf == "" || strings.HasPrefix(first, "---") || strings.HasPrefix(first, ":--") {
			started = true
			continue
		}
		started = true
		aliases = append(aliases, first)
	}
	return aliases
}

func splitTableRow(ln string) []string {
	ln = strings.Trim(ln, "|")
	return strings.Split(ln, "|")
}

// splitListValue parses a frontmatter list value, accepting both YAML flow style
// `[a, b]` and a bare comma string `a, b`.
func splitListValue(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.Trim(v, "[]")
	var out []string
	for _, part := range strings.Split(v, ",") {
		p := strings.TrimSpace(strings.Trim(part, `"'`))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func tagsMarkPerson(tags []string) bool {
	hasEntity, hasPerson, hasPrimary := false, false, false
	for _, t := range tags {
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "person":
			hasPerson = true
		case "entity":
			hasEntity = true
		case "primary":
			hasPrimary = true
		}
	}
	// Either an explicit `person` tag, or `entity`+`primary` (the VIDEOS wiki style).
	return hasPerson || (hasEntity && hasPrimary)
}

// hasNonSubjectRole reports whether a person's tags mark them as a legal
// professional whose wiki media are case EXHIBITS they handle, not recordings of
// them. Auto-enrolling such people produces false voice/face prints (the audio is
// the victim/defendant, not the attorney), so they are skipped by default.
func hasNonSubjectRole(tags []string) bool {
	for _, t := range tags {
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "attorney", "prosecution", "prosecutor", "state-attorney",
			"defense-counsel", "judge", "detective", "officer", "investigator":
			return true
		}
	}
	return false
}

// personSlugsFromTags returns hyphenated multi-word tags that look like person
// references (e.g. "hair-jordan"), excluding generic category tags.
func personSlugsFromTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if strings.Contains(t, "-") && !genericTag(t) {
			out = append(out, t)
		}
	}
	return out
}

// genericTag filters out non-person hyphenated tags (categories, not people).
func genericTag(t string) bool {
	switch t {
	case "no-contact-order", "nco-violation", "response-video", "private-recording",
		"public-statement", "prior-bad-acts", "affair-denial", "boxing-match",
		"location-proof", "screen-recording", "phone-call":
		return true
	}
	return false
}

func titleizeSlug(slug string) string {
	parts := strings.FieldsFunc(slug, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		key := strings.TrimSpace(s)
		if key == "" || seen[strings.ToLower(key)] {
			continue
		}
		seen[strings.ToLower(key)] = true
		out = append(out, key)
	}
	return out
}

func siblingRawDir(root string) string {
	return filepath.Join(filepath.Dir(root), "raw")
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
