package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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
}

type chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start_time"`
}

func ytdlp(args ...string) ([]byte, error) {
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

// withTempVideo downloads the audio of one video into tempRoot/<id>, runs fn,
// then DELETES that folder - always, success or failure. The deletion is plain
// code: no model has any say in what gets removed, and it can only ever remove
// tempRoot/<11-char video id>.
func withTempVideo(tempRoot, id string, fn func(audioPath string) error) (err error) {
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
	if _, err := ytdlp("-f", "ba[ext=m4a]/ba", "--no-playlist", "-P", dir, "-o", "%(id)s.%(ext)s",
		"https://www.youtube.com/watch?v="+id); err != nil {
		return err
	}
	matches, _ := filepath.Glob(filepath.Join(dir, id+".*"))
	if len(matches) == 0 {
		return fmt.Errorf("download finished but no audio file appeared in %s", dir)
	}
	return fn(matches[0])
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
