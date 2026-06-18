package main

// server.go is the localhost HTTP layer (SPEC-BECKY-CLIP §9). It serves two
// things over a loopback-only listener:
//
//  1. The app shell — index.html / app.css / app.js, embedded into the binary via
//     go:embed (single-exe, nothing to ship alongside).
//  2. Media — GET /media?path=<abs> → http.ServeFile, which emits Accept-Ranges so
//     the <video> element can RANGE-SEEK without downloading the whole file. The
//     requested path is validated against the open case folder / work dir
//     (App.ResolveMediaPath) before a single byte is served — the load-bearing
//     forensic guard against serving arbitrary disk. Read-only.
//
// No build tag: the server is pure net/http and is unit-tested with httptest
// against a faked folder. The WebView2 window (windows+gui) just navigates to the
// URL this returns; everything else here runs everywhere.

import (
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// server holds the running HTTP server's base URL and listener so the window can
// navigate to it and the bridge can mint media URLs.
type server struct {
	base string // e.g. "http://127.0.0.1:53124"
	ln   net.Listener
}

// httpState is the App's lazily-started server (one per session).
type httpState struct {
	once sync.Once
	srv  *server
	err  error
}

// startServer starts the loopback media + shell server (idempotent). It returns
// the base URL the window navigates to. Subsequent calls return the same server.
func (a *App) startServer() (string, error) {
	a.http.once.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			a.http.err = fmt.Errorf("listen: %w", err)
			return
		}
		s := &server{base: "http://" + ln.Addr().String(), ln: ln}
		a.http.srv = s
		go func() { _ = http.Serve(ln, a.handler()) }()
	})
	if a.http.err != nil {
		return "", a.http.err
	}
	return a.http.srv.base, nil
}

// baseURL returns the running server base, or "" if not started yet.
func (a *App) baseURL() string {
	if a.http.srv == nil {
		return ""
	}
	return a.http.srv.base
}

// handler builds the mux: the embedded shell at "/", static assets, and the
// guarded /media endpoint.
func (a *App) handler() http.Handler {
	mux := http.NewServeMux()

	// Static assets (index.html at "/", plus app.css / app.js / any future asset).
	sub, err := fs.Sub(assetsFS, "assets")
	if err == nil {
		fileServer := http.FileServer(http.FS(sub))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
			}
			fileServer.ServeHTTP(w, r)
		})
	}

	// Guarded media: only files under the open folder / work dir, range-seekable.
	mux.HandleFunc("/media", a.serveMedia)

	return mux
}

// serveMedia validates ?path against the open case folder (read-only) and streams
// it with http.ServeFile (which sets Accept-Ranges so <video> can seek). A path
// outside scope, a traversal, a directory, or a missing file → 403, never a
// served byte.
func (a *App) serveMedia(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Query().Get("path")
	abs, ok := a.ResolveMediaPath(reqPath)
	if !ok {
		http.Error(w, "forbidden or not found", http.StatusForbidden)
		return
	}
	// http.ServeFile handles Range requests + content type from the extension.
	http.ServeFile(w, r, abs)
}

// mediaURL builds the /media?path= URL for an absolute media path, escaping the
// path as a query value. The window's <video src> uses this; the path is
// re-validated server-side on every request.
func (a *App) mediaURL(absPath string) string {
	base := a.baseURL()
	q := url.Values{}
	q.Set("path", absPath)
	return base + "/media?" + q.Encode()
}

// frameURL builds a /media URL for a produced still PNG (it lives in the work dir,
// which ResolveMediaPath also allows), so the UI can show grabbed frames.
func (a *App) frameURL(absPath string) string {
	if strings.TrimSpace(absPath) == "" {
		return ""
	}
	return a.mediaURL(absPath)
}
