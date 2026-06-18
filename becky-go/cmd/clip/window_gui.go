//go:build gui && windows

// window_gui.go is the REAL becky-clip: the native WebView2 window
// (SPEC-BECKY-CLIP §1/§9, R-STACK.md). It is a thin shell over the App engine —
// it starts the loopback media+shell server, opens the WebView2 window on the
// installed Evergreen runtime, binds the single `beckyCall` bridge so the page
// can drive the engine, and (optionally) opens a folder passed on argv.
//
// Built only on Windows with the gui tag (go-webview2 is Windows-only, no cgo):
//
//	go build -tags gui -o bin/becky-clip.exe ./cmd/clip
//
// Everything heavy lives in the cross-platform App; this file is just the window.
// Degrade-never-crash: if the WebView2 runtime is missing (w == nil) it prints a
// plain "install the WebView2 runtime" message and exits cleanly, never panics.
package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jchv/go-webview2"
)

func main() {
	app := NewApp()

	// Start the loopback HTTP server (serves the embedded UI + range-seekable
	// media). If this fails we cannot show anything — report plainly and exit.
	base, err := app.startServer()
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-clip: could not start the local server:", err)
		os.Exit(1)
	}

	// A folder may be passed on the command line (drag-onto-exe / shortcut arg) so
	// the detective lands straight in their case. Best-effort: a bad path just
	// leaves the folder unopened (the UI's Open button still works).
	if folder := firstNonFlagArg(os.Args[1:]); folder != "" {
		if _, err := app.OpenFolder(folder); err != nil {
			fmt.Fprintln(os.Stderr, "becky-clip: could not open folder:", err)
		}
	}

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug: os.Getenv("BECKY_CLIP_DEBUG") != "",
		WindowOptions: webview2.WindowOptions{
			Title:  "becky-clip — forensic video editor",
			Width:  1280,
			Height: 800,
			Center: true,
		},
	})
	if w == nil {
		fmt.Fprintln(os.Stderr,
			"becky-clip: the Microsoft WebView2 runtime is not installed.\n"+
				"Install it (the tiny 'Evergreen Bootstrapper' from microsoft.com/edge/webview2)\n"+
				"and run becky-clip again.")
		os.Exit(2)
	}
	defer w.Destroy()

	// THE bridge: window.beckyCall(verb, argsJSON) → JSON envelope. One bound
	// function is the whole control surface (every verb is allowlisted in
	// dispatch). It returns the JSON string the page parses.
	if err := w.Bind("beckyCall", app.Call); err != nil {
		fmt.Fprintln(os.Stderr, "becky-clip: bind failed:", err)
		os.Exit(1)
	}

	// Tell the page where the media server is so it can build <video> URLs that
	// match the server we just started (the bridge also returns full URLs, but the
	// page reads this on boot for the initial open).
	_ = w.Bind("beckyBase", func() string { return base })

	// Unattended screenshot support (mirrors the spike): exit after N ms so a
	// screenshot can be captured headlessly during verification.
	if v := os.Getenv("SCREENSHOT_MS"); v != "" {
		if ms, e := strconv.Atoi(v); e == nil {
			go func() {
				time.Sleep(time.Duration(ms) * time.Millisecond)
				w.Dispatch(func() { w.Terminate() })
			}()
		}
	}

	// Verification convenience: BECKY_CLIP_DEMO=<folder> makes the page autodrive
	// the core loop (open/search/play/overlay/add) so a screenshot shows it
	// populated. No effect in normal use.
	target := base + "/"
	if demo := strings.TrimSpace(os.Getenv("BECKY_CLIP_DEMO")); demo != "" {
		target = base + "/?demo=" + url.QueryEscape(demo)
		if os.Getenv("BECKY_CLIP_DEMO_EXPORT") != "" {
			target += "&export=1"
		}
	}

	w.Navigate(target)
	w.Run() // blocks until the window is closed / Terminate
}

// firstNonFlagArg returns the first argv entry that is not a -flag (the optional
// folder path), or "".
func firstNonFlagArg(args []string) string {
	for _, a := range args {
		if a == "" || a[0] == '-' {
			continue
		}
		return a
	}
	return ""
}
