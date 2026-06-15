package freshness

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadManifest_complete(t *testing.T) {
	deps, err := LoadManifest()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(deps) == 0 {
		t.Fatal("empty manifest")
	}
	seen := map[string]bool{}
	for _, d := range deps {
		if d.ID == "" || d.Name == "" || d.Upstream.Ref == "" {
			t.Errorf("incomplete dep: %+v", d)
		}
		if LatestURL(d.Upstream) == "" {
			t.Errorf("dep %s has unknown upstream type %q", d.ID, d.Upstream.Type)
		}
		if len(d.UsedBy) == 0 {
			t.Errorf("dep %s lists no using tool", d.ID)
		}
		if seen[d.ID] {
			t.Errorf("duplicate dep id %q", d.ID)
		}
		seen[d.ID] = true
	}
	// The whole reason this package exists: the OCR VLM that was missed must now
	// be tracked so it can never be silently forgotten again.
	if !seen["paddleocr-vl"] {
		t.Error("paddleocr-vl missing from manifest — the very thing freshness exists to catch")
	}
}

func TestParseLatest_perType(t *testing.T) {
	cases := []struct {
		u    Upstream
		body string
		want string
	}{
		{Upstream{Type: "github-release"}, `{"tag_name":"v3.7.0"}`, "v3.7.0"},
		{Upstream{Type: "pypi"}, `{"info":{"version":"3.9.0"}}`, "3.9.0"},
		{Upstream{Type: "hf-model"}, `{"lastModified":"2026-05-28T00:00:00.000Z"}`, "updated 2026-05-28T00:00:00.000Z"},
	}
	for _, c := range cases {
		got, err := ParseLatest(c.u, []byte(c.body))
		if err != nil {
			t.Errorf("%s: %v", c.u.Type, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.u.Type, got, c.want)
		}
	}
}

func TestCheck_networkFailureDegrades(t *testing.T) {
	fail := func(string) ([]byte, error) { return nil, errors.New("no network") }
	r := Check(Dependency{ID: "x", Upstream: Upstream{Type: "pypi", Ref: "rapidocr"}}, fail)
	if r.Checked {
		t.Error("should not be marked checked on network failure")
	}
	if r.Error == "" {
		t.Error("expected the network error to be recorded (degrade, not crash)")
	}
}

func TestCheck_success(t *testing.T) {
	get := func(url string) ([]byte, error) {
		if !strings.Contains(url, "pypi.org/pypi/rapidocr") {
			t.Errorf("unexpected url %s", url)
		}
		return []byte(`{"info":{"version":"9.9.9"}}`), nil
	}
	r := Check(Dependency{ID: "rapidocr", Upstream: Upstream{Type: "pypi", Ref: "rapidocr"}}, get)
	if !r.Checked || r.Latest != "9.9.9" {
		t.Errorf("got %+v", r)
	}
}
