// becky-edit — the forensic NLE bridge (SPEC-BECKY-NLE.md). It is the Go engine +
// brain the forked Shotcut "Becky dock" drives over NDJSON-over-stdio (the
// internal/seam wire shape): the dock sends commands (open a folder, search the
// transcripts, do a tool, run the AI agent), the bridge owns THE shared live
// editor state (internal/editmodel.Project) and answers with the new state +
// the host commands the dock should run against Shotcut.
//
// The built-in AI is the warm Gemma-4 QAT model (the same model becky-validate's
// AVLM uses), driven in text mode through internal/ctlagent's multi-step tool
// loop. Because every edit — whether the human's (mirrored in via "event") or the
// model's (via "agent") — flows through the SAME internal/edittools, the model
// and the program always share one state (Jordan's hard requirement).
//
// Usage:
//
//	becky-edit                 # serve: NDJSON over stdin/stdout (what the dock talks to)
//	becky-edit --selftest      # run the offline proof (no host, no GPU) and exit 0/1
//	becky-edit --version       # print the version
//
// Wire (one JSON object per line):
//
//	-> {"type":"command","id":"1","name":"open_folder","args":{"path":"E:/case"}}
//	<- {"type":"response","id":"1","ok":true,"data":{"videos":3,...}}
//
// Commands: ping · open_folder · state · search · do · event · agent · approve ·
// reject. See bridge.go for each. Exit codes: 0 ok; 1 selftest failed / fatal.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/ctlagent"
	"becky-go/internal/seam"
)

const version = "0.1.0"

func main() {
	selftest := flag.Bool("selftest", false, "run the offline self-test and exit")
	verbose := flag.Bool("verbose", false, "log progress to stderr")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("becky-edit", version)
		return
	}

	logf := func(string, ...any) {}
	if *verbose {
		logf = func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	}

	if *selftest {
		if err := runSelftest(logf); err != nil {
			fmt.Fprintln(os.Stderr, "SELFTEST FAILED:", err)
			os.Exit(1)
		}
		return
	}

	if err := serve(logf); err != nil {
		fmt.Fprintln(os.Stderr, "becky-edit:", err)
		os.Exit(1)
	}
}

// serve is the NDJSON loop the Shotcut dock drives. It emits a "ready" event, then
// reads one command per line and writes one response per line. The warm Gemma
// model loads lazily inside the bridge (the first "agent" call); a missing model
// degrades that one verb, never the whole bridge.
func serve(logf func(string, ...any)) error {
	cfg := config.Load()
	model, note := newLocalModel(cfg, logf)
	var m ctlagent.Model // stays nil (not a non-nil interface over a nil pointer) when no model
	if model != nil {
		m = model
		defer model.Close()
	}
	br := NewBridge(cfg, m, note, logf)

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	// Mandatory ready event (seam convention) so the dock knows it can send commands.
	ready, _ := json.Marshal(seam.EventMsg{
		Type: seam.TypeEvent, Name: "ready",
		Data: mustJSON(seam.ReadyData{Sidecar: "becky-edit", Version: version}),
	})
	writeLine(out, ready)

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req seam.CommandMsg
		if err := json.Unmarshal(line, &req); err != nil {
			writeResp(out, seam.ResponseMsg{Type: seam.TypeResponse, OK: false, Error: "bad json: " + err.Error()})
			continue
		}
		if req.Name == "shutdown" {
			writeResp(out, seam.ResponseMsg{Type: seam.TypeResponse, ID: req.ID, OK: true})
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		resp := br.Dispatch(ctx, req)
		cancel()
		writeResp(out, resp)
	}
	return sc.Err()
}

func writeResp(w *bufio.Writer, resp seam.ResponseMsg) {
	raw, err := json.Marshal(resp)
	if err != nil {
		raw = []byte(`{"type":"response","ok":false,"error":"marshal failed"}`)
	}
	writeLine(w, raw)
}

func writeLine(w *bufio.Writer, raw []byte) {
	w.Write(raw)
	w.WriteByte('\n')
	w.Flush()
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
