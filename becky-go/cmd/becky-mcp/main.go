// Package main is becky-mcp: a Model Context Protocol (MCP) stdio server that exposes
// EVERY becky tool and workflow to any MCP-capable agent (Claude Code, etc.) as a
// first-class, schema'd tool. This is THE way an external agent — Jordan's forensic
// agent — actually USES becky tools, instead of shelling raw commands and guessing
// args (which was the "shit show"). The agent connects once; becky's tools then appear
// as native tools with names, descriptions, and input schemas, auto-discovered.
//
// Transport: newline-delimited JSON-RPC 2.0 over stdin/stdout (the MCP stdio standard).
// Methods: initialize, tools/list, tools/call. The tool list is generated from the
// shared internal/catalog (single source of truth) plus internal/workflowdef workflows,
// so it never drifts from the real tools. A tools/call shells the real becky-*.exe
// (where the binaries live, i.e. locally) and returns its output; missing tools degrade
// to a clean isError result rather than crashing the server.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/catalog"
	"becky-go/internal/workflowdef"
)

const protocolVersion = "2024-11-05"

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent => notification (no reply)
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

func fileInputSchema(desc string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{"type": "string", "description": desc},
		},
		"required": []string{"input"},
	}
}

// toolList builds the MCP tool set from the shared catalog + the workflows. Each becky
// tool becomes one MCP tool; each workflow becomes a `workflow:<name>` MCP tool that runs
// the whole chain in one call.
func toolList() []mcpTool {
	ts := make([]mcpTool, 0, len(catalog.ToolCatalog)+1)
	for _, c := range catalog.ToolCatalog {
		ts = append(ts, mcpTool{
			Name:        c.Verb,
			Description: fmt.Sprintf("%s [tier: %s] e.g. %s", c.Summary, c.TierOf(), c.Example),
			InputSchema: fileInputSchema("absolute path to the media file"),
		})
	}
	if r, err := workflowdef.ProcessVideo(); err == nil {
		ts = append(ts, mcpTool{
			Name:        "workflow:" + r.Name,
			Description: fmt.Sprintf("Run the '%s' workflow end-to-end (the becky way: %s).", r.Name, stepSummary(r)),
			InputSchema: fileInputSchema("absolute path to the media file or folder"),
		})
	}
	return ts
}

func stepSummary(r workflowdef.Recipe) string {
	names := make([]string, 0, len(r.Steps))
	for _, s := range r.Steps {
		n := s.Name()
		if s.When != "" {
			n += "?"
		}
		names = append(names, n)
	}
	return strings.Join(names, " -> ")
}

// handle processes one JSON-RPC message. It returns the response to write (nil for a
// notification, which gets no reply) and whether the message was a notification.
func handle(req rpcReq) *rpcResp {
	isNotification := len(req.ID) == 0
	switch req.Method {
	case "initialize":
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "becky", "version": "0.1.0"},
		}}
	case "notifications/initialized", "initialized", "notifications/cancelled":
		return nil // notifications: no reply
	case "ping":
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": toolList()}}
	case "tools/call":
		var p struct {
			Name      string `json:"name"`
			Arguments struct {
				Input string `json:"input"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		text, isErr := runTool(p.Name, p.Arguments.Input)
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}}
	default:
		if isNotification {
			return nil
		}
		return &rpcResp{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}}
	}
}

// runTool invokes a becky tool or workflow and returns (outputText, isError). The actual
// binaries live on the local machine; a missing binary degrades to a clean error string
// (never a panic), so the agent sees a useful message.
func runTool(name, input string) (string, bool) {
	if strings.TrimSpace(input) == "" {
		return "no 'input' argument was given (expected a file path)", true
	}
	if strings.HasPrefix(name, "workflow:") {
		return runWorkflow(strings.TrimPrefix(name, "workflow:"), input)
	}
	if !knownTool(name) {
		return "unknown becky tool: " + name, true
	}
	out, err := shell(name, input)
	if err != nil {
		return fmt.Sprintf("%s failed: %v\n%s", name, err, out), true
	}
	return out, false
}

func knownTool(name string) bool {
	for _, c := range catalog.ToolCatalog {
		if c.Verb == name {
			return true
		}
	}
	return false
}

// runWorkflow runs a named workflow over the input by shelling each tool step in order.
// Conditions are evaluated against facts that accumulate as steps run (full fact probing
// is tuned locally where the real tools run); a step whose condition is false is skipped.
func runWorkflow(name, input string) (string, bool) {
	r, err := workflowdef.ProcessVideo()
	if err != nil || r.Name != name {
		return "unknown workflow: " + name, true
	}
	facts := workflowdef.Facts{}
	var b strings.Builder
	anyErr := false
	results := r.Run(facts, func(step workflowdef.Step, f workflowdef.Facts) (string, error) {
		if step.Kind() != "tool" {
			return "(" + step.Kind() + ": " + step.Name() + ")", nil // verb/merge handled by the engine/local
		}
		out, e := shell(step.Tool, input)
		if e != nil {
			anyErr = true
			return out, e
		}
		return out, nil
	})
	for _, sr := range results {
		if sr.Skipped {
			fmt.Fprintf(&b, "- %s: skipped (condition %q false)\n", sr.Step.Name(), sr.Step.When)
			continue
		}
		status := "ok"
		if sr.Err != nil {
			status = "error: " + sr.Err.Error()
		}
		fmt.Fprintf(&b, "- %s: %s\n", sr.Step.Name(), status)
	}
	return b.String(), anyErr
}

// shell runs `name input`, returning combined output. Never throws.
func shell(name, input string) (string, error) {
	cmd := exec.Command(name, input)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024) // allow large messages
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for in.Scan() {
		line := bytes.TrimSpace(in.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal(line, &req); err != nil {
			continue // ignore unparseable lines (robust transport)
		}
		resp := handle(req)
		if resp == nil {
			continue
		}
		b, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		out.Write(b)
		out.WriteByte('\n')
		out.Flush()
	}
}
