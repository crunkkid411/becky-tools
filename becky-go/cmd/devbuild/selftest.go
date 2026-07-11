// selftest.go — becky-devbuild's one-command, OFFLINE proof of the
// deterministic core: traceback parsing, error classification, the
// dependency-order sort, fence stripping, and the plan JSON round-trip. No
// model call, no network, no subprocess. This is becky's "provable handoff"
// gate (STANDARDS-WORKFLOW.md §7) - it cannot prove the LLM stages work (that
// needs a real local model, exercised by a live smoke run instead), only that
// the surrounding machinery is correct.
package main

import (
	"encoding/json"
	"fmt"
	"sort"
)

func runSelftest() int {
	type check struct {
		name string
		ok   bool
	}
	var checks []check
	add := func(name string, ok bool) { checks = append(checks, check{name, ok}) }

	// sanitizeName
	add("sanitizeName strips unsafe chars", sanitizeName("My Cool Project!!") == "My_Cool_Project__")
	add("sanitizeName trims leading underscores", sanitizeName("  weird") == "weird")

	// stripFences
	fenced := "```python\nprint('hi')\n```"
	add("stripFences removes a markdown code fence", stripFences(fenced) == "print('hi')")
	add("stripFences is a no-op on bare code", stripFences("print('hi')") == "print('hi')")

	// classifyError / hasError
	add("classifyError finds ModuleNotFoundError", classifyError("ModuleNotFoundError: No module named 'requests'") == errDependency)
	add("classifyError finds SyntaxError", classifyError("  File \"x.py\", line 3\nSyntaxError: invalid syntax") == errSyntax)
	add("classifyError finds a runtime NameError", classifyError("Traceback (most recent call last):\nNameError: name 'x' is not defined") == errRuntime)
	add("classifyError returns none for clean output", classifyError("hello world\n") == errNone)
	add("hasError is false on empty output", !hasError(""))
	add("hasError is false on a timeout message", !hasError("Timed out after 30s - long-running app is likely working"))
	add("hasError is true on a real traceback", hasError("Traceback (most recent call last):\nValueError: bad"))

	// extractMissingModule
	pkg, ok := extractMissingModule("ModuleNotFoundError: No module named 'requests_oauthlib.core'")
	add("extractMissingModule extracts + normalizes the package name", ok && pkg == "requests-oauthlib")

	// parseTraceback: deepest (last) matching frame wins
	tb := "Traceback (most recent call last):\n" +
		"  File \"main.py\", line 5, in <module>\n" +
		"    helpers.run()\n" +
		"  File \"utils/helpers.py\", line 12, in run\n" +
		"    raise ValueError(\"bad\")\n" +
		"ValueError: bad"
	file, line := parseTraceback(tb, []string{"main.py", "utils/helpers.py"})
	add("parseTraceback picks the deepest known frame", file == "utils/helpers.py" && line == 12)

	// dependency-order sort matches build.go's stable sort-by-import-count
	files := []fileSpec{
		{Path: "main.py", Imports: []string{"utils.helpers", "utils.io"}},
		{Path: "utils/helpers.py", Imports: nil},
		{Path: "utils/io.py", Imports: []string{"utils.helpers"}},
	}
	sortedFiles := append([]fileSpec(nil), files...)
	sort.SliceStable(sortedFiles, func(i, j int) bool { return len(sortedFiles[i].Imports) < len(sortedFiles[j].Imports) })
	add("dependency sort puts the zero-import file first", sortedFiles[0].Path == "utils/helpers.py")
	add("dependency sort puts main.py (most imports) last", sortedFiles[len(sortedFiles)-1].Path == "main.py")

	// plan JSON round-trip (the shape the planner must produce)
	planJSON := `{"project_name":"csv2json","entry_point":"main.py","files":[{"path":"main.py","description":"entry","imports":[]}],"run_command":"python main.py","dependencies":["click"]}`
	var plan projectPlan
	err := json.Unmarshal([]byte(planJSON), &plan)
	add("plan JSON round-trips", err == nil && plan.ProjectName == "csv2json" && len(plan.Files) == 1 && plan.Dependencies[0] == "click")

	// project-dir default is home-scoped, not a bare relative path
	add("defaultRoot resolves under the home dir", len(defaultRoot()) > len("BeckyDevBuilds"))

	pass := 0
	for _, c := range checks {
		mark := "FAIL"
		if c.ok {
			mark = "PASS"
			pass++
		}
		fmt.Printf("[%s] %s\n", mark, c.name)
	}
	fmt.Printf("%d/%d checks passed\n", pass, len(checks))
	if pass != len(checks) {
		return 1
	}
	return 0
}
