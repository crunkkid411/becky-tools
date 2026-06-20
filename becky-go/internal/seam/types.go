// Package seam implements the becky engine↔sidecar NDJSON-over-stdio protocol.
//
// A sidecar is a native subprocess (C++ audio host, Rust video preview, etc.)
// that the Go engine drives via a simple newline-delimited JSON wire format.
// All musical/forensic state lives in the Go engine; the sidecar is a pure
// rendering/hosting shell that reads commands and emits events.
//
// See SEAM-PROTOCOL.md at the repo root for the full human-readable spec.
package seam

import (
	"encoding/json"
	"fmt"
)

// MessageType is the value of the "type" field in every message.
type MessageType string

const (
	// TypeCommand is sent by the controller to request a state-changing
	// operation. The sidecar must reply with exactly one TypeResponse
	// bearing the same ID.
	TypeCommand MessageType = "command"

	// TypeQuery is sent by the controller to request a state snapshot.
	// The sidecar must reply with exactly one TypeResponse bearing the
	// same ID. Query must not mutate sidecar state.
	TypeQuery MessageType = "query"

	// TypeResponse is sent by the sidecar in reply to a command or query.
	// The "id" field must match the originating command/query.
	TypeResponse MessageType = "response"

	// TypeEvent is sent by the sidecar unsolicited. Events carry async
	// notifications: transport position, meter levels, "ready", job progress.
	// A ready event with name "ready" is emitted on startup before the sidecar
	// accepts commands.
	TypeEvent MessageType = "event"
)

// CommandMsg is the typed view of a controller->sidecar command or query.
//
// Wire shape:
//
//	{"type":"command","id":"<string>","name":"<verb>","args":{...}}
//	{"type":"query",  "id":"<string>","name":"<verb>","args":{...}}
type CommandMsg struct {
	Type MessageType     `json:"type"`
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// ResponseMsg is the typed view of a sidecar->controller response.
//
// Wire shape (success):
//
//	{"type":"response","id":"<same id>","ok":true,"data":{...}}
//
// Wire shape (failure):
//
//	{"type":"response","id":"<same id>","ok":false,"error":"<message>"}
type ResponseMsg struct {
	Type  MessageType     `json:"type"`
	ID    string          `json:"id"`
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// EventMsg is the typed view of an unsolicited sidecar->controller event.
//
// Wire shape:
//
//	{"type":"event","name":"<verb>","data":{...}}
type EventMsg struct {
	Type MessageType     `json:"type"`
	Name string          `json:"name"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ReadyData is the payload of the mandatory "ready" startup event.
// Every sidecar must emit this as its first stdout line.
//
// Wire shape:
//
//	{"type":"event","name":"ready","data":{"sidecar":"becky-audio-host","version":"0.1.0"}}
type ReadyData struct {
	Sidecar string `json:"sidecar"`
	Version string `json:"version"`
}

// Event is delivered to callers via the Events() channel on a Sidecar.
type Event struct {
	// Name is the event verb, e.g. "ready", "transport.tick", "meter.update".
	Name string
	// Data is the raw JSON payload (may be nil for events with no payload).
	Data json.RawMessage
}

// marshalCommand encodes a command or query to JSON bytes (no trailing newline).
func marshalCommand(typ MessageType, id, name string, args interface{}) ([]byte, error) {
	var rawArgs json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("seam: marshal args: %w", err)
		}
		rawArgs = b
	}
	msg := CommandMsg{
		Type: typ,
		ID:   id,
		Name: name,
		Args: rawArgs,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("seam: marshal command: %w", err)
	}
	return b, nil
}

// marshalResponse encodes a success or error response to JSON bytes (no newline).
func marshalResponse(id string, ok bool, data interface{}, errMsg string) ([]byte, error) {
	resp := ResponseMsg{
		Type:  TypeResponse,
		ID:    id,
		OK:    ok,
		Error: errMsg,
	}
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("seam: marshal response data: %w", err)
		}
		resp.Data = b
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("seam: marshal response: %w", err)
	}
	return b, nil
}

// marshalEvent encodes an event to JSON bytes (no newline).
func marshalEvent(name string, data interface{}) ([]byte, error) {
	ev := EventMsg{Type: TypeEvent, Name: name}
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("seam: marshal event data: %w", err)
		}
		ev.Data = b
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("seam: marshal event: %w", err)
	}
	return b, nil
}
