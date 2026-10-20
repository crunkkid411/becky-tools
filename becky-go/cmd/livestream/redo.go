package main

// redo.go - "redo one step" (Jordan, 2026-10-20: iterate on single steps).
// Every step that asks a model or measures something saves its answers in
// becky-edit\ and the next run reuses them, so a crashed run picks up where it
// stopped just by running again. --redo 10 throws away one step's saved answers
// (numbers = the step folders in workflows\video-edit) so that step works again;
// everything else is reused. Steps not listed here run fresh every time.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// redoFiles are the saved answers of each step, by step folder number.
func (r *run) redoFiles(step int) []string {
	t := r.tag
	switch step {
	case 3:
		return []string{r.stem + ".transcript.json"}
	case 4:
		return []string{"becky-cut.json", "source16k.wav"}
	case 5:
		return []string{r.stem + ".sound-v1.json", r.stem + ".picture-v3.json", r.stem + ".posture.json"}
	case 6:
		return []string{"selection-" + t + ".json", "coarse-" + t + ".json", "instructions-" + t + ".json",
			"thinking-" + t + ".json", "show-" + t + ".json", "second-listen.transcript.json"}
	case 7:
		return []string{r.stem + ".chat.json"}
	case 9:
		return []string{"moments-cache.json"}
	case 10:
		return []string{"held-cache.json", "held-gemma.json"}
	case 11:
		return []string{"publish-cache.json"}
	}
	return nil
}

// redo deletes the saved answers of the listed steps ("10" or "6,10").
func (r *run) redo(list string) {
	for _, f := range strings.FieldsFunc(list, func(c rune) bool { return c == ',' || c == ' ' }) {
		n, err := strconv.Atoi(strings.SplitN(f, "_", 2)[0])
		if err != nil {
			fatal(fmt.Sprintf("--redo takes step numbers like 10 (not %q)", f))
		}
		files := r.redoFiles(n)
		if files == nil {
			r.logf("redo step %d: it saves nothing, it runs fresh every time anyway", n)
			continue
		}
		gone := 0
		for _, name := range files {
			if os.Remove(filepath.Join(r.work, name)) == nil {
				gone++
			}
		}
		r.logf("redo step %d: threw away %d saved file(s); this step works again, the rest is reused", n, gone)
	}
}
