// seam-echo is a minimal reference sidecar that speaks the becky NDJSON
// engine<->sidecar protocol (see SEAM-PROTOCOL.md at the repo root).
//
// It is used as the real-subprocess end of the seam package's E2E tests,
// and as a documented starting point for building real C++/Rust sidecars.
//
// Supported verbs:
//
//	ping  (command|query) -- responds with {"reply":"pong"}
//	slow  (command|query) -- waits delay_ms milliseconds, responds {"status":"done"}
//	                         args: {"delay_ms": N}  (N defaults to 200)
//	<any other verb>      -- responds ok:false "unknown verb: <name>"
//
// On startup it emits:
//
//	{"type":"event","name":"ready","data":{"sidecar":"seam-echo","version":"0.1.0"}}
//
// Protocol rules:
//   - Read stdin line-by-line; each line is one JSON object.
//   - EVERY command/query gets exactly one response with the matching id.
//   - Flush after every output line.
//   - Never crash on malformed input; respond ok:false if there is an id to echo.
//   - Logs go to stderr ONLY; stdout is pure protocol.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const (
	sidecarName    = "seam-echo"
	sidecarVersion = "0.1.0"
)

// inMsg is the wire shape of a command or query arriving on stdin.
type inMsg struct {
	Type string          `json:"type"`
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// outMsg is the wire shape of a response or event written to stdout.
type outMsg struct {
	Type  string          `json:"type"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	OK    *bool           `json:"ok,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

var out = bufio.NewWriter(os.Stdout)

// writeLine marshals v to JSON and writes it as one line to stdout, flushing
// immediately so the controller sees it without waiting for buffer fill.
func writeLine(v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: marshal: %v\n", sidecarName, err)
		return
	}
	_, _ = fmt.Fprintf(out, "%s\n", b)
	_ = out.Flush()
}

func boolPtr(v bool) *bool { return &v }

// respond writes a correlated response for the given id.
func respond(id string, ok bool, data interface{}, errMsg string) {
	msg := outMsg{Type: "response", ID: id, OK: boolPtr(ok)}
	if ok && data != nil {
		raw, _ := json.Marshal(data)
		msg.Data = raw
	}
	if !ok {
		msg.Error = errMsg
	}
	writeLine(msg)
}

// emitEvent writes an unsolicited event to stdout.
func emitEvent(name string, data interface{}) {
	raw, _ := json.Marshal(data)
	writeLine(outMsg{Type: "event", Name: name, Data: raw})
}

// handle dispatches a single command/query to the appropriate verb handler.
func handle(msg inMsg) {
	switch msg.Name {
	case "ping":
		respond(msg.ID, true, map[string]string{"reply": "pong"}, "")

	case "slow":
		// Parse optional delay_ms; default 200ms.
		delayMs := 200
		if len(msg.Args) > 0 {
			var a struct {
				DelayMs int `json:"delay_ms"`
			}
			if err := json.Unmarshal(msg.Args, &a); err == nil && a.DelayMs > 0 {
				delayMs = a.DelayMs
			}
		}
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
		respond(msg.ID, true, map[string]string{"status": "done"}, "")

	default:
		respond(msg.ID, false, nil, "unknown verb: "+msg.Name)
	}
}

func main() {
	// The mandatory first output: the "ready" event.
	emitEvent("ready", map[string]string{
		"sidecar": sidecarName,
		"version": sidecarVersion,
	})

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg inMsg
		if err := json.Unmarshal(line, &msg); err != nil {
			// Malformed JSON -- log to stderr, do not write to stdout.
			fmt.Fprintf(os.Stderr, "%s: malformed input: %v\n", sidecarName, err)
			continue
		}

		if msg.ID == "" {
			// No id to correlate -- drop per spec.
			fmt.Fprintf(os.Stderr, "%s: missing id, dropping line\n", sidecarName)
			continue
		}

		if msg.Type != "command" && msg.Type != "query" {
			respond(msg.ID, false, nil, fmt.Sprintf("unexpected type: %s", msg.Type))
			continue
		}

		handle(msg)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: stdin: %v\n", sidecarName, err)
		os.Exit(1)
	}
}
