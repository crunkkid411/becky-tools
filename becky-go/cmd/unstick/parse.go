package main

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
)

// faraGrid is the fixed coordinate space Fara1.5 answers in
// (microsoft/fara coord_spaces.FARA_DISPLAY_SIZE).
const faraGrid = 1000.0

// parseAction pulls the <tool_call>{...}</tool_call> block out of Fara's reply
// and returns the action with any coordinate scaled to screenshot pixels w x h.
func parseAction(reply string, w, h int) (map[string]any, error) {
	i := strings.Index(reply, "<tool_call>")
	j := strings.LastIndex(reply, "</tool_call>")
	if i < 0 || j < i {
		return nil, errors.New("model reply has no <tool_call> block")
	}
	var call struct {
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(reply[i+len("<tool_call>"):j])), &call); err != nil {
		return nil, errors.New("model tool_call is not valid JSON: " + err.Error())
	}
	args := call.Arguments
	if args == nil || args["action"] == nil {
		return nil, errors.New("model tool_call has no action")
	}
	out := map[string]any{"thoughts": strings.TrimSpace(reply[:i])}
	for k, v := range args {
		out[k] = v
	}
	if c, ok := args["coordinate"].([]any); ok && len(c) == 2 {
		cx, okx := c[0].(float64)
		cy, oky := c[1].(float64)
		if !okx || !oky {
			return nil, errors.New("model coordinate is not two numbers")
		}
		out["x"] = int(math.Round(cx * float64(w) / faraGrid))
		out["y"] = int(math.Round(cy * float64(h) / faraGrid))
	}
	return out, nil
}
