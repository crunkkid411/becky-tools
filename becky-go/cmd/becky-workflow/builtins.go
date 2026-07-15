package main

import (
	"embed"
	"sort"

	"becky-go/internal/workflowdef"
)

// builtinFS holds the shipped standard recipes, embedded so becky-workflow works from
// ANY directory (no workflows/ folder required) — the same pattern workflowdef uses for
// its default recipe. User recipes on disk (a --dir folder or an explicit .json path)
// add to, and can shadow, these.
//
//go:embed builtins/*.json
var builtinFS embed.FS

// builtinRecipes parses the embedded standard recipes, keyed by recipe name.
func builtinRecipes() map[string]workflowdef.Recipe {
	out := map[string]workflowdef.Recipe{}
	entries, _ := builtinFS.ReadDir("builtins")
	for _, e := range entries {
		b, err := builtinFS.ReadFile("builtins/" + e.Name())
		if err != nil {
			continue
		}
		r, err := workflowdef.Parse(b)
		if err != nil {
			continue
		}
		out[r.Name] = r
	}
	return out
}

// builtinNames returns the built-in recipe names, sorted (for stable help/errors).
func builtinNames() []string {
	m := builtinRecipes()
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
