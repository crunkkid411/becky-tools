package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/scout"
)

// ytdlpSource is the real, live PlaylistSource: it shells out to yt-dlp to
// resolve a YouTube playlist. This is becky-scout's single, explicit online step
// (the tool is otherwise offline + deterministic). It needs the `yt-dlp` binary
// on PATH at runtime (pip install yt-dlp); if yt-dlp is missing or the network
// fails, the error propagates and Build turns it into a plain-language degrade
// note — never a crash.
//
// Two cost tiers (SPEC §7 #2): the default is a single fast `--flat-playlist -J`
// call that returns titles + ids for the whole playlist. `--deep` additionally
// pulls each video's description, tags and channel (one yt-dlp call per video) —
// far richer signal, so far more videos corroborate to "relevant", at the cost
// of one network request per video.
type ytdlpSource struct {
	bin   string   // yt-dlp binary (BECKY_YTDLP, default "yt-dlp")
	deep  bool     // also fetch per-video description/tags/channel
	extra []string // extra yt-dlp args (BECKY_YTDLP_ARGS; e.g. proxy/cert flags in odd networks)
}

func newYtdlpSource(deep bool) ytdlpSource {
	bin := os.Getenv("BECKY_YTDLP")
	if bin == "" {
		bin = "yt-dlp"
	}
	var extra []string
	if e := strings.TrimSpace(os.Getenv("BECKY_YTDLP_ARGS")); e != "" {
		extra = strings.Fields(e)
	}
	return ytdlpSource{bin: bin, deep: deep, extra: extra}
}

func (y ytdlpSource) run(args ...string) ([]byte, error) {
	cmd := exec.Command(y.bin, append(append([]string{}, y.extra...), args...)...)
	out, err := cmd.Output()
	if err != nil {
		// Surface yt-dlp's own stderr (last line) so the degrade note is useful.
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			lines := strings.Split(strings.TrimSpace(string(ee.Stderr)), "\n")
			msg = lines[len(lines)-1]
		}
		return nil, fmt.Errorf("%s: %s", y.bin, msg)
	}
	return out, nil
}

// Playlist implements scout.PlaylistSource.
func (y ytdlpSource) Playlist(ref string) (scout.Playlist, error) {
	flat, err := y.run("--flat-playlist", "-J", ref)
	if err != nil {
		return scout.Playlist{}, err
	}
	pl, err := parseFlatPlaylist(flat)
	if err != nil {
		return scout.Playlist{}, err
	}
	if !y.deep {
		return pl, nil
	}
	for i := range pl.Videos {
		vj, err := y.run("-J", "--skip-download", pl.Videos[i].URL)
		if err != nil {
			// One bad video must not sink the whole run — keep the flat entry.
			fmt.Fprintf(os.Stderr, "scout: deep fetch failed for %s (%v) — using title only\n", pl.Videos[i].URL, err)
			continue
		}
		applyVideoJSON(&pl.Videos[i], vj)
	}
	return pl, nil
}

// flatEntry / flatPlaylist mirror the subset of yt-dlp's `--flat-playlist -J`
// output that scout needs.
type flatEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

type flatPlaylist struct {
	ID      string      `json:"id"`
	Title   string      `json:"title"`
	WebURL  string      `json:"webpage_url"`
	Entries []flatEntry `json:"entries"`
}

// parseFlatPlaylist turns a yt-dlp flat-playlist JSON dump into a scout.Playlist.
func parseFlatPlaylist(b []byte) (scout.Playlist, error) {
	var fp flatPlaylist
	if err := json.Unmarshal(b, &fp); err != nil {
		return scout.Playlist{}, fmt.Errorf("parse yt-dlp playlist json: %w", err)
	}
	pl := scout.Playlist{ID: fp.ID, Title: fp.Title, URL: fp.WebURL}
	for i, e := range fp.Entries {
		url := e.URL
		if url == "" && e.ID != "" {
			url = "https://youtu.be/" + e.ID
		}
		pl.Videos = append(pl.Videos, scout.Video{
			ID:          e.ID,
			URL:         url,
			Title:       e.Title,
			Description: e.Description,
			Position:    i + 1,
		})
	}
	return pl, nil
}

// videoJSON mirrors the per-video fields scout fills in --deep mode.
type videoJSON struct {
	Title       string   `json:"title"`
	Channel     string   `json:"channel"`
	Uploader    string   `json:"uploader"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// applyVideoJSON merges a per-video yt-dlp `-J` dump into an existing entry,
// filling description/tags/channel (and title if the flat one was empty).
func applyVideoJSON(v *scout.Video, b []byte) {
	var vj videoJSON
	if err := json.Unmarshal(b, &vj); err != nil {
		return // keep what we have; deep enrichment is best-effort
	}
	if vj.Title != "" {
		v.Title = vj.Title
	}
	if vj.Channel != "" {
		v.Channel = vj.Channel
	} else if vj.Uploader != "" {
		v.Channel = vj.Uploader
	}
	if vj.Description != "" {
		v.Description = vj.Description
	}
	if len(vj.Tags) > 0 {
		v.Tags = vj.Tags
	}
}
