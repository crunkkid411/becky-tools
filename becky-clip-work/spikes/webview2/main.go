// Spike B - WebView2 (Go backend + the system WebView2 runtime).
//
// Goal: prove that a Go program can open a native WebView2 window that loads local
// HTML containing a <video> element pointing at a real local H.264 mp4, and that a
// button (and a bound Go function) can drive video.currentTime to seek+play
// instantly - the becky-clip "click a quote -> preview seeks" core interaction.
//
// Binding: github.com/jchv/go-webview2 - pure-Go (no cgo); it loads the WebView2Loader
// via syscall and uses the already-installed Evergreen WebView2 runtime. The window is
// created by Microsoft's Chromium-based WebView2, so <video> H.264/AAC plays natively.
//
// Two backend<->frontend channels are exercised, mirroring the two becky-clip options:
//  1. localhost HTTP - the mp4 (and the page) are served by a tiny net/http server on
//     127.0.0.1 so <video src> is a normal http URL the engine can range-stream.
//  2. bound Go function - w.Bind("reportReady", ...) is callable from JS; the AI router
//     in the real tool will emit JSON the page applies, but this proves the live
//     JS->Go bridge today.
//
// Build/run:
//
//	go build -o becky-clip-webview2-spike.exe .
//	./becky-clip-webview2-spike.exe                       # opens, auto-seeks, screenshots itself
//
// It auto-seeks to 3.0s after load and (when SCREENSHOT_MS is set) exits so a
// screenshot can be taken unattended.
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jchv/go-webview2"
)

// page is the local HTML. It holds a <video> (served over localhost), a row of
// "transcript quote" buttons that each seek the preview to a timecode, and a status
// line. window.reportReady is bound from Go to prove the JS<->Go bridge.
const page = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<style>
  :root { --neon:#39ff14; --bg:#0d0d10; --panel:#16181d; --dim:#9aa0a6; --text:#e8eaec; }
  * { box-sizing:border-box; font-family: "Segoe UI", system-ui, sans-serif; }
  body { margin:0; background:var(--bg); color:var(--text); display:flex; height:100vh; }
  #left { width:34%; background:var(--panel); padding:12px; overflow:auto; }
  #left h2 { color:var(--neon); font-size:15px; margin:4px 0 10px; }
  .q { display:block; width:100%; text-align:left; margin:4px 0; padding:8px;
       background:#14211a; color:var(--text); border:1px solid #223; border-radius:6px;
       cursor:pointer; font-size:13px; }
  .q:hover { border-color:var(--neon); }
  .q.sel { color:var(--neon); border-color:var(--neon); background:#22331f; }
  #right { flex:1; display:flex; flex-direction:column; padding:12px; }
  video { width:100%; background:#000; flex:0 0 auto; max-height:62vh; }
  #bar { margin-top:10px; display:flex; align-items:center; gap:10px; }
  button.act { background:var(--neon); color:#000; border:0; padding:8px 12px;
       border-radius:6px; cursor:pointer; font-weight:600; }
  #status { color:var(--dim); font-size:12px; margin-top:8px; }
  #timeline { margin-top:10px; height:60px; background:var(--panel); border-radius:6px;
       display:flex; gap:6px; padding:8px; align-items:center; }
  .clip { width:110px; height:40px; background:#1e4a2a; border-radius:4px; }
</style>
</head>
<body>
  <div id="left">
    <h2>Transcript / search</h2>
    <div id="quotes"></div>
  </div>
  <div id="right">
    <video id="vid" src="__VIDEO_URL__" controls preload="auto"></video>
    <div id="bar">
      <button class="act" id="append">+ append clip</button>
      <span id="clips" style="color:var(--dim);font-size:13px">timeline clips: 0</span>
    </div>
    <div id="status">loading...</div>
    <div id="timeline"></div>
  </div>
<script>
  // Synthetic transcript quotes (timecode seconds -> label). Not real case data.
  const quotes = [
    [0.2,  "00:00  - and then he walked toward the door"],
    [1.0,  "00:01  - I never said that to anyone"],
    [2.0,  "00:02  - the package was on the table"],
    [3.0,  "00:03  - she handed me the keys at noon"],
    [4.0,  "00:04  - we left before the alarm went off"],
  ];
  const vid = document.getElementById('vid');
  const status = document.getElementById('status');
  const quotesDiv = document.getElementById('quotes');
  const timeline = document.getElementById('timeline');
  let clips = 0;

  function seekTo(t, label, el) {
    document.querySelectorAll('.q').forEach(b => b.classList.remove('sel'));
    if (el) el.classList.add('sel');
    vid.currentTime = t;          // INSTANT seek
    vid.play().catch(()=>{});     // play from there
    status.textContent = "seek -> " + t.toFixed(2) + "s : " + (label||"");
  }

  quotes.forEach(([t,label]) => {
    const b = document.createElement('button');
    b.className = 'q'; b.textContent = label;
    b.onclick = () => seekTo(t, label, b);
    quotesDiv.appendChild(b);
  });

  document.getElementById('append').onclick = () => {
    clips++; document.getElementById('clips').textContent = "timeline clips: " + clips;
    const c = document.createElement('div'); c.className='clip'; timeline.appendChild(c);
  };

  vid.addEventListener('loadeddata', () => {
    status.textContent = "video loaded (" + vid.videoWidth + "x" + vid.videoHeight +
                         ", " + vid.duration.toFixed(1) + "s) - codec played natively";
  });
  vid.addEventListener('error', () => {
    status.textContent = "VIDEO ERROR: " + (vid.error ? vid.error.code : "?") +
                         " (codec unsupported?)";
  });

  // Auto-drive a seek shortly after load so an unattended screenshot shows play state.
  setTimeout(() => {
    const q = quotes[3];                       // "she handed me the keys at noon" @3.0s
    seekTo(q[0], q[1], document.querySelectorAll('.q')[3]);
    if (window.reportReady) window.reportReady("seeked to " + q[0]);
  }, 900);
</script>
</body>
</html>`

func main() {
	// 1) Serve the page + mp4 from a localhost HTTP server (range-streamable).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(1)
	}
	addr := ln.Addr().String()
	videoURL := "http://" + addr + "/test.mp4"

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			html := strings.ReplaceAll(page, "__VIDEO_URL__", videoURL)
			_, _ = w.Write([]byte(html))
			return
		}
		http.NotFound(w, r)
	})
	// http.ServeFile sends Accept-Ranges so the <video> element can seek.
	mux.HandleFunc("/test.mp4", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "test.mp4")
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()

	// 2) Open the WebView2 window (uses the installed Evergreen runtime).
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "becky-clip - WebView2 spike",
			Width:  1100,
			Height: 640,
			Center: true,
		},
	})
	if w == nil {
		fmt.Fprintln(os.Stderr, "WebView2 init failed (runtime missing?)")
		os.Exit(2)
	}
	defer w.Destroy()

	// JS->Go bridge: the page can call window.reportReady(msg).
	_ = w.Bind("reportReady", func(msg string) string {
		fmt.Println("[bridge] page reported:", msg)
		return "ack"
	})

	// Auto-exit timer for unattended screenshotting.
	if v := os.Getenv("SCREENSHOT_MS"); v != "" {
		if ms, e := strconv.Atoi(v); e == nil {
			go func() {
				time.Sleep(time.Duration(ms) * time.Millisecond)
				w.Dispatch(func() { w.Terminate() })
			}()
		}
	}

	w.Navigate("http://" + addr + "/")
	w.Run() // blocks until window closed / Terminate
}
