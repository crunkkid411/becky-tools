package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIngestSteps_KBAddsIdentifyReportAlwaysAppended(t *testing.T) {
	cases := []struct {
		name      string
		userSteps string
		kb        string
		wantHas   []string
		wantNot   []string
	}{
		{
			name: "default set + kb", userSteps: "", kb: "kb-final",
			wantHas: []string{"transcribe", "metadata", "diarize", "events", "osint", "ocr", "identify", "report"},
		},
		{
			name: "default set no kb (no identify)", userSteps: "", kb: "",
			wantHas: []string{"transcribe", "report"}, wantNot: []string{"identify"},
		},
		{
			name: "explicit minimal set still gets report", userSteps: "transcribe", kb: "",
			wantHas: []string{"transcribe", "report"}, wantNot: []string{"events"},
		},
		{
			name: "report not duplicated", userSteps: "transcribe,report", kb: "",
			wantHas: []string{"transcribe", "report"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ingestSteps(tc.userSteps, tc.kb)
			set := strings.Split(got, ",")
			has := func(s string) bool {
				for _, x := range set {
					if x == s {
						return true
					}
				}
				return false
			}
			for _, w := range tc.wantHas {
				if !has(w) {
					t.Errorf("ingestSteps(%q,%q)=%q missing %q", tc.userSteps, tc.kb, got, w)
				}
			}
			for _, w := range tc.wantNot {
				if has(w) {
					t.Errorf("ingestSteps(%q,%q)=%q should not contain %q", tc.userSteps, tc.kb, got, w)
				}
			}
			// report appears exactly once.
			n := 0
			for _, x := range set {
				if x == "report" {
					n++
				}
			}
			if n != 1 {
				t.Errorf("report appears %d times in %q, want 1", n, got)
			}
		})
	}
}

// writeFixture writes a committed-style pipeline-out dir for the --no-pipeline
// golden proof: a manifest plus one clip's report.json + osint-manifest.json.
func writeFixture(t *testing.T, root string) {
	t.Helper()
	clipDir := filepath.Join(root, "reddit-livestream-2025-08-14")
	if err := os.MkdirAll(clipDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Use forward-slash paths in the fixture JSON: a real manifest is json.Marshal'd
	// (backslashes escaped), but this hand-written JSON would be INVALID on Windows
	// where root/clipDir contain backslashes (e.g. \U -> "invalid string escape").
	manifest := `{
  "tool": "becky-pipeline",
  "out_root": "` + filepath.ToSlash(root) + `",
  "videos": [
    {
      "input": "/cases/reddit/reddit-livestream-2025-08-14.mp4",
      "stem": "reddit-livestream-2025-08-14",
      "out_dir": "` + filepath.ToSlash(clipDir) + `",
      "status": "ok",
      "steps": [
        {"name": "transcribe", "status": "ok"},
        {"name": "identify", "status": "ok"},
        {"name": "report", "status": "ok"}
      ]
    }
  ]
}`
	writeFile(t, filepath.Join(root, "manifest.json"), manifest)

	reportJSON := `{
  "source": "reddit-livestream-2025-08-14",
  "duration": 862,
  "entities": [
    {"name": "John Clancy", "type": "voice+face", "confidence": 0.88,
     "corroborated_by": ["voice","face"], "corroborated_count": 2, "concluded": true, "tag": "DOCUMENTED"},
    {"name": "Mark", "type": "voice", "confidence": 0.71, "concluded": false, "tag": "CANDIDATE"}
  ],
  "conclusions": [
    {"what": "John taps her hip", "when": "0:13", "when_sec": 13, "confidence": 0.9, "sources": ["events"], "tag": "DOCUMENTED"}
  ],
  "review_required": [
    {"what": "sub-second motion burst at 588.0s - missed by 1-fps sampling (score 0.82)", "when": "9:48", "when_sec": 588, "confidence": 0.82, "sources": ["motion"], "tag": "CANDIDATE"}
  ]
}`
	writeFile(t, filepath.Join(clipDir, "report.json"), reportJSON)

	osintJSON := `{
  "tool": "becky-osint",
  "source_file": "/cases/reddit/reddit-livestream-2025-08-14.mp4",
  "metadata": {
    "source": "exiftool",
    "capture_time_local": "2025-08-14T19:32:07-05:00",
    "utc_offset": "-05:00",
    "capture_time_source": "quicktime",
    "device_name": "Samsung Galaxy S25 Ultra",
    "gps": {"latitude": 41.8781, "longitude": -87.6298}
  }
}`
	writeFile(t, filepath.Join(clipDir, "osint-manifest.json"), osintJSON)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestIngest_NoPipeline_GoldenProof is the one-command offline proof: it builds a
// fixture pipeline-out dir and runs `becky ingest <out> --no-pipeline`, then
// asserts the generated DIGEST.md matches the checked-in golden and exercises the
// real formatter code path with ZERO models/ffmpeg/network.
func TestIngest_NoPipeline_GoldenProof(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "pipeline-out")
	writeFixture(t, out)

	digestPath := filepath.Join(tmp, "DIGEST.md")
	// runIngest reads positional <folder> (= out here, since --no-pipeline reads
	// <out>), --out, --no-pipeline, --digest, --kb.
	args := []string{out, "--out", out, "--no-pipeline", "--digest", digestPath, "--kb", "kb-final", "--json"}
	if err := runIngest(args); err != nil {
		t.Fatalf("runIngest --no-pipeline failed: %v", err)
	}

	gotBytes, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatalf("read generated DIGEST.md: %v", err)
	}
	got := normalizeForGolden(string(gotBytes), out, tmp)

	goldenPath := filepath.Join("testdata", "DIGEST.golden.md")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated golden %s", goldenPath)
		return
	}

	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create): %v", err)
	}
	// Normalize line endings: on Windows the golden file may be checked out with
	// CRLF (git autocrlf), while the generated digest always uses \n. Compare on
	// content, not on the platform's newline convention.
	gotN := strings.ReplaceAll(got, "\r\n", "\n")
	wantN := strings.ReplaceAll(string(wantBytes), "\r\n", "\n")
	if gotN != wantN {
		t.Errorf("DIGEST.md != golden.\n--- got ---\n%s\n--- want ---\n%s", gotN, wantN)
	}

	// Also assert the load-bearing content directly (so the golden can't be
	// blessed into wrongness).
	for _, w := range []string{
		"John Clancy - DOCUMENTED.",
		"[source: quicktime - trusted]",
		"Mark - CANDIDATE, single signal, not concluded.",
		"GPS 41.878100, -87.629800",
		"sub-second motion burst",
		"- REVIEW",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("DIGEST.md missing load-bearing line %q", w)
		}
	}

	// digest.json must also exist next to the out dir.
	if _, err := os.Stat(filepath.Join(out, "digest.json")); err != nil {
		t.Errorf("digest.json not written: %v", err)
	}
}

// normalizeForGolden strips the volatile parts (the temp paths + the generated
// timestamp) so the golden file is stable across runs/machines.
func normalizeForGolden(s, out, tmp string) string {
	// Slash-agnostic: the digest emits forward slashes, but out/tmp are OS paths
	// (backslashes on Windows). Normalize everything to forward slashes first so the
	// placeholder replacement and the golden compare are identical on Linux + Windows.
	s = filepath.ToSlash(s)
	s = strings.ReplaceAll(s, filepath.ToSlash(out), "<OUT>")
	s = strings.ReplaceAll(s, filepath.ToSlash(tmp), "<TMP>")
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "Generated: ") {
			l = "Generated: <TIMESTAMP>"
		}
		lines = append(lines, l)
	}
	return strings.Join(lines, "\n")
}
