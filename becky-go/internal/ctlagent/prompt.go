package ctlagent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// systemPrompt frames the embedded model as becky-edit's editor and pins the
// strict one-JSON-object-per-turn contract. The tool list is injected so the
// model knows its exact, allowlisted control surface (no free-form actions).
func systemPrompt(toolList string) string {
	return `You are the editing assistant inside becky-edit, a forensic video editor.
You change the timeline ONLY by calling tools from the list below. You and the
editor share one live state; after every tool call you are shown the updated state.

RULES:
- Reply with EXACTLY ONE JSON object per turn. No prose outside the JSON.
- To act:    {"tool":"<name>","args":{...},"thought":"<one short reason>"}
- To finish: {"done":true,"message":"<one sentence summary>"}
- Use only the tools and parameters listed. Clip ids look like c1, c2.
- Work step by step: one tool per turn, then read the new state before the next.
- Prefer the smallest set of edits that satisfies the goal. Finish as soon as it is met.
- Originals are read-only; you never alter source files, only the timeline.

TOOLS:
` + strings.TrimRight(toolList, "\n")
}

// userPrompt assembles the per-turn message: the goal, the compact live state,
// and the previous tool result (empty on the first turn).
func userPrompt(goal, digest, last string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "GOAL: %s\n\nSTATE:\n%s", goal, strings.TrimRight(digest, "\n"))
	if strings.TrimSpace(last) != "" {
		fmt.Fprintf(&b, "\n\nLAST RESULT: %s", last)
	}
	b.WriteString("\n\nReply with one JSON tool call, or {\"done\":true,\"message\":\"...\"} if the goal is met.")
	return b.String()
}

// action is the parsed model reply. tool/verb and message/final are accepted as
// aliases so a small model's near-misses still parse.
type action struct {
	Tool    string
	Args    map[string]any
	Done    bool
	Message string
	Thought string
}

// parseAction extracts the first JSON object from the model's text (tolerating
// prose, code fences, or trailing tokens) and maps it to an action. It returns an
// error when no object is found or the object names neither a tool nor done — the
// loop feeds that error back so the model self-repairs.
func parseAction(out string) (action, error) {
	obj, ok := extractJSONObject(out)
	if !ok {
		return action{}, fmt.Errorf("no JSON object found")
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(obj), &raw); err != nil {
		return action{}, fmt.Errorf("invalid JSON: %v", err)
	}
	var a action
	a.Done = truthy(raw["done"])
	a.Thought = str(raw["thought"])
	a.Message = firstNonEmpty(str(raw["message"]), str(raw["final"]))
	a.Tool = firstNonEmpty(str(raw["tool"]), str(raw["verb"]), str(raw["action"]))
	if args, ok := raw["args"].(map[string]any); ok {
		a.Args = args
	} else if params, ok := raw["params"].(map[string]any); ok {
		a.Args = params
	}
	if !a.Done && a.Tool == "" {
		return action{}, fmt.Errorf("object names neither a tool nor done")
	}
	return a, nil
}

// extractJSONObject returns the first balanced {...} span in s, honouring strings
// and escapes so a brace inside a quoted value doesn't end the object early.
func extractJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func truthy(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true" || b == "1" || b == "yes"
	case float64:
		return b != 0
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
