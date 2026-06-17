package studio

// jsonutil.go holds the small JSON + string helpers the model_parser stub uses.
// They mirror the tolerant first-balanced-object scan in
// internal/canvas/model_transformer.go so the local agent's wiring matches the
// house pattern.

import (
	"encoding/json"
	"strings"
)

// marshalCompact renders v as compact JSON (used for the model prompt summary).
func marshalCompact(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// jsonUnmarshal is a thin wrapper so model_parser.go reads cleanly.
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// extractFirstJSONObject scans s for the first balanced '{' … '}' pair (quotes
// and escapes respected) and returns it, or "" if none. Tolerates leading/
// trailing model chatter.
func extractFirstJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escape:
			escape = false
		case c == '\\' && inStr:
			escape = true
		case c == '"':
			inStr = !inStr
		case !inStr && c == '{':
			depth++
		case !inStr && c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
