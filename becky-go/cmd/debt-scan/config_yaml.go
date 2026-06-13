// config_yaml.go — optional .becky-debt.yaml loader.
//
// To keep the tool stdlib-only (no go.mod deps) we parse a deliberately small,
// flat subset of YAML that covers everything the scanner needs:
//
//	min_age: 30
//	max_complexity: 10
//	languages: [go, python]            # or a "- item" block
//	categories: [todo, complexity]
//	exclude: [generated/, *_pb.go]     # path substrings to skip
//	deprecated: [my.OldFunc]           # extra deprecated symbols
//	ci_severity: high                  # threshold for --ci
//
// Anything we don't recognize is ignored. CLI flags always win over the file:
// the file fills in defaults the user did not pass. Unknown/invalid files are a
// soft error reported under notes, never fatal.
package main

import (
	"os"
	"strings"
)

// debtConfig mirrors the recognized .becky-debt.yaml keys. Pointers distinguish
// "unset" (nil) from a real zero so CLI overrides compose correctly.
type debtConfig struct {
	MinAge        *int
	MaxComplexity *int
	Languages     []string
	Categories    []string
	Exclude       []string
	Deprecated    []string
	CISeverity    string
}

// loadDebtConfig reads and parses a .becky-debt.yaml. A missing file returns an
// empty config and nil error (config is optional).
func loadDebtConfig(path string) (debtConfig, error) {
	var cfg debtConfig
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	return parseDebtYAML(string(data)), nil
}

// parseDebtYAML parses the flat subset. It supports inline lists ([a, b]) and
// block lists (a key followed by "- item" lines). Comments (#) and blank lines
// are ignored.
func parseDebtYAML(src string) debtConfig {
	var cfg debtConfig
	lines := splitLines(src)
	for i := 0; i < len(lines); i++ {
		line := stripYAMLComment(lines[i])
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		key, val, ok := splitKeyVal(trimmed)
		if !ok {
			continue
		}
		if val == "" {
			// Possible block list: collect following "- item" lines.
			items, consumed := collectBlockList(lines[i+1:])
			i += consumed
			applyList(&cfg, key, items)
			continue
		}
		if isInlineList(val) {
			applyList(&cfg, key, parseInlineList(val))
			continue
		}
		applyScalar(&cfg, key, val)
	}
	return cfg
}

// splitKeyVal splits "key: value" on the first colon.
func splitKeyVal(s string) (key, val string, ok bool) {
	idx := strings.Index(s, ":")
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:]), true
}

// stripYAMLComment removes a trailing # comment that is not inside brackets.
func stripYAMLComment(s string) string {
	depth := 0
	for i, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case '#':
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}

func isInlineList(v string) bool {
	return strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]")
}

func parseInlineList(v string) []string {
	v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(strings.Trim(p, `"'`))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// collectBlockList reads consecutive "- item" lines, returning the items and how
// many input lines it consumed.
func collectBlockList(rest []string) ([]string, int) {
	var items []string
	consumed := 0
	for _, l := range rest {
		t := strings.TrimSpace(stripYAMLComment(l))
		if t == "" {
			consumed++
			continue
		}
		if !strings.HasPrefix(t, "- ") && t != "-" {
			break
		}
		item := strings.TrimSpace(strings.TrimPrefix(t, "-"))
		item = strings.Trim(item, `"'`)
		if item != "" {
			items = append(items, item)
		}
		consumed++
	}
	return items, consumed
}

func applyScalar(cfg *debtConfig, key, val string) {
	val = strings.Trim(val, `"'`)
	switch key {
	case "min_age":
		n := atoi(val)
		cfg.MinAge = &n
	case "max_complexity":
		n := atoi(val)
		cfg.MaxComplexity = &n
	case "ci_severity":
		cfg.CISeverity = val
	}
}

func applyList(cfg *debtConfig, key string, items []string) {
	switch key {
	case "languages":
		cfg.Languages = items
	case "categories":
		cfg.Categories = items
	case "exclude":
		cfg.Exclude = items
	case "deprecated":
		cfg.Deprecated = items
	}
}
