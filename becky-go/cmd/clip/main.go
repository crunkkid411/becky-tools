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
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

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
	case "bridge":
		cmdBridge()
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

// cmdBridge is the PERSISTENT engine: one long-lived App (so the folder index +
// transcript parse cache stay WARM = fast repeat searches), driven over stdin/stdout
// as newline-delimited JSON. This is what Becky Review (gui/BeckyReview) spawns once
// per session and talks to — it gives the new WPF/mpv shell the entire becky-clip
// engine (search, timeline, trim, transcribe, assistant, export — every bridge verb)
// without a media server and without re-indexing on every call.
//
// Wire format (one JSON object per line):
//
//	stdin :  {"id":"r1","verb":"search","args":{"query":"cat"}}
//	stdout:  {"id":"r1","reply":{"ok":true,"data":[...]}}        // reply = the App.Call envelope
//
// Each request runs on its own goroutine (a 30-min transcribe never blocks a search),
// replies are tagged by id and may return out of order, and stdout writes are
// serialized. Diagnostics still go to stderr. Matches the GUI bridge's async model
// (window_gui.go) so App.Call concurrency behaves identically.
func cmdBridge() {
	app := NewApp()

	// Becky Review can hand us a reel to pre-load at startup (set by the "Open
	// Forensic Hits" launcher via BECKY_REVIEW_REEL). The page's boot() already
	// fetches `timeline`, so a pre-loaded reel shows on the timeline with NO UI
	// change. Guarded: unset -> identical to before. Degrade-never-crash on a bad path.
	if reelPath := strings.TrimSpace(os.Getenv("BECKY_REVIEW_REEL")); reelPath != "" {
		if _, err := app.LoadReel(reelPath); err != nil {
			fmt.Fprintf(os.Stderr, "becky-review: could not pre-load reel %q: %v\n", reelPath, err)
		}
	}

	out := bufio.NewWriter(os.Stdout)
	var outMu sync.Mutex

	write := func(id, envelope string) {
		outMu.Lock()
		defer outMu.Unlock()
		idJSON, _ := json.Marshal(id)
		out.WriteString(`{"id":`)
		out.Write(idJSON)
		out.WriteString(`,"reply":`)
		if json.Valid([]byte(envelope)) {
			out.WriteString(envelope)
		} else {
			out.WriteString(`{"ok":false,"error":"bad envelope"}`)
		}
		out.WriteString("}\n")
		_ = out.Flush()
	}

	var wg sync.WaitGroup
	scanner := bufio.NewScanner(os.Stdin)
	// Allow large request lines (load_reel/save paths, long ask utterances).
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req struct {
			ID   string          `json:"id"`
			Verb string          `json:"verb"`
			Args json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue // ignore a malformed line rather than die
		}
		argsJSON := ""
		if len(req.Args) > 0 {
			argsJSON = string(req.Args)
		}
		wg.Add(1)
		go func(id, verb, args string) {
			defer wg.Done()
			defer func() {
				if rec := recover(); rec != nil {
					write(id, fmt.Sprintf(`{"ok":false,"error":"internal error: %v"}`, rec))
				}
			}()
			write(id, app.Call(verb, args))
		}(req.ID, req.Verb, argsJSON)
	}
	// Drain in-flight verbs so no reply is lost when stdin closes (shutdown / piped use).
	wg.Wait()
}
