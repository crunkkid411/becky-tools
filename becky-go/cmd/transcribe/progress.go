package main

// progress.go: becky-transcribe always says which step it is on (2026-10-17, Jordan: "there is
// no indicator of progress"). Each step prints one line on stderr, the listening step adds a
// percentage from the helper's per-window "[progress] window i/n" lines, and --progress-file
// keeps the latest line in a small text file so becky-review can show it while it waits.

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

type progressReporter struct {
	mu    sync.Mutex
	file  string // --progress-file ("" = stderr only)
	name  string // video base name, so the line says which video
	step  int
	total int
	label string
	pct   int // last percentage printed for this step (-1 = none)
}

var prog = &progressReporter{}

// stepf starts the next step and reports it.
func (p *progressReporter) stepf(format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.step++
	p.label = fmt.Sprintf(format, a...)
	p.pct = -1
	p.emit("")
}

// percent reports progress inside the current step; stderr only gets every 10%.
func (p *progressReporter) percent(pct int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pct/10 != p.pct/10 || p.pct < 0 {
		p.pct = pct
		p.emit(fmt.Sprintf(" %d%%", pct))
		return
	}
	p.pct = pct
	p.writeFile(fmt.Sprintf(" %d%%", pct))
}

func (p *progressReporter) line(suffix string) string {
	return fmt.Sprintf("step %d of %d: %s%s", p.step, p.total, p.label, suffix)
}

func (p *progressReporter) emit(suffix string) {
	fmt.Fprintf(os.Stderr, "becky-transcribe %s\n", p.line(suffix))
	p.writeFile(suffix)
}

func (p *progressReporter) writeFile(suffix string) {
	if p.file == "" {
		return
	}
	_ = os.WriteFile(p.file, []byte(p.name+" - "+p.line(suffix)), 0o644)
}

// done removes the progress file so a finished run never shows a stale step.
func (p *progressReporter) done() {
	if p.file != "" {
		_ = os.Remove(p.file)
	}
}

// helperStderr collects a helper's stderr: "[progress] window i/n" lines become a percentage,
// everything else is kept for the error message (and echoed with --verbose).
type helperStderr struct {
	verbose bool
	buf     strings.Builder
	partial string
}

func (h *helperStderr) Write(b []byte) (int, error) {
	h.partial += string(b)
	for {
		i := strings.IndexByte(h.partial, '\n')
		if i < 0 {
			break
		}
		ln := h.partial[:i+1]
		h.partial = h.partial[i+1:]
		var cur, n int
		if _, err := fmt.Sscanf(strings.TrimSpace(ln), "[progress] window %d/%d", &cur, &n); err == nil && n > 0 {
			prog.percent(cur * 100 / n)
			continue
		}
		h.buf.WriteString(ln)
		if h.verbose {
			os.Stderr.WriteString(ln)
		}
	}
	return len(b), nil
}

func (h *helperStderr) String() string { return h.buf.String() + h.partial }
