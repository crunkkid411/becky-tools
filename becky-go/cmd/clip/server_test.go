package main

// server_test.go covers the LOAD-BEARING forensic guard: ResolveMediaPath +
// the /media handler must serve ONLY files under the open case folder (or the
// work dir), reject path traversal, reject directories, and serve real media
// with Accept-Ranges so the <video> can range-seek. Synthetic files only.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveMediaPathSecurity(t *testing.T) {
	dir := t.TempDir()
	work := t.TempDir()
	inside := filepath.Join(dir, "clip.mp4")
	mustWriteRaw(t, inside, "video")
	stillInWork := filepath.Join(work, "frame.png")
	mustWriteRaw(t, stillInWork, "png")

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.txt")
	mustWriteRaw(t, outside, "secret")

	app := NewApp()
	app.folder = filepath.Clean(dir)
	app.workDir = filepath.Clean(work)

	// a real file inside the folder resolves.
	if _, ok := app.ResolveMediaPath(inside); !ok {
		t.Error("file inside the open folder should resolve")
	}
	// a produced still inside the work dir resolves.
	if _, ok := app.ResolveMediaPath(stillInWork); !ok {
		t.Error("file inside the work dir should resolve")
	}
	// a file OUTSIDE the scope is rejected.
	if _, ok := app.ResolveMediaPath(outside); ok {
		t.Error("file outside the scope must be rejected")
	}
	// traversal out of the folder is rejected.
	traverse := filepath.Join(dir, "..", filepath.Base(outsideDir), "secret.txt")
	if _, ok := app.ResolveMediaPath(traverse); ok {
		t.Error("path traversal out of the folder must be rejected")
	}
	// a directory is rejected (we only serve files).
	if _, ok := app.ResolveMediaPath(dir); ok {
		t.Error("a directory must be rejected")
	}
	// a missing file is rejected.
	if _, ok := app.ResolveMediaPath(filepath.Join(dir, "ghost.mp4")); ok {
		t.Error("a missing file must be rejected")
	}
	// empty path is rejected.
	if _, ok := app.ResolveMediaPath(""); ok {
		t.Error("empty path must be rejected")
	}
}

func TestResolveMediaPathNoFolderServesNothing(t *testing.T) {
	app := NewApp()
	app.workDir = "" // and no folder
	anyFile := filepath.Join(t.TempDir(), "x.mp4")
	mustWriteRaw(t, anyFile, "x")
	if _, ok := app.ResolveMediaPath(anyFile); ok {
		t.Error("with no folder/work scope, nothing should resolve")
	}
}

func TestServeMediaHandler(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "clip.mp4")
	mustWriteRaw(t, media, "hello-bytes-pretend-mp4")

	app := NewApp()
	app.folder = filepath.Clean(dir)
	app.workDir = filepath.Clean(t.TempDir())

	srv := httptest.NewServer(app.handler())
	defer srv.Close()

	// allowed file → 200 + Accept-Ranges (so <video> can seek).
	resp := mustGet(t, srv.URL+app.mediaQuery(media))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("allowed media: status %d", resp.StatusCode)
	}
	if resp.Header.Get("Accept-Ranges") == "" {
		t.Error("ServeFile must emit Accept-Ranges for range-seekable <video>")
	}
	resp.Body.Close()

	// a Range request returns 206 Partial Content.
	req, _ := http.NewRequest("GET", srv.URL+app.mediaQuery(media), nil)
	req.Header.Set("Range", "bytes=0-3")
	rr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("range GET: %v", err)
	}
	if rr.StatusCode != http.StatusPartialContent {
		t.Errorf("range request want 206, got %d", rr.StatusCode)
	}
	rr.Body.Close()

	// a forbidden path → 403.
	outside := filepath.Join(t.TempDir(), "x.txt")
	mustWriteRaw(t, outside, "x")
	fr := mustGet(t, srv.URL+app.mediaQuery(outside))
	if fr.StatusCode != http.StatusForbidden {
		t.Errorf("out-of-scope media want 403, got %d", fr.StatusCode)
	}
	fr.Body.Close()
}

func TestServeShellAtRoot(t *testing.T) {
	app := NewApp()
	srv := httptest.NewServer(app.handler())
	defer srv.Close()
	resp := mustGet(t, srv.URL+"/")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("shell at / : status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("shell content-type want html, got %q", ct)
	}
}

// mediaQuery builds the /media?path= query relative to the server root (mirrors
// mediaURL but without the base, for httptest).
func (a *App) mediaQuery(absPath string) string {
	full := a.mediaURL(absPath) // base may be "" in tests → "/media?path=..."
	if i := strings.Index(full, "/media"); i >= 0 {
		return full[i:]
	}
	return "/media?path=" + absPath
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func mustWriteRaw(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
