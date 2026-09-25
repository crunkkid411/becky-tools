package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

type repo struct {
	Name        string `json:"name"` // owner/repo
	Description string `json:"description"`
	Stars       int    `json:"stars"`
	Language    string `json:"language"`
	License     string `json:"license"`
	Pushed      string `json:"pushed"`
	Readme      string `json:"-"` // excerpt, fed to the model only
	Err         string `json:"error,omitempty"`

	Match painMatch `json:"match"` // closest of Jordan's pain points
}

func gh(args ...string) ([]byte, error) {
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("gh: %s", msg)
	}
	return out, nil
}

// fetchRepo reads a repo's GitHub metadata and the start of its README.
func fetchRepo(name string) repo {
	r := repo{Name: name}
	raw, err := gh("api", "repos/"+name)
	if err != nil {
		r.Err = err.Error()
		return r
	}
	var meta struct {
		FullName    string `json:"full_name"`
		Description string `json:"description"`
		Stars       int    `json:"stargazers_count"`
		Language    string `json:"language"`
		Pushed      string `json:"pushed_at"`
		License     *struct {
			SPDX string `json:"spdx_id"`
		} `json:"license"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		r.Err = "unreadable repo JSON"
		return r
	}
	r.Name, r.Description, r.Stars, r.Language, r.Pushed = meta.FullName, meta.Description, meta.Stars, meta.Language, meta.Pushed
	if meta.License != nil {
		r.License = meta.License.SPDX
	}
	if raw, err := gh("api", "repos/"+name+"/readme"); err == nil {
		var rd struct {
			Content string `json:"content"`
		}
		if json.Unmarshal(raw, &rd) == nil {
			if b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(rd.Content, "\n", "")); err == nil {
				r.Readme = readmeExcerpt(string(b), 1500)
			}
		}
	}
	return r
}

var (
	htmlTagRe = regexp.MustCompile(`<[^>]+>`)
	mdImageRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	blankRe   = regexp.MustCompile(`\n{3,}`)
)

// readmeExcerpt strips badges/HTML so the embedding sees prose, not image links.
func readmeExcerpt(md string, max int) string {
	s := mdImageRe.ReplaceAllString(md, "")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = blankRe.ReplaceAllString(strings.TrimSpace(s), "\n\n")
	if len(s) > max {
		s = s[:max]
	}
	return s
}

func (r repo) text() string {
	s := fmt.Sprintf("GitHub project: %s\nDescription: %s\nLanguage: %s\nREADME:\n%s", r.Name, r.Description, r.Language, r.Readme)
	if len(s) > 1200 {
		s = s[:1200]
	}
	return s
}
