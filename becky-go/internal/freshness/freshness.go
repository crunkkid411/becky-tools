// Package freshness is becky's standard-practice answer to "did we miss an
// upstream improvement?" It carries a manifest of every external model/library/
// binary becky pins (manifest.json), and checks each one against its upstream
// (HuggingFace, GitHub releases, PyPI) so a newer release is surfaced
// automatically instead of waiting for a human to notice. The network step is
// explicit and isolated here (becky's offline tools never call this at runtime).
package freshness

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

//go:embed manifest.json
var manifestJSON []byte

// Upstream says where to watch for a newer version of a dependency.
type Upstream struct {
	Type string `json:"type"` // hf-model | github-release | pypi
	Ref  string `json:"ref"`  // e.g. "PaddlePaddle/PaddleOCR-VL" or "rapidocr"
}

// Dependency is one external thing becky depends on.
type Dependency struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	UsedBy   []string `json:"used_by"`
	Pinned   string   `json:"pinned"`
	Upstream Upstream `json:"upstream"`
	Note     string   `json:"note"`
}

type manifestFile struct {
	Dependencies []Dependency `json:"dependencies"`
}

// LoadManifest returns the embedded dependency manifest.
func LoadManifest() ([]Dependency, error) {
	var m manifestFile
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return m.Dependencies, nil
}

// Result is one dependency's freshness outcome.
type Result struct {
	Dep     Dependency `json:"dependency"`
	Latest  string     `json:"latest,omitempty"` // upstream latest tag/version/date
	Error   string     `json:"error,omitempty"`
	Checked bool       `json:"checked"` // true if upstream was reached and parsed
}

// Getter fetches a URL body. Injectable so tests run fully offline.
type Getter func(url string) ([]byte, error)

// HTTPGet is the default network getter (short timeout, capped body).
func HTTPGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: 12 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "becky-freshness")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

// LatestURL builds the upstream API URL for a dependency (empty = unknown type).
func LatestURL(u Upstream) string {
	switch u.Type {
	case "github-release":
		return "https://api.github.com/repos/" + u.Ref + "/releases/latest"
	case "hf-model":
		return "https://huggingface.co/api/models/" + u.Ref
	case "pypi":
		return "https://pypi.org/pypi/" + u.Ref + "/json"
	}
	return ""
}

// ParseLatest extracts the upstream "latest" marker from an API body per type.
func ParseLatest(u Upstream, body []byte) (string, error) {
	switch u.Type {
	case "github-release":
		var r struct {
			TagName string `json:"tag_name"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return "", err
		}
		return r.TagName, nil
	case "hf-model":
		var r struct {
			LastModified string `json:"lastModified"`
			SHA          string `json:"sha"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return "", err
		}
		if r.LastModified != "" {
			return "updated " + r.LastModified, nil
		}
		return r.SHA, nil
	case "pypi":
		var r struct {
			Info struct {
				Version string `json:"version"`
			} `json:"info"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return "", err
		}
		return r.Info.Version, nil
	}
	return "", fmt.Errorf("unknown upstream type %q", u.Type)
}

// Check fetches and parses the latest upstream marker for one dependency. A
// network failure becomes a per-dep Error (never a crash) — degrade, don't die.
func Check(dep Dependency, get Getter) Result {
	res := Result{Dep: dep}
	url := LatestURL(dep.Upstream)
	if url == "" {
		res.Error = "unknown upstream type: " + dep.Upstream.Type
		return res
	}
	body, err := get(url)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	latest, err := ParseLatest(dep.Upstream, body)
	if err != nil {
		res.Error = "parse upstream: " + err.Error()
		return res
	}
	res.Latest = latest
	res.Checked = true
	return res
}

// CheckAll runs Check across every dependency (sequential; N is tiny).
func CheckAll(deps []Dependency, get Getter) []Result {
	out := make([]Result, 0, len(deps))
	for _, d := range deps {
		out = append(out, Check(d, get))
	}
	return out
}
