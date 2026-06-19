//go:build !gui || !windows

// becky-clip (headless build) — keeps `go build ./...` GREEN on every OS/CI with
// NO build tags (SPEC-BECKY-CLIP §10). The real product is the WebView2 window in
// window_gui.go (//go:build gui && windows); go-webview2 is Windows-only, so this
// stub is what compiles everywhere else. It still exercises the same App engine
// via a tiny CLI so the core loop can be smoke-tested without a window:
//
//	becky-clip                          # explain that the GUI needs the windows build
//	becky-clip info   <folder>          # index a case folder → JSON (videos + transcripts)
//	becky-clip search <folder> <query>  # keyword search across the folder's transcripts → JSON
//	becky-clip call   <verb> [argsJSON] # invoke one bridge verb (for scripted checks)
//
// JSON to stdout, diagnostics to stderr. Offline + deterministic; sources are
// opened READ-ONLY. Exit 0 on success, non-zero on a bad invocation/error.
package main

import (
	"fmt"
	"os"
	"runtime"

	"becky-go/internal/beckyio"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		banner()
		return
	}
	switch args[0] {
	case "info":
		cmdInfo(args[1:])
	case "search":
		cmdSearch(args[1:])
	case "call":
		cmdCall(args[1:])
	case "-h", "--help", "help":
		banner()
	default:
		beckyio.Fatalf("unknown subcommand %q (want info|search|call)", args[0])
	}
}

// banner explains, in plain language, that the interactive editor is the Windows
// GUI build — so a non-Windows run is honest about what it is, never a crash.
func banner() {
	fmt.Fprintf(os.Stderr, `becky-clip — forensic transcript-based video editor

This is the headless build (%s/%s). The interactive editor is a native WebView2
window, built on Windows with:

    go build -tags gui -o bin/becky-clip.exe ./cmd/clip

Headless subcommands (engine smoke tests, work on any OS):
    becky-clip info   <folder>          index a case folder (videos + transcripts)
    becky-clip search <folder> <query>  keyword search across transcripts
    becky-clip call   <verb> [argsJSON] invoke one GUI bridge verb
`, runtime.GOOS, runtime.GOARCH)
}

// cmdInfo indexes a case folder and prints the video/transcript inventory.
func cmdInfo(argv []string) {
	if len(argv) < 1 {
		beckyio.Fatalf("usage: becky-clip info <folder>")
	}
	app := NewApp()
	fv, err := app.OpenFolder(argv[0])
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.PrintJSON(fv)
}

// cmdSearch runs a keyword search over a folder's transcripts.
func cmdSearch(argv []string) {
	if len(argv) < 2 {
		beckyio.Fatalf("usage: becky-clip search <folder> <query>")
	}
	app := NewApp()
	if _, err := app.OpenFolder(argv[0]); err != nil {
		beckyio.Fatalf("%v", err)
	}
	results := app.Search(argv[1])
	beckyio.PrintJSON(map[string]any{"query": argv[1], "count": len(results), "results": results})
}

// cmdCall invokes one bridge verb directly (scripted checks / debugging). The
// args payload is an optional JSON object string, exactly as the GUI sends it.
// An optional third positional [folder] opens that case folder in THIS process
// before the verb runs — needed for headless smoke-testing of verbs that act on
// the open folder (transcribe / transcribe_all / search / add_clip), since each
// CLI invocation is a fresh process with no folder open. The GUI never needs this
// (its long-lived App opens a folder via open_folder/pick_folder first); this is a
// headless convenience only and does not change the bridge/verb contract.
func cmdCall(argv []string) {
	if len(argv) < 1 {
		beckyio.Fatalf("usage: becky-clip call <verb> [argsJSON] [folder]")
	}
	verb := argv[0]
	argsJSON := ""
	if len(argv) > 1 {
		argsJSON = argv[1]
	}
	app := NewApp()
	if len(argv) > 2 && argv[2] != "" {
		if _, err := app.OpenFolder(argv[2]); err != nil {
			beckyio.Fatalf("open folder %q: %v", argv[2], err)
		}
	}
	reply := app.Call(verb, argsJSON)
	fmt.Fprintln(os.Stdout, reply)
}
