package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Every yt-dlp call passes --ignore-config: Jordan's global yt-dlp.conf (used
// by many other workflows) is never read or changed by this tool.

type video struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Channel     string    `json:"channel"`
	Description string    `json:"description"`
	Duration    float64   `json:"duration"`
	UploadDate  string    `json:"upload_date"`
	URL         string    `json:"webpage_url"`
	Chapters    []chapter `json:"chapters"`
	// Caption tracks by language: Subtitles were uploaded by the creator,
	// AutoCaptions are YouTube's own speech recognition (and its translations).
	Subtitles    map[string]json.RawMessage `json:"subtitles"`
	AutoCaptions map[string]json.RawMessage `json:"automatic_captions"`
}

type chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start_time"`
}

// Jordan's rule (2026-10-07): at most ONE yt-dlp request per 90 seconds, across
// every becky-intake process. ytdlp() is the only place yt-dlp runs, and it
// always goes through this gate: a lock file so two runs never call at once,
// and a stamp file whose time is the last call's start and end.
const (
	ytdlpGap  = 90 * time.Second
	staleLock = 15 * time.Minute // a run killed mid-call leaves its lock behind
)

var gateDir = `X:\AI-2\becky-tools\research\playlist-intake` // tests point this elsewhere

func waitYtdlpTurn() (done func(), err error) {
	if err := os.MkdirAll(gateDir, 0o755); err != nil {
		return nil, fmt.Errorf("yt-dlp gate: %w", err)
	}
	lock, stamp := filepath.Join(gateDir, "ytdlp.lock"), filepath.Join(gateDir, "ytdlp-last-call.txt")
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			break
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("yt-dlp gate: %w", err)
		}
		if fi, e := os.Stat(lock); e == nil && time.Since(fi.ModTime()) > staleLock {
			_ = os.Remove(lock)
		}
		time.Sleep(2 * time.Second)
	}
	time.Sleep(ytdlpWait(stamp))
	touch := func() { _ = os.WriteFile(stamp, []byte(time.Now().Format(time.RFC3339)), 0o644) }
	touch() // counts even if this process is killed mid-call
	return func() { touch(); _ = os.Remove(lock) }, nil
}

// ytdlpWait is how long to wait so 90 s pass since the last call's stamp.
func ytdlpWait(stamp string) time.Duration {
	fi, err := os.Stat(stamp)
	if err != nil {
		return 0
	}
	if w := ytdlpGap - time.Since(fi.ModTime()); w > 0 {
		return w
	}
	return 0
}

func ytdlp(args ...string) ([]byte, error) {
	done, err := waitYtdlpTurn()
	if err != nil {
		return nil, err
	}
	defer done()
	bin := os.Getenv("BECKY_YTDLP")
	if bin == "" {
		bin = "yt-dlp"
	}
	cmd := exec.Command(bin, append([]string{"--ignore-config"}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			lines := strings.Split(strings.TrimSpace(string(ee.Stderr)), "\n")
			msg = lines[len(lines)-1]
		}
		return nil, fmt.Errorf("yt-dlp: %s", msg)
	}
	return out, nil
}

// playlistIDs lists video ids in playlist order (one cheap flat call).
func playlistIDs(ref string) (string, []string, error) {
	raw, err := ytdlp("--flat-playlist", "-J", ref)
	if err != nil {
		return "", nil, err
	}
	var pl struct {
		Title   string `json:"title"`
		ID      string `json:"id"`
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &pl); err != nil {
		return "", nil, fmt.Errorf("unreadable playlist JSON: %w", err)
	}
	if len(pl.Entries) == 0 { // a single video URL
		return "", []string{pl.ID}, nil
	}
	ids := make([]string, 0, len(pl.Entries))
	for _, e := range pl.Entries {
		ids = append(ids, e.ID)
	}
	return pl.Title, ids, nil
}

func fetchVideo(id string) (video, error) {
	raw, err := ytdlp("-J", "--skip-download", "--no-playlist", "https://www.youtube.com/watch?v="+id)
	if err != nil {
		return video{}, err
	}
	var v video
	if err := json.Unmarshal(raw, &v); err != nil {
		return video{}, fmt.Errorf("unreadable video JSON: %w", err)
	}
	return v, nil
}

var (
	urlRe    = regexp.MustCompile(`https?://[^\s<>()"']+`)
	githubRe = regexp.MustCompile(`(?i)github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)`)
	idRe     = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
)

// facts are what code measures; the model is never asked to count.
type facts struct {
	Minutes  float64  `json:"minutes"`
	Links    int      `json:"links"`
	Repos    []string `json:"repos"` // owner/name, first-seen order, unique
	Chapters int      `json:"chapters"`
}

var notRepoOwners = map[string]bool{"sponsors": true, "orgs": true, "topics": true, "features": true, "marketplace": true, "settings": true}

func measure(v video) facts {
	f := facts{Minutes: v.Duration / 60, Chapters: len(v.Chapters)}
	seen := map[string]bool{}
	for _, u := range urlRe.FindAllString(v.Description, -1) {
		f.Links++
		m := githubRe.FindStringSubmatch(u)
		if m == nil || notRepoOwners[strings.ToLower(m[1])] {
			continue
		}
		repo := m[1] + "/" + strings.TrimSuffix(strings.TrimRight(m[2], "."), ".git")
		if key := strings.ToLower(repo); !seen[key] {
			seen[key] = true
			f.Repos = append(f.Repos, repo)
		}
	}
	return f
}

// withTempDir runs fn with tempRoot/<id> as its download folder, then DELETES
// that folder - always, success or failure. The deletion is plain code: no
// model has any say in what gets removed, and it can only ever remove
// tempRoot/<11-char video id>.
func withTempDir(tempRoot, id string, fn func(dir string) error) (err error) {
	dir, err := tempDirFor(tempRoot, id)
	if err != nil {
		return err
	}
	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil && err == nil {
			err = fmt.Errorf("could not delete temp folder %s: %w", dir, rmErr)
		}
	}()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fn(dir)
}

// downloadAudio fetches one video's audio into dir (a withTempDir folder).
func downloadAudio(dir, id string) (string, error) {
	if _, err := ytdlp("-f", "ba[ext=m4a]/ba", "--no-playlist", "-P", dir, "-o", "%(id)s.%(ext)s",
		"https://www.youtube.com/watch?v="+id); err != nil {
		return "", err
	}
	matches, _ := filepath.Glob(filepath.Join(dir, id+".*"))
	if len(matches) == 0 {
		return "", fmt.Errorf("download finished but no audio file appeared in %s", dir)
	}
	return matches[0], nil
}

// captionTrack picks the YouTube transcript to use: one the creator uploaded,
// else YouTube's speech recognition of the original audio ("en-orig"), else
// YouTube's English version. lang "" means YouTube has none. Only ONE track is
// ever downloaded: asking for two at once got a 429 (too many requests).
func captionTrack(v video) (lang, from string) {
	const creator = "YouTube captions uploaded by the creator"
	if _, ok := v.Subtitles["en"]; ok {
		return "en", creator
	}
	keys := make([]string, 0, len(v.Subtitles))
	for k := range v.Subtitles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.HasPrefix(k, "en-") { // en-US, en-GB...
			return k, creator
		}
	}
	for _, k := range []string{"en-orig", "en"} {
		if _, ok := v.AutoCaptions[k]; ok {
			return k, "YouTube's automatic captions"
		}
	}
	return "", ""
}

// fetchCaptions downloads one caption track into dir and returns it as
// timestamped paragraphs.
func fetchCaptions(dir string, v video, lang string) (string, error) {
	kind := "--write-auto-subs"
	if _, ok := v.Subtitles[lang]; ok {
		kind = "--write-subs"
	}
	if _, err := ytdlp("--skip-download", kind, "--sub-langs", lang, "--sub-format", "json3", "--no-playlist",
		"-P", dir, "-o", "%(id)s.%(ext)s", "https://www.youtube.com/watch?v="+v.ID); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(dir, v.ID+"."+lang+".json3"))
	if err != nil {
		return "", fmt.Errorf("caption download finished but no file appeared: %w", err)
	}
	return json3Text(raw)
}

// json3Text turns YouTube's json3 caption format into readable paragraphs,
// each starting with its time: a new paragraph after 30 s at a sentence end,
// or after 60 s regardless.
func json3Text(raw []byte) (string, error) {
	var d struct {
		Events []struct {
			Start int64 `json:"tStartMs"`
			Segs  []struct {
				Text string `json:"utf8"`
			} `json:"segs"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return "", fmt.Errorf("unreadable caption file: %w", err)
	}
	var out, para strings.Builder
	start := int64(-1)
	flush := func() {
		if s := strings.Join(strings.Fields(para.String()), " "); s != "" {
			fmt.Fprintf(&out, "[%s] %s\n\n", clock(start), s)
		}
		para.Reset()
		start = -1
	}
	for _, e := range d.Events {
		var t strings.Builder
		for _, s := range e.Segs {
			t.WriteString(s.Text)
		}
		text := strings.TrimSpace(t.String())
		if text == "" {
			continue
		}
		if start < 0 {
			start = e.Start
		}
		para.WriteString(t.String() + " ")
		if long := e.Start - start; long >= 60000 || (long >= 30000 && strings.ContainsAny(text[len(text)-1:], ".?!")) {
			flush()
		}
	}
	flush()
	return strings.TrimSpace(out.String()), nil
}

func clock(ms int64) string {
	s := ms / 1000
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// tempDirFor refuses anything but <root named TEMP>/<video id>.
func tempDirFor(tempRoot, id string) (string, error) {
	if !strings.EqualFold(filepath.Base(filepath.Clean(tempRoot)), "TEMP") {
		return "", fmt.Errorf("temp root %q must be a folder named TEMP", tempRoot)
	}
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("refusing temp folder for odd video id %q", id)
	}
	return filepath.Join(tempRoot, id), nil
}

// sweepTemp empties tempRoot of leftovers from a crashed run (e.g. power loss
// mid-download). Same rule: only 11-char video-id folders directly inside a
// folder named TEMP.
func sweepTemp(tempRoot string) {
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		if dir, err := tempDirFor(tempRoot, e.Name()); err == nil && e.IsDir() {
			_ = os.RemoveAll(dir)
		}
	}
}
