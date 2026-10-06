package main

// claude.go - the Claude run. Claude is reached ONLY through Jordan's OAuth
// session, headless, with the fleet's own wrapper (X:\AI-2\fleet\fleet-run.ps1):
// one of the three sanctioned ways to reach another model (becky-tools
// CLAUDE.md), never a pay-per-token API. One call decides the whole stream, so a
// run costs one Claude session, not one per window.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const fleetRun = `X:\AI-2\fleet\fleet-run.ps1`

// claudeModel is the Claude model the run asks for (BECKY_CLAUDE_MODEL
// overrides; fleet-run accepts opus / sonnet / haiku / fable).
func claudeModel() string {
	if m := strings.TrimSpace(os.Getenv("BECKY_CLAUDE_MODEL")); m != "" {
		return m
	}
	return "opus"
}

func claudeOrder(guidance, sentencesFile string, n int) string {
	return editorRole + keepRule + `

GUIDANCE: ` + guidance + `

The transcript is in this file, one sentence per line, as: id <TAB> time <TAB> text
` + sentencesFile + `
It has ` + strconv.Itoa(n) + ` lines. Read ALL of it before deciding (read it in parts if it is long).

Write your decisions to the output file as plain text, ONE LINE PER SENTENCE, for every id from 0 to ` + strconv.Itoa(n-1) + `, in order:
<id> <label> <keep or cut> <confidence> <optional short note, only when confidence is below 70>

Example lines:
0 meta cut 95
1 narrative keep 90
2 chat_reply cut 60 could still be part of the topic
`
}

// parseClaude reads the decision lines. Every id must be present exactly once.
func parseClaude(path string, n int) ([]Decision, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	got := make([]*Decision, n)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		fs := strings.Fields(strings.Trim(sc.Text(), " \t`|-*"))
		if len(fs) < 4 {
			continue
		}
		id, err1 := strconv.Atoi(fs[0])
		conf, err2 := strconv.Atoi(strings.TrimSuffix(fs[3], "%"))
		keep := strings.EqualFold(fs[2], "keep")
		if err1 != nil || err2 != nil || id < 0 || id >= n || !validLabel(fs[1]) || (!keep && !strings.EqualFold(fs[2], "cut")) {
			continue
		}
		got[id] = &Decision{ID: id, Label: fs[1], Keep: keep, Confidence: min(max(conf, 0), 100),
			Note: strings.Join(fs[4:], " ")}
	}
	var out []Decision
	var missing []string
	for i, d := range got {
		if d == nil {
			missing = append(missing, strconv.Itoa(i))
			continue
		}
		out = append(out, *d)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("Claude's answer is missing %d of %d sentences (ids %s)", len(missing), n, strings.Join(firstN(missing, 12), ", "))
	}
	return out, nil
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return append(xs[:n:n], "...")
	}
	return xs
}

// runClaude writes the transcript + order into the work folder and runs one
// headless Claude session through fleet-run.
func runClaude(ss []Sentence, guidance, work string, logf func(string, ...any)) (Selection, error) {
	start := time.Now()
	model := claudeModel()
	sel := Selection{Model: "claude-" + model, Guidance: guidance}
	if _, err := os.Stat(fleetRun); err != nil {
		return sel, fmt.Errorf("the fleet wrapper is missing (%s), so Claude cannot be reached headless", fleetRun)
	}
	sentFile := filepath.Join(work, "sentences.txt")
	var sb strings.Builder
	for _, s := range ss {
		fmt.Fprintf(&sb, "%d\t%s\t%s\n", s.ID, clock(s.Start), s.Text)
	}
	if err := os.WriteFile(sentFile, []byte(sb.String()), 0o644); err != nil {
		return sel, err
	}
	order := filepath.Join(work, "claude-order.md")
	if err := os.WriteFile(order, []byte(claudeOrder(guidance, sentFile, len(ss))), 0o644); err != nil {
		return sel, err
	}
	outFile := filepath.Join(work, "claude-decisions.txt")
	logf("claude (%s, your subscription, headless): deciding %d sentences...", model, len(ss))
	cmd := exec.Command("pwsh", "-NoProfile", "-File", fleetRun,
		"-Mode", model, "-OrderFile", order, "-OutFile", outFile,
		"-AllowedTools", "Read Write", "-WorkDir", work,
		"-TimeoutMin", "45", "-MaxAttempts", "2", "-MinBytes", strconv.Itoa(8*len(ss)))
	res, err := cmd.CombinedOutput()
	if err != nil {
		logf("  fleet-run: %v\n%s", err, lastLines(string(res), 8))
	}
	ds, perr := parseClaude(outFile, len(ss))
	if perr != nil {
		return sel, fmt.Errorf("the Claude run gave no usable answer: %v (fleet-run said: %s)", perr, lastLines(string(res), 3))
	}
	sel.Decisions = conclude(ds, nil, "claude")
	sel.Seconds = time.Since(start).Seconds()
	return sel, nil
}

func lastLines(s string, n int) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}
