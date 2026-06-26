package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func req(t *testing.T, id, method, params string) rpcReq {
	t.Helper()
	r := rpcReq{JSONRPC: "2.0", Method: method}
	if id != "" {
		r.ID = json.RawMessage(id)
	}
	if params != "" {
		r.Params = json.RawMessage(params)
	}
	return r
}

// TestInitialize asserts the MCP handshake reply advertises the protocol + tools cap.
func TestInitialize(t *testing.T) {
	resp := handle(req(t, "1", "initialize", ""))
	if resp == nil || resp.Error != nil {
		t.Fatalf("initialize must return a result, got %+v", resp)
	}
	m := resp.Result.(map[string]any)
	if m["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, want %s", m["protocolVersion"], protocolVersion)
	}
	caps, _ := m["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities.tools missing: %+v", m)
	}
}

// TestInitializedNotificationGetsNoReply asserts a notification (no id) is not answered.
func TestInitializedNotificationGetsNoReply(t *testing.T) {
	if resp := handle(req(t, "", "notifications/initialized", "")); resp != nil {
		t.Errorf("a notification must get NO reply, got %+v", resp)
	}
}

// TestToolsList asserts every becky tool AND the workflow are exposed with input schemas.
func TestToolsList(t *testing.T) {
	resp := handle(req(t, "2", "tools/list", ""))
	if resp == nil || resp.Error != nil {
		t.Fatalf("tools/list failed: %+v", resp)
	}
	tools := resp.Result.(map[string]any)["tools"].([]mcpTool)
	if len(tools) == 0 {
		t.Fatal("no tools exposed")
	}
	byName := map[string]mcpTool{}
	for _, x := range tools {
		byName[x.Name] = x
	}
	for _, want := range []string{"becky-transcribe", "becky-identify", "workflow:process-video"} {
		tl, ok := byName[want]
		if !ok {
			t.Errorf("MCP tool %q missing from tools/list", want)
			continue
		}
		if tl.Description == "" {
			t.Errorf("tool %q has empty description", want)
		}
		sch, _ := tl.InputSchema.(map[string]any)
		props, _ := sch["properties"].(map[string]any)
		if _, ok := props["input"]; !ok {
			t.Errorf("tool %q input schema missing 'input' property: %+v", want, tl.InputSchema)
		}
	}
}

// TestToolsCall_WellFormed asserts a tools/call returns the MCP content shape even when
// the underlying binary is absent (cloud): a clean isError result, never a crash.
func TestToolsCall_WellFormed(t *testing.T) {
	resp := handle(req(t, "3", "tools/call",
		`{"name":"becky-transcribe","arguments":{"input":"/no/such/file.mp4"}}`))
	if resp == nil || resp.Error != nil {
		t.Fatalf("tools/call must return a result, got %+v", resp)
	}
	m := resp.Result.(map[string]any)
	content := m["content"].([]map[string]any)
	if len(content) == 0 || content[0]["type"] != "text" {
		t.Errorf("content must be a non-empty text block: %+v", m)
	}
	// the binary doesn't exist in this environment -> isError true, with a message
	if m["isError"] != true {
		t.Errorf("missing binary should be isError=true, got %+v", m["isError"])
	}
	if txt, _ := content[0]["text"].(string); !strings.Contains(txt, "becky-transcribe") {
		t.Errorf("error text should name the tool, got %q", txt)
	}
}

// TestToolsCall_MissingInput asserts the no-input guard.
func TestToolsCall_MissingInput(t *testing.T) {
	resp := handle(req(t, "4", "tools/call", `{"name":"becky-ocr","arguments":{}}`))
	m := resp.Result.(map[string]any)
	if m["isError"] != true {
		t.Errorf("missing input should be isError=true")
	}
}

// TestUnknownMethod asserts an unknown request id gets a JSON-RPC error (not a crash).
func TestUnknownMethod(t *testing.T) {
	resp := handle(req(t, "5", "resources/list", ""))
	if resp == nil || resp.Error == nil {
		t.Fatalf("unknown method must return an error, got %+v", resp)
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code = %d, want -32601 (method not found)", resp.Error.Code)
	}
}
