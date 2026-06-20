# SEAM-PROTOCOL — becky engine↔sidecar NDJSON protocol

This document is the human-readable specification for the wire protocol used between
the Go engine and native sidecar subprocesses (C++ audio host, Rust video preview).

The Go implementation lives in `becky-go/internal/seam/`.
The reference sidecar (for tests and as a porting template) is `becky-go/cmd/seam-echo/`.

---

## Overview

The seam is a **bidirectional, line-delimited JSON** channel over the sidecar's stdin/stdout.

- **Go engine** (controller): writes commands/queries to the sidecar's **stdin**.
- **Sidecar** (C++, Rust, ...): reads stdin, writes responses and events to **stdout**.
- **stderr** is for human-readable logs only; it is never parsed by the controller.
- **Audio data, video frames, and all binary payloads** must NOT cross the seam.
  The seam is a *control plane*. Binary data moves through shared memory, named pipes,
  or files agreed on in the command args.

---

## Message format

Each message is one UTF-8 JSON object per line, `\n`-terminated, flushed immediately.
There must be no embedded newlines within a message.

### Controller to Sidecar: command

Sent when the controller wants the sidecar to perform a state-changing operation.

```json
{"type":"command","id":"r1","name":"transport.play","args":{"bpm":120}}
```

- `type`: always `"command"`
- `id`: unique string for this request (monotonically increasing; e.g. `"r1"`, `"r2"`)
- `name`: the verb (see **Verb naming** below)
- `args`: optional JSON object with verb-specific parameters; omit or `null` for no-args verbs

### Controller to Sidecar: query

Sent when the controller wants to read state without mutating it.

```json
{"type":"query","id":"r7","name":"transport.status","args":null}
```

- `type`: always `"query"`
- All other fields: same as `command`

### Sidecar to Controller: response (success)

Exactly one response per command/query, correlated by `id`.

```json
{"type":"response","id":"r1","ok":true,"data":{"position_frames":0}}
```

- `type`: always `"response"`
- `id`: matches the originating command/query `id`
- `ok`: `true`
- `data`: optional JSON object with result payload; omit for void verbs

### Sidecar to Controller: response (failure)

```json
{"type":"response","id":"r1","ok":false,"error":"transport already playing"}
```

- `type`: always `"response"`
- `id`: matches the originating command/query `id`
- `ok`: `false`
- `error`: human-readable error string; never empty when `ok` is false

### Sidecar to Controller: event (unsolicited push)

Sent at any time, in any order, without a correlation id.

```json
{"type":"event","name":"transport.tick","data":{"position_frames":48000}}
```

- `type`: always `"event"`
- `name`: the event verb
- `data`: optional JSON object; omit for no-payload events

---

## Startup sequence

The **very first line** the sidecar writes to stdout must be the `ready` event:

```json
{"type":"event","name":"ready","data":{"sidecar":"becky-audio-host","version":"0.1.0"}}
```

The controller forwards this event on the `Events()` channel. If a `ready` event is
not the first line, the controller still works -- it just won't receive the startup
handshake.

The sidecar must not accept command/query lines until after it has written `ready`.

---

## Verb naming

Verbs follow the `domain.action` convention. This keeps the verb namespace
organized and readable in logs.

| Prefix | Domain |
|--------|--------|
| `audio.*` | Audio engine (start/stop, level, pan, solo, mute) |
| `vst.*` | VST3 plug-in host (scan, load, open-editor, bypass) |
| `render.*` | Offline render (start, status, cancel) |
| `video.*` | Video preview (open, seek, frame, overlay, window) |
| `transport.*` | Playback transport (play, stop, tick, status) |
| `pad.*` | Drum pad (trigger, level, pan, mute, solo) |
| `fader.*` | Mixer fader (set, get) |

Examples:

```
pad.toggle        -- toggle a pad step on or off
transport.play    -- start playback
transport.tick    -- event: current playback position
fader.set         -- set a fader value
vst.load          -- load a VST3 plug-in into a slot
render.start      -- begin an offline render
video.frame       -- query: get a frame as a file path
```

---

## Protocol rules

1. **One response per request.** Every `command` or `query` received by the sidecar
   must be answered by exactly one `response` with the matching `id`, even if the
   verb is unknown or the args are malformed.

2. **Flush after every line.** The sidecar must flush stdout after writing each JSON
   line. Buffered output breaks correlated request/response.

3. **Never crash on malformed input.** If a line cannot be parsed, the sidecar writes
   a `response` with `ok:false` and the parse error (if an `id` can be extracted);
   otherwise it logs the error to stderr and continues reading.

4. **stdout is pure protocol.** No human-readable text, no progress spinners, no
   partial writes. Everything diagnostic goes to stderr.

5. **Audio/binary data does not cross the seam.** Agree on a shared-memory region,
   temp-file path, or named pipe in the command args; the seam carries only control
   messages.

6. **Exit on stdin close.** When the controller closes the sidecar's stdin (or the
   controller process exits), the sidecar should exit cleanly. The controller's
   `Close()` method closes stdin for this purpose.

7. **Events may arrive at any time.** The controller pump goroutine delivers events
   to the `Events()` channel concurrently with in-flight requests. Neither side
   should block the other.

---

## Wire examples

### Ping / pong (command)

```
Controller -> stdin:  {"type":"command","id":"r1","name":"ping","args":null}
Sidecar -> stdout:    {"type":"response","id":"r1","ok":true,"data":{"reply":"pong"}}
```

### Unknown verb (failure)

```
Controller -> stdin:  {"type":"command","id":"r2","name":"frobnicate","args":{}}
Sidecar -> stdout:    {"type":"response","id":"r2","ok":false,"error":"unknown verb: frobnicate"}
```

### Query with result

```
Controller -> stdin:  {"type":"query","id":"r3","name":"transport.status"}
Sidecar -> stdout:    {"type":"response","id":"r3","ok":true,"data":{"playing":false,"position_frames":0,"bpm":120.0}}
```

### Unsolicited transport tick event

```
Sidecar -> stdout:    {"type":"event","name":"transport.tick","data":{"position_frames":96000}}
```

### Concurrent calls (out-of-order responses are fine)

The controller sends two commands without waiting for the first response:

```
Controller -> stdin:  {"type":"command","id":"r4","name":"render.start","args":{"duration_s":30}}
Controller -> stdin:  {"type":"command","id":"r5","name":"transport.play","args":{}}
Sidecar -> stdout:    {"type":"response","id":"r5","ok":true,"data":{}}
Sidecar -> stdout:    {"type":"response","id":"r4","ok":true,"data":{"job_id":"j001"}}
```

The controller correlates by `id`, so out-of-order responses are handled correctly.

---

## Implementing a new sidecar

Minimum viable C++ sidecar skeleton:

```cpp
#include <iostream>
#include <string>
#include "json.hpp"   // nlohmann/json or equivalent

int main() {
    // Mandatory first line
    std::cout << R"({"type":"event","name":"ready","data":{"sidecar":"my-sidecar","version":"0.1.0"}})" << "\n";
    std::cout.flush();

    std::string line;
    while (std::getline(std::cin, line)) {
        if (line.empty()) continue;
        try {
            auto msg = json::parse(line);
            std::string id   = msg.value("id", "");
            std::string name = msg.value("name", "");

            if (name == "ping") {
                std::cout << json{{"type","response"},{"id",id},{"ok",true},{"data",json{{"reply","pong"}}}} << "\n";
            } else {
                std::cout << json{{"type","response"},{"id",id},{"ok",false},{"error","unknown verb: "+name}} << "\n";
            }
            std::cout.flush();
        } catch (const std::exception& e) {
            std::cerr << "seam: parse error: " << e.what() << "\n";
        }
    }
}
```

Minimum viable Rust sidecar skeleton:

```rust
use serde_json::{json, Value};
use std::io::{self, BufRead, Write};

fn main() {
    let stdout = io::stdout();
    let mut out = io::BufWriter::new(stdout.lock());

    // Mandatory first line
    writeln!(out, r#"{{"type":"event","name":"ready","data":{{"sidecar":"my-sidecar","version":"0.1.0"}}}}"#).unwrap();
    out.flush().unwrap();

    let stdin = io::stdin();
    for line in stdin.lock().lines() {
        let line = line.unwrap();
        if line.is_empty() { continue; }

        let msg: Value = match serde_json::from_str(&line) {
            Ok(v) => v,
            Err(e) => { eprintln!("seam: parse error: {}", e); continue; }
        };

        let id   = msg["id"].as_str().unwrap_or("");
        let name = msg["name"].as_str().unwrap_or("");

        let resp = match name {
            "ping" => json!({"type":"response","id":id,"ok":true,"data":{"reply":"pong"}}),
            _      => json!({"type":"response","id":id,"ok":false,"error":format!("unknown verb: {}", name)}),
        };

        writeln!(out, "{}", resp).unwrap();
        out.flush().unwrap();
    }
}
```

---

## Go controller API (internal/seam)

```go
// Start spawns a sidecar process and returns a controller.
sc, err := seam.Start(ctx, "becky-audio-host")

// Call sends a command or query and blocks until the response or ctx cancel.
data, err := sc.Call(ctx, seam.TypeCommand, "transport.play", map[string]int{"bpm": 120})

// Events returns a channel for unsolicited events (buffered 64; oldest dropped if full).
for ev := range sc.Events() {
    fmt.Println(ev.Name, string(ev.Data))
}

// Close signals the sidecar to exit by closing its stdin.
sc.Close()
```

For unit tests without spawning a real subprocess, use `seam.NewFakeSidecar`:

```go
fake := seam.NewFakeSidecar(func(typ seam.MessageType, name string, args json.RawMessage) (interface{}, string) {
    if name == "ping" {
        return map[string]string{"reply": "pong"}, ""
    }
    return nil, "unknown verb: " + name
})
fake.EmitReady("test-sidecar", "0.1.0")
sc := fake.Controller()

data, err := sc.Call(ctx, seam.TypeCommand, "ping", nil)
```

---

## Invariants (load-bearing)

These are properties the seam guarantees. Violating them in a new sidecar or in Go
controller changes is a bug.

- **Exactly one response per request.** The controller blocks until it receives one.
  Sending two responses for one id, or zero, will cause hangs or panics.
- **Degrade, never crash.** A malformed line from the sidecar must not crash the
  controller. The pump goroutine skips malformed lines silently.
- **Idempotent shutdown.** `Sidecar.Close()` and `Sidecar.shutdown()` are safe to call
  multiple times (via `sync.Once`).
- **No lock held during I/O.** The controller registers the waiter under a mutex, then
  unlocks before writing to stdin. This prevents deadlocks when multiple goroutines call
  `Call()` concurrently (the known failure mode: pump blocks on mutex, pipe buffer fills,
  writer blocks -- deadlock).
- **Events are non-blocking.** The event channel is buffered (64). When full, the oldest
  event is dropped rather than blocking the pump goroutine.
